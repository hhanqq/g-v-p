"""Метрики дашборда администратора — кейс, раздел 7 «Аналитика»:
нагрузка системы, обработанные события, процент доставленных уведомлений,
наиболее частые инциденты. Каждая функция — чистый SELECT по уже
существующим таблицам конвейера, без побочных эффектов и без своих
таблиц: дашборд ничего не может сломать и не требует отдельной схемы."""
from __future__ import annotations

from datetime import datetime, timedelta

from sqlalchemy import func, select

from packages.models.db import (CmdbObject, CmdbOwnership, Event, Incident, Notification, Problem,
                                 Signal, SignalQueueEntry, SlaBreachNotice)


def queue_depth(session) -> dict[str, int]:
    rows = session.execute(
        select(SignalQueueEntry.status, func.count()).group_by(SignalQueueEntry.status)
    ).all()
    return dict(rows)


def events_processed(session) -> dict:
    total_signals = session.execute(select(func.count(Signal.id))).scalar_one()
    total_events = session.execute(select(func.count(Event.id))).scalar_one()
    parse_failed = session.execute(
        select(func.count(SignalQueueEntry.id)).where(SignalQueueEntry.status == "parse_failed")
    ).scalar_one()
    return {
        "signals": total_signals,
        "events": total_events,
        "parse_failed": parse_failed,
        "parse_success_rate": round(100 * (total_signals - parse_failed) / total_signals, 1) if total_signals else None,
    }


def delivery_rate(session) -> dict:
    total = session.execute(select(func.count(Notification.id))).scalar_one()
    sent = session.execute(select(func.count(Notification.id)).where(Notification.status == "sent")).scalar_one()
    failed = session.execute(select(func.count(Notification.id)).where(Notification.status == "failed")).scalar_one()
    supplements_sent = session.execute(
        select(func.count(Notification.id))
        .where(Notification.type == "SUPPLEMENT", Notification.status == "sent")
    ).scalar_one()
    return {
        "total": total, "sent": sent, "failed": failed, "supplements_sent": supplements_sent,
        "delivered_pct": round(100 * sent / total, 1) if total else None,
    }


def top_symptom_classes(session, limit: int = 5) -> list[tuple[str, int]]:
    rows = session.execute(
        select(Problem.symptom_class, func.count())
        .group_by(Problem.symptom_class)
        .order_by(func.count().desc())
        .limit(limit)
    ).all()
    return [(cls, count) for cls, count in rows]


def priority_distribution(session) -> dict[str, int]:
    rows = session.execute(
        select(Problem.priority, func.count())
        .where(Problem.priority.is_not(None))
        .group_by(Problem.priority)
    ).all()
    return dict(rows)


def resolution_coverage(session) -> float | None:
    """Раздел 4.3 — доля событий, для которых резолвер нашёл object_id
    (не ушли в карантин)."""
    total = session.execute(select(func.count(Event.id))).scalar_one()
    resolved = session.execute(select(func.count(Event.id)).where(Event.resolved.is_(True))).scalar_one()
    return round(100 * resolved / total, 1) if total else None


def average_mttr_seconds(session) -> float | None:
    """Раздел 6.6 (не реализовано — измерение рассинхронизации часов
    источников) — occurred_at событий не проверяется на упорядоченность
    при свёртке в Problem (state_manager.apply_event). На вживую
    смешанных данных демо-стенда (разные "часовые пояса" тестовых
    прогонов) это может дать resolved_at раньше opened_at; такие строки
    исключаются здесь, а не там — дашборд не должен показывать
    отрицательный MTTR, даже если корень проблемы шире одной метрики."""
    rows = session.execute(
        select(Problem.opened_at, Problem.resolved_at)
        .where(Problem.status == "RESOLVED", Problem.resolved_at.is_not(None),
               Problem.resolved_at >= Problem.opened_at)
    ).all()
    if not rows:
        return None
    total = sum((resolved_at - opened_at).total_seconds() for opened_at, resolved_at in rows)
    return round(total / len(rows), 1)


def average_ingest_latency_seconds(session, sample: int = 200) -> float | None:
    """Раздел 15 — "оценена производительность": среднее время от приёма
    сигнала шлюзом (Signal.received_at) до готового Event (стадии 0-1),
    по последним N событиям."""
    rows = session.execute(
        select(Signal.received_at, Event.ingest_ts)
        .join(Event, Event.signal_id == Signal.id)
        .order_by(Event.id.desc())
        .limit(sample)
    ).all()
    if not rows:
        return None
    total = sum((ingest_ts - received_at).total_seconds() for received_at, ingest_ts in rows)
    return round(total / len(rows), 3)


