"""Pipeline-worker — стадии 1-3 (раздел 5). Забирает сигналы из очереди на
PostgreSQL (раздел 15.1, демо-стенд), разбирает коннектором, резолвит
объект и сворачивает в проблему.

Очередь — двухфазная, а не один SELECT...FOR UPDATE + общий commit в конце
батча:

1. Claim: одной короткой транзакцией помечает пачку 'pending' → 'processing'
   (FOR UPDATE SKIP LOCKED — несколько экземпляров воркера не конкурируют
   за одну строку, раздел 15.3) и сразу коммитит.
2. Process: каждая заявка обрабатывается и коммитится в СВОЕЙ транзакции.

Так ошибка на одной записи не откатывает и не валит весь пакет и не роняет
процесс — предыдущая версия коммитила весь батч одной транзакцией в конце
process_batch, из-за чего необработанное исключение на записи №5 из 20
теряло прогресс по записям 1-4 и (что хуже) убивало сам процесс воркера.
Зависшие 'processing'-строки (воркер упал между фазами) сама разгребает
_requeue_stuck_processing по TTL.
"""
from __future__ import annotations

import json
import os
import signal
import time
import traceback
from datetime import datetime, timedelta
from pathlib import Path

from sqlalchemy import select, update

from packages.ai.classify import classify_symptom_sync
from packages.ai.dedup import is_duplicate_sync
from packages.ai.root_cause_hypothesis import suggest_root_cause_sync
from packages.common.db import engine, get_session
from packages.common.deprovision import deactivate_departed_subscribers
from packages.common.sources import load_passports, seed_from_yaml_if_empty
from packages.models.canonical import compute_dedup_key
from packages.models.db import (Base, CmdbService, CmdbServiceObject, Event, Problem, Signal,
                                 SignalQueueEntry)
from packages.rules.priority import business_impact_from_criticality, compute_priority
from services.pipeline.correlator import try_correlate
from services.pipeline.parser import load_connectors, parse
from services.pipeline.resolver import resolve
from services.pipeline.state_manager import apply_event, close_stale_problems

DEFAULT_TECHNICAL_SEVERITY = 2  # раздел 18.3 п.5: severity_raw не всегда есть (напр. Reset-сообщения)
DEDUP_WINDOW_S = 300  # раздел 18.4: duplicate_cross_system шлёт пару за 1-8с, окно с запасом
ROOT_CAUSE_WINDOW_S = 300  # раздел 18.4: site_outage_ambiguous — та же пара источников почти синхронно


def _business_impact(session, object_id: str | None) -> int:
    if not object_id:
        return 0
    criticalities = session.execute(
        select(CmdbService.criticality)
        .join(CmdbServiceObject, CmdbServiceObject.service_id == CmdbService.id)
        .where(CmdbServiceObject.object_id == object_id)
    ).scalars().all()
    if not criticalities:
        return 0
    return max(business_impact_from_criticality(c) for c in criticalities)

CONNECTORS_DIR = Path(__file__).resolve().parents[2] / "connectors"
BATCH_SIZE = 20
POLL_INTERVAL_S = float(os.environ.get("PIPELINE_POLL_INTERVAL_S", "1"))
STUCK_PROCESSING_TIMEOUT_S = 120

_stop = False


def _handle_stop(*_args):
    global _stop
    _stop = True


def claim_batch() -> list[int]:
    with get_session() as session:
        claimed_ids = session.execute(
            update(SignalQueueEntry)
            .where(SignalQueueEntry.id.in_(
                select(SignalQueueEntry.id)
                .where(SignalQueueEntry.status == "pending")
                .order_by(SignalQueueEntry.id)
                .limit(BATCH_SIZE)
                .with_for_update(skip_locked=True)
            ))
            .values(status="processing")
            .returning(SignalQueueEntry.id)
        ).scalars().all()
        session.commit()
        return list(claimed_ids)


def _first_raw_body(session, problem_id: int) -> str | None:
    row = session.execute(
        select(Signal.raw_body)
        .join(Event, Event.signal_id == Signal.id)
        .where(Event.problem_id == problem_id)
        .order_by(Event.id.asc())
        .limit(1)
    ).scalar_one_or_none()
    return row


