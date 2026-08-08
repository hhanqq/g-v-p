"""Менеджер состояний — раздел 5, стадия 3. Свёртка событий в проблемы
по dedup_key, повторы, флаппинг, TTL-закрытие для источников без резолва.

Ключевое архитектурное решение: если firing приходит на dedup_key вскоре
после resolved (в пределах flap_window_s), СУЩЕСТВУЮЩАЯ Problem
переоткрывается, а не создаётся новая. Без этого счётчик переключений
не пережил бы границу между "эпизодами" и флаппинг не детектировался бы
(раздел 5: "При превышении порога переключений проблема переводится в
FLAPPING... дальнейшие переключения увеличивают счётчик").
"""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import select
from sqlalchemy.orm import Session

from packages.models.db import Problem

ACTIVE_STATUSES = ("OPEN", "ACKNOWLEDGED", "FLAPPING")

DEFAULT_FLAP_THRESHOLD = 6
DEFAULT_FLAP_WINDOW_S = 180

# Раздел 5: TTL по классу симптома — резервный механизм для источников без
# сообщения о восстановлении. В целевой архитектуре живёт в паспорте
# источника (раздел 11.2); здесь — общий дефолт по symptom_class, т.к.
# паспорта источников с индивидуальными TTL пока не разведены.
DEFAULT_TTL_BY_SYMPTOM = {
    "host_unreachable": 600,
    "node_down": 600,
    "interface_down": 300,
    "service_down": 900,
    "power_lost": 900,
    "disk_space": 1800,
    "unknown": 1200,
}


def apply_event(session: Session, *, dedup_key: str | None, state: str, occurred_at: datetime,
                 object_id: str | None, component: str | None, symptom_class: str,
                 site: str | None, flap_threshold: int = DEFAULT_FLAP_THRESHOLD,
                 flap_window_s: int = DEFAULT_FLAP_WINDOW_S) -> Problem | None:
    """Свёртка одного события в проблему.

    Возвращает None, если у события нет dedup_key — карантинные и
    нерезолвленные события в эту стадию не попадают (раздел 4.3 п.6).
    """
    if not dedup_key:
        return None

    latest = session.execute(
        select(Problem).where(Problem.dedup_key == dedup_key).order_by(Problem.id.desc()).limit(1)
    ).scalars().first()

    is_active = latest is not None and latest.status in ACTIVE_STATUSES
    reopen_within_window = (
        latest is not None and not is_active and latest.resolved_at is not None
        and (occurred_at - latest.resolved_at).total_seconds() <= flap_window_s
    )

    if state == "firing":
        if is_active:
            latest.repeat_count += 1
            latest.last_seen_at = occurred_at
            return latest

        if reopen_within_window:
            latest.toggle_count += 1
            latest.repeat_count += 1
            latest.status = "FLAPPING" if latest.toggle_count >= flap_threshold else "OPEN"
            latest.resolved_at = None
            latest.last_seen_at = occurred_at
            return latest

        problem = Problem(dedup_key=dedup_key, status="OPEN", object_id=object_id,
                           component=component, symptom_class=symptom_class, site=site,
                           opened_at=occurred_at, last_seen_at=occurred_at,
                           repeat_count=1, toggle_count=0)
        session.add(problem)
        session.flush()  # нужен problem.id для трассировки из Event (И4)
        return problem

    # state == "resolved"
    if is_active:
        latest.status = "RESOLVED"
        latest.resolved_at = occurred_at
        latest.last_seen_at = occurred_at
        return latest
    return None  # резолв без активной проблемы — нет на чём закрывать


def close_stale_problems(session: Session, now: datetime,
                          ttl_by_symptom: dict[str, int] | None = None) -> int:
    """Раздел 5 — TTL вместо цикла сверки. closed_by_reconciliation=True,
    чтобы TTL-закрытия не искажали MTTR как настоящие подтверждения."""
    ttl_by_symptom = ttl_by_symptom or DEFAULT_TTL_BY_SYMPTOM
    active = session.execute(select(Problem).where(Problem.status.in_(ACTIVE_STATUSES))).scalars().all()
    closed = 0
    for problem in active:
        ttl = ttl_by_symptom.get(problem.symptom_class, DEFAULT_TTL_BY_SYMPTOM["unknown"])
        if (now - problem.last_seen_at).total_seconds() > ttl:
            problem.status = "RESOLVED"
            problem.resolved_at = problem.last_seen_at
            problem.closed_by_reconciliation = True
            closed += 1
    return closed


def mttr_seconds(problem: Problem) -> float | None:
    if problem.resolved_at is None:
        return None
    return (problem.resolved_at - problem.opened_at).total_seconds()