def incidents_summary(session) -> dict:
    open_count = session.execute(
        select(func.count()).select_from(Problem).where(Problem.status.in_(("OPEN", "FLAPPING")))
    ).scalar_one()
    total_incidents = session.execute(select(func.count(Incident.id))).scalar_one()
    return {"open_problems": open_count, "incidents": total_incidents}


def subsidiary_incident_stats(session, limit: int = 5) -> list[tuple[str, int, int]]:
    """Раздел 5/8 — сырьё для ИИ-подсказки "на что подписаться" (раздел
    8, packages/ai/suggest_subscription.py): частота и критичность
    прошлых Problem по владеющему филиалу (CmdbOwnership — раздел 4.2,
    объект/сервис может принадлежать сразу нескольким филиалам)."""
    subsidiaries = session.execute(select(CmdbOwnership.subsidiary).distinct()).scalars().all()
    stats = []
    for subsidiary in subsidiaries:
        owned_objects = select(CmdbOwnership.object_id).where(CmdbOwnership.subsidiary == subsidiary,
                                                                CmdbOwnership.object_id.is_not(None))
        total = session.execute(
            select(func.count()).select_from(Problem).where(Problem.object_id.in_(owned_objects))
        ).scalar_one()
        if total == 0:
            continue
        critical = session.execute(
            select(func.count()).select_from(Problem)
            .where(Problem.object_id.in_(owned_objects), Problem.priority.in_(("P0", "P1")))
        ).scalar_one()
        stats.append((subsidiary, total, critical))
    stats.sort(key=lambda row: row[1], reverse=True)
    return stats[:limit]


def ai_scenario_counts(session) -> dict:
    """Раздел «Использование ИИ» — сколько раз каждый сценарий реально
    сработал на живых данных (не оценка, а COUNT по факту): дедупликация
    и гипотеза первопричины оставляют след прямо на Problem, саммари/
    рекомендации — в Notification.type=SUPPLEMENT."""
    duplicates = session.execute(
        select(func.count()).select_from(Problem).where(Problem.duplicate_of_problem_id.is_not(None))
    ).scalar_one()
    hypotheses = session.execute(
        select(func.count()).select_from(Problem).where(Problem.ai_root_cause_hypothesis.is_not(None))
    ).scalar_one()
    supplements = session.execute(
        select(func.count(Notification.id)).where(Notification.type == "SUPPLEMENT",
                                                    Notification.status == "sent")
    ).scalar_one()
    return {
        "duplicates_detected": duplicates,
        "root_cause_hypotheses": hypotheses,
        "ai_supplements_sent": supplements,
    }


def ai_recent_examples(session) -> dict:
    """Реальные последние срабатывания каждого ИИ-сценария — раздел
    «Использование ИИ» demo-страницы: показываем то, что модель РЕАЛЬНО
    сгенерировала на живых данных, читая свою же БД (packages/ai/ пишет
    туда через worker.py/delivery_trueconf), а не переигрываем заново."""

    summary_row = session.execute(
        select(Notification, Problem)
        .join(Problem, Problem.id == Notification.problem_id)
        .where(Notification.ai_summary.is_not(None))
        .order_by(Notification.id.desc()).limit(1)
    ).first()

    recommendation_row = session.execute(
        select(Notification, Problem)
        .join(Problem, Problem.id == Notification.problem_id)
        .where(Notification.ai_recommendation.is_not(None))
        .order_by(Notification.id.desc()).limit(1)
    ).first()

    classify_row = session.execute(
        select(Event).where(Event.symptom_class_source == "ai")
        .order_by(Event.id.desc()).limit(1)
    ).scalar_one_or_none()

    dup_row = session.execute(
        select(Problem).where(Problem.duplicate_of_problem_id.is_not(None))
        .order_by(Problem.id.desc()).limit(1)
    ).scalar_one_or_none()
    dup_original = session.get(Problem, dup_row.duplicate_of_problem_id) if dup_row else None

    hypothesis_row = session.execute(
        select(Problem).where(Problem.ai_root_cause_hypothesis.is_not(None))
        .order_by(Problem.id.desc()).limit(1)
    ).scalar_one_or_none()

    return {
        "summary": {
            "text": summary_row[0].ai_summary, "problem_id": summary_row[1].id,
            "object_id": summary_row[1].object_id,
        } if summary_row else None,
        "recommendation": {
            "text": recommendation_row[0].ai_recommendation, "problem_id": recommendation_row[1].id,
            "symptom_class": recommendation_row[1].symptom_class,
        } if recommendation_row else None,
        "classification": {
            "title": classify_row.title, "symptom_class": classify_row.symptom_class,
            "event_id": classify_row.id,
        } if classify_row else None,
        "duplicate": {
            "problem_id": dup_row.id, "object_id": dup_row.object_id, "symptom_class": dup_row.symptom_class,
            "original_id": dup_original.id if dup_original else None,
            "original_symptom_class": dup_original.symptom_class if dup_original else None,
        } if dup_row else None,
        "root_cause_hypothesis": {
            "text": hypothesis_row.ai_root_cause_hypothesis, "problem_id": hypothesis_row.id,
            "site": hypothesis_row.site,
        } if hypothesis_row else None,
    }