def process_one(entry_id: int, connectors, passports) -> None:
    with get_session() as session:
        entry = session.get(SignalQueueEntry, entry_id)
        signal_row: Signal = session.get(Signal, entry.signal_id)
        entry.attempts += 1
        entry.processed_at = datetime.utcnow()

        connector = connectors.get(signal_row.source_system)
        if connector is None:
            entry.status = "parse_failed"
            entry.error = f"неизвестная система источника: {signal_row.source_system}"
            session.commit()
            return

        passport = passports.get(signal_row.source_instance)
        site = passport["site"] if passport else None

        result = parse(connector, signal_row.raw_body, signal_row.source_instance,
                        received_at=signal_row.received_at, site=site)
        if not result.success:
            entry.status = "parse_failed"
            entry.error = result.error
            session.commit()
            return

        ev = result.event
        symptom_class_source = "rule"
        if ev.symptom_class == "unknown":
            # ИИ-сценарий «семантическая нормализация» (раздел 5,
            # packages/ai/classify.py) — регэксп не распознал формулировку,
            # пробуем распознать по смыслу. Короткий таймаут и молчаливый
            # None-фолбэк: событие остаётся "unknown", как и до этой
            # возможности, конвейер не блокируется и не падает (раздел И5).
            ai_class = classify_symptom_sync(ev.title or signal_row.raw_body)
            if ai_class:
                print(f"pipeline-worker: ИИ переклассифицировал unknown -> {ai_class} "
                      f"(signal_id={signal_row.id}, site={ev.entity.site})")
                ev.symptom_class = ai_class
                symptom_class_source = "ai"

        resolution = resolve(session, ev.entity.site, ev.entity.object.name, ev.entity.object.ip)
        is_resolved = resolution.object_id is not None
        # Резолвер знает канонический object_id — провизорный dedup_key
        # парсера (по сырому имени) пересчитывается на точный (раздел 4.3).
        # В карантине ключ не строится: карантинные события не должны
        # схлопываться коррелятором (раздел 4.3 п.6).
        final_dedup_key = (
            compute_dedup_key(ev.entity.site, resolution.object_id, ev.entity.component, ev.symptom_class)
            if is_resolved else None
        )
        # Стадия 3: свёртка в проблему — только для резолвленных событий.
        problem = apply_event(
            session, dedup_key=final_dedup_key, state=ev.state, occurred_at=ev.occurred_at,
            object_id=resolution.object_id, component=ev.entity.component,
            symptom_class=ev.symptom_class, site=ev.entity.site,
        ) if is_resolved else None

        if problem is not None:
            # Стадия 5 (раздел 7): приоритет пересчитывается на каждое
            # обновление проблемы — дёшево, а repeat_count и время могли
            # измениться. Стадия 4 (раздел 6): пробуем корреляцию сразу же;
            # окно ожидания по приоритету (раздел 6.6) не применяется — оно
            # регулирует ТАЙМИНГ ДОСТАВКИ уведомления, а доставки пока нет.
            technical_severity = connector.severity_map.get(ev.severity_raw, DEFAULT_TECHNICAL_SEVERITY)
            business_impact = _business_impact(session, resolution.object_id)
            priority, breakdown = compute_priority(
                technical_severity=technical_severity, business_impact=business_impact,
                repeat_count=problem.repeat_count, occurred_at=ev.occurred_at,
            )
            problem.priority = priority
            problem.priority_breakdown = json.dumps(breakdown, ensure_ascii=False)
            try_correlate(session, problem)

            if problem.repeat_count == 1:
                # ИИ-сценарий «дедупликация между источниками» (раздел 4.1,
                # packages/ai/dedup.py) — только на свежесозданной проблеме,
                # не на каждом повторе одного и того же dedup_key. Кандидат:
                # тот же объект, ДРУГОЙ symptom_class (иначе поймал бы точный
                # dedup_key), открыт почти одновременно, сам ещё не дубль.
                candidate = session.execute(
                    select(Problem)
                    .where(Problem.object_id == problem.object_id,
                           Problem.id != problem.id,
                           Problem.symptom_class != problem.symptom_class,
                           Problem.status.in_(("OPEN", "FLAPPING")),
                           Problem.duplicate_of_problem_id.is_(None),
                           Problem.opened_at >= problem.opened_at - timedelta(seconds=DEDUP_WINDOW_S))
                    .order_by(Problem.opened_at.desc())
                    .limit(1)
                ).scalars().first() if problem.object_id else None
                if candidate is not None:
                    candidate_body = _first_raw_body(session, candidate.id)
                    verdict = is_duplicate_sync(signal_row.raw_body, candidate_body or "")
                    if verdict:
                        problem.duplicate_of_problem_id = candidate.id
                        print(f"pipeline-worker: ИИ определил дубль problem={problem.id} "
                              f"-> {candidate.id} (object={problem.object_id})")

                # ИИ-сценарий «вероятная первопричина при неоднозначной
                # корреляции» (раздел 6.1, packages/ai/root_cause_hypothesis.py)
                # — только если правило корреляции НЕ связало проблему
                # (incident_id всё ещё пуст) и это не дубль другого события.
                # Приоритет НЕ ограничен P0/P1: раздел 18.4 site_outage_ambiguous
                # даёт P0-P2 в зависимости от критичности сервиса в CMDB (не
                # фиксированное значение) — важна сама неоднозначность
                # ситуации, не конкретный уровень; P3 (фон) исключён, чтобы
                # не звать ИИ на случайные совпадения по времени в шуме.
                if (problem.incident_id is None and problem.duplicate_of_problem_id is None
                        and problem.priority in ("P0", "P1", "P2") and problem.site):
                    sibling = session.execute(
                        select(Problem)
                        .where(Problem.site == problem.site,
                               Problem.id != problem.id,
                               Problem.object_id != problem.object_id,
                               Problem.symptom_class != problem.symptom_class,
                               Problem.incident_id.is_(None),
                               Problem.duplicate_of_problem_id.is_(None),
                               Problem.status.in_(("OPEN", "FLAPPING")),
                               Problem.priority.in_(("P0", "P1", "P2")),
                               Problem.opened_at >= problem.opened_at - timedelta(seconds=ROOT_CAUSE_WINDOW_S))
                        .order_by(Problem.opened_at.desc())
                        .limit(1)
                    ).scalars().first()
                    if sibling is not None:
                        sibling_body = _first_raw_body(session, sibling.id)
                        hypothesis = suggest_root_cause_sync(
                            site=problem.site, candidate_a_text=sibling_body or "",
                            candidate_b_text=signal_row.raw_body,
                        )
                        if hypothesis:
                            problem.ai_root_cause_hypothesis = hypothesis
                            print(f"pipeline-worker: ИИ-гипотеза первопричины для problem={problem.id} "
                                  f"(площадка {problem.site}, кандидат-сосед={sibling.id})")

        session.add(Event(
            signal_id=signal_row.id,
            dedup_key=final_dedup_key,
            state=ev.state,
            occurred_at=ev.occurred_at,
            ingest_ts=ev.ingest_ts,
            symptom_class=ev.symptom_class,
            severity_raw=ev.severity_raw,
            title=ev.title,
            site=ev.entity.site,
            object_name_raw=ev.entity.object.name,
            ip_raw=ev.entity.object.ip,
            component=ev.entity.component,
            parser_version=ev.parser_version,
            resolved=is_resolved,
            object_id=resolution.object_id,
            resolution_method=resolution.method,
            resolution_confidence=resolution.confidence,
            problem_id=problem.id if problem else None,
            symptom_class_source=symptom_class_source,
        ))
        entry.status = "done"
        session.commit()


