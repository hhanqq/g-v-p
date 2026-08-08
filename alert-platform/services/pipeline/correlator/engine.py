"""Коррелятор — раздел 6. Конструктор правил, не алгоритм: правило описывает
пару "симптом/причина" плюс оси совпадения и окно (раздел 6.4). В этой
версии реализована статическая часть конструктора — правила читаются из
БД (`CorrelationRule`, наполняется `scripts/seed_demo.py` из
`packages/rules/correlations/*.yaml`); диалоговое создание правил из
разбора инцидента (раздел 10, M9) и симулятор/теневой режим (раздел 6.4,
M13) — не реализованы, это отдельные будущие этапы.

Раздел 6.7, защита от ошибок: кандидат в первопричину не подавляется —
если problem уже корень какого-то инцидента, try_correlate для него
не запускается вообще (см. проверку is_already_root).
"""
from __future__ import annotations

from datetime import timedelta

from sqlalchemy import select
from sqlalchemy.orm import Session

from packages.models.db import CmdbObject, CorrelationRule, Incident, IncidentProblem, Problem

ACTIVE_STATUSES = ("OPEN", "ACKNOWLEDGED", "FLAPPING")


def _cmdb(session: Session, object_id: str | None) -> CmdbObject | None:
    if not object_id:
        return None
    return session.get(CmdbObject, object_id)


def _topology_matches(session: Session, trigger_object_id: str | None, cause_object_id: str | None) -> bool:
    trigger_obj = _cmdb(session, trigger_object_id)
    if trigger_obj is None or cause_object_id is None:
        return False
    if trigger_obj.parent_switch_id == cause_object_id:
        return True
    parent = _cmdb(session, trigger_obj.parent_switch_id)
    return parent is not None and parent.parent_switch_id == cause_object_id


def _axis_matches(session: Session, axis: str, trigger: Problem, cause: Problem) -> bool:
    if axis == "site":
        return trigger.site is not None and trigger.site == cause.site
    if axis == "subnet":
        t_obj, c_obj = _cmdb(session, trigger.object_id), _cmdb(session, cause.object_id)
        return bool(t_obj and c_obj and t_obj.subnet and t_obj.subnet == c_obj.subnet)
    if axis == "topology":
        return _topology_matches(session, trigger.object_id, cause.object_id)
    return False  # неизвестная ось — не совпадает, а не ошибка (раздел И5)


def _find_cause(session: Session, rule: CorrelationRule, trigger: Problem) -> Problem | None:
    """Кандидат в причину — либо ЕЩЁ активен (тогда он причина независимо
    от того, когда открылся: коммутатор, лежащий 4 часа, всё ещё причина
    недоступности хоста на 4-м часу), либо разрешился НЕДАВНО, в пределах
    window_s (симптом мог быть замечен платформой с небольшой задержкой
    относительно фактического восстановления причины).

    Раньше здесь сравнивался trigger.opened_at с cause.opened_at — из-за
    этого причина, которую фоновый шум держал открытой часами (переоткрывая
    в пределах окна флаппинга много раз подряд), перестававала быть
    кандидатом уже через window_s после САМОГО ПЕРВОГО открытия, хотя
    оставалась активной. Раздел 6.1: "первопричина" определяется тем, что
    она ещё не устранена, а не тем, когда она возникла.
    """
    axes = [a.strip() for a in rule.match_axes.split(",") if a.strip()]

    candidates = session.execute(
        select(Problem).where(
            Problem.symptom_class == rule.cause_symptom,
            Problem.opened_at <= trigger.opened_at,
            Problem.id != trigger.id,
            (Problem.status.in_(ACTIVE_STATUSES))
            | ((Problem.status == "RESOLVED")
               & (Problem.resolved_at >= trigger.opened_at - timedelta(seconds=rule.window_s))),
        ).order_by(Problem.opened_at.desc())
    ).scalars().all()

    for cause in candidates:
        if all(_axis_matches(session, axis, trigger, cause) for axis in axes):
            return cause
    return None


def try_correlate(session: Session, problem: Problem) -> Incident | None:
    if problem.incident_id is not None:
        return None  # уже свёрнут — раздел 6: одна проблема не переносится между инцидентами дважды

    is_already_root = session.execute(
        select(Incident.id).where(Incident.root_problem_id == problem.id)
    ).scalar_one_or_none()
    if is_already_root:
        return None  # раздел 6.7 — кандидат в первопричину не подавляется и не переквалифицируется

    rules = session.execute(
        select(CorrelationRule).where(CorrelationRule.status == "active",
                                       CorrelationRule.trigger_symptom == problem.symptom_class)
    ).scalars().all()

    for rule in rules:
        cause = _find_cause(session, rule, problem)
        if cause is None:
            continue

        incident = None
        if cause.incident_id is not None:
            incident = session.get(Incident, cause.incident_id)
        else:
            incident = Incident(root_problem_id=cause.id, priority=cause.priority, opened_at=cause.opened_at)
            session.add(incident)
            session.flush()
            cause.incident_id = incident.id
            session.add(IncidentProblem(incident_id=incident.id, problem_id=cause.id, role="root"))

        session.add(IncidentProblem(incident_id=incident.id, problem_id=problem.id,
                                     role="symptom", rule_id=rule.rule_id))
        problem.incident_id = incident.id
        session.flush()
        return incident

    return None