def alerts_over_time(session, days: int = 14) -> list[tuple[str, int]]:
    """Раздел «Аналитика», Этап 4 — количество событий (`Event.occurred_at`)
    по дням за последние `days` дней. Дни без событий дозаполняются
    нулями в Python, а не через generate_series в SQL — переносимость
    между SQLite (тесты) и Postgres (прод) проще, а график на фронтенде
    не должен «прыгать» из-за пропущенных дат."""
    since = datetime.utcnow() - timedelta(days=days - 1)
    rows = session.execute(
        select(func.date(Event.occurred_at), func.count())
        .where(Event.occurred_at >= since)
        .group_by(func.date(Event.occurred_at))
    ).all()
    counts = {str(day): count for day, count in rows}
    today = datetime.utcnow().date()
    return [
        ((today - timedelta(days=i)).isoformat(), counts.get((today - timedelta(days=i)).isoformat(), 0))
        for i in range(days - 1, -1, -1)
    ]


def top_problem_objects(session, limit: int = 8) -> list[dict]:
    """Раздел «Аналитика», Этап 4 — самые проблемные объекты по
    количеству Problem, с человекочитаемым именем/площадкой из
    CmdbObject (объект без карточки CmdbObject — раздел 4.2, не ошибка,
    просто выводится под собственным object_id)."""
    rows = session.execute(
        select(Problem.object_id, func.count())
        .where(Problem.object_id.is_not(None))
        .group_by(Problem.object_id)
        .order_by(func.count().desc())
        .limit(limit)
    ).all()
    result = []
    for object_id, count in rows:
        obj = session.get(CmdbObject, object_id)
        result.append({
            "object_id": object_id, "count": count,
            "name": obj.name if obj else object_id,
            "site": obj.site if obj else None,
            "equipment_type": obj.equipment_type if obj else None,
        })
    return result


def sla_breach_stats(session) -> dict:
    """Раздел «SLA»/«Аналитика», Этап 4 — сколько раз реально сработало
    напоминание о нарушении (SlaBreachNotice — одно на жизненный цикл
    проблемы, см. run_sla_breaches), с разбивкой по приоритету
    нарушенной проблемы."""
    total = session.execute(select(func.count(SlaBreachNotice.id))).scalar_one()
    rows = session.execute(
        select(Problem.priority, func.count())
        .select_from(SlaBreachNotice)
        .join(Problem, Problem.id == SlaBreachNotice.problem_id)
        .group_by(Problem.priority)
    ).all()
    return {"total": total, "by_priority": {priority or "?": count for priority, count in rows}}


def analytics_summary(session) -> dict:
    """Всё для страницы «Аналитика» (Этап 4) — историческая часть,
    отдельно от dashboard_snapshot (Главная показывает состояние ПРЯМО
    СЕЙЧАС, Аналитика — историю за период)."""
    return {
        "alerts_over_time": alerts_over_time(session),
        "top_problem_objects": top_problem_objects(session),
        "top_symptoms": top_symptom_classes(session),
        "priority_distribution": priority_distribution(session),
        "sla_breach": sla_breach_stats(session),
        "avg_mttr_seconds": average_mttr_seconds(session),
        "resolution_coverage_pct": resolution_coverage(session),
        "incidents": incidents_summary(session),
    }


def dashboard_snapshot(session) -> dict:
    """Всё сразу — один вызов для страницы/API дашборда."""
    return {
        "queue": queue_depth(session),
        "events": events_processed(session),
        "delivery": delivery_rate(session),
        "top_symptoms": top_symptom_classes(session),
        "priority_distribution": priority_distribution(session),
        "resolution_coverage_pct": resolution_coverage(session),
        "avg_mttr_seconds": average_mttr_seconds(session),
        "avg_ingest_latency_seconds": average_ingest_latency_seconds(session),
        "incidents": incidents_summary(session),
        "ai_scenarios": ai_scenario_counts(session),
    }