def _mark_failed(entry_id: int, error: str) -> None:
    with get_session() as session:
        entry = session.get(SignalQueueEntry, entry_id)
        entry.status = "parse_failed"
        entry.error = error[:2000]
        session.commit()


def requeue_stuck_processing() -> int:
    """Крэш-восстановление: если воркер умер между claim и commit,
    строка осталась бы в 'processing' навечно — возвращаем её в 'pending'."""
    cutoff = datetime.utcnow() - timedelta(seconds=STUCK_PROCESSING_TIMEOUT_S)
    with get_session() as session:
        result = session.execute(
            update(SignalQueueEntry)
            .where(SignalQueueEntry.status == "processing", SignalQueueEntry.processed_at < cutoff)
            .values(status="pending")
        )
        session.commit()
        return result.rowcount


def process_batch(connectors, passports) -> int:
    claimed_ids = claim_batch()
    for entry_id in claimed_ids:
        try:
            process_one(entry_id, connectors, passports)
        except Exception:  # noqa: BLE001 — одна плохая запись не должна валить воркер (раздел И5)
            _mark_failed(entry_id, traceback.format_exc())
    return len(claimed_ids)


TTL_SWEEP_EVERY_S = float(os.environ.get("TTL_SWEEP_EVERY_S", "30"))


def main():
    signal.signal(signal.SIGTERM, _handle_stop)
    signal.signal(signal.SIGINT, _handle_stop)

    Base.metadata.create_all(bind=engine)
    connectors = load_connectors(CONNECTORS_DIR)
    with get_session() as session:
        seed_from_yaml_if_empty(session, CONNECTORS_DIR / "sources.yaml")
        passports = load_passports(session)
    print(f"pipeline-worker: загружено коннекторов={len(connectors)}, "
          f"паспортов источников={len(passports)} (из БД, /sources редактируется без редеплоя)")

    last_sweep = 0.0
    while not _stop:
        n = process_batch(connectors, passports)

        now_monotonic = time.monotonic()
        if now_monotonic - last_sweep > TTL_SWEEP_EVERY_S:
            with get_session() as session:
                closed = close_stale_problems(session, datetime.utcnow())
                session.commit()
                # Перечитываем паспорта источников на той же периодичности,
                # что и TTL-развёртку: администратор мог зарегистрировать
                # новый инстанс через /sources, не перезапуская воркер.
                new_passports = load_passports(session)
                # Раздел «Безопасность» — автоотключение подписок при
                # увольнении: сверяем Subscriber с LDAP-каталогом на той
                # же периодичности (packages/common/deprovision.py).
                deprovisioned = deactivate_departed_subscribers(session)

            if closed:
                print(f"pipeline-worker: TTL закрыл {closed} проблем без сообщения о восстановлении")
            if new_passports != passports:
                print(f"pipeline-worker: паспорта источников обновлены "
                      f"({len(passports)} -> {len(new_passports)})")
                passports = new_passports
            if deprovisioned:
                print(f"pipeline-worker: деактивировано подписчиков (уволены/удалены из LDAP): "
                      f"{deprovisioned}")
            requeued = requeue_stuck_processing()
            if requeued:
                print(f"pipeline-worker: восстановлено {requeued} зависших 'processing'-записей")
            last_sweep = now_monotonic

        if n == 0:
            time.sleep(POLL_INTERVAL_S)


if __name__ == "__main__":
    main()
