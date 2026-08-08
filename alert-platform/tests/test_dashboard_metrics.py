"""Тесты метрик дашборда — раздел 7 кейса «Аналитика». Каждая функция —
чистый SELECT; проверяем и типичный случай, и пустую БД (дашборд не
должен падать на свежем стенде без данных)."""
from datetime import datetime, timedelta

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.models.db import Base, CmdbOwnership, Event, Notification, Problem, Signal, SignalQueueEntry
from services.api import metrics

T0 = datetime(2026, 8, 6, 10, 0, 0)


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        yield s


def test_empty_db_returns_safe_defaults(session):
    snap = metrics.dashboard_snapshot(session)
    assert snap["events"]["signals"] == 0
    assert snap["events"]["parse_success_rate"] is None
    assert snap["delivery"]["delivered_pct"] is None
    assert snap["avg_mttr_seconds"] is None
    assert snap["avg_ingest_latency_seconds"] is None
    assert snap["top_symptoms"] == []


def test_queue_depth_groups_by_status(session):
    for i, status in enumerate(("pending", "pending", "done", "parse_failed")):
        sig = Signal(source_system="zabbix", source_instance="x", received_at=T0,
                      raw_body="x", hash=f"{status}-{i}")
        session.add(sig)
        session.flush()
        session.add(SignalQueueEntry(signal_id=sig.id, status=status, enqueued_at=T0))
    session.commit()
    depth = metrics.queue_depth(session)
    assert depth["pending"] == 2
    assert depth["done"] == 1
    assert depth["parse_failed"] == 1


def test_delivery_rate_and_supplement_count(session):
    p = Problem(dedup_key="k", status="OPEN", symptom_class="node_down", opened_at=T0,
                last_seen_at=T0, repeat_count=1, toggle_count=0)
    session.add(p)
    session.flush()
    session.add_all([
        Notification(problem_id=p.id, type="NEW", chat_id="c1", status="sent", created_at=T0),
        Notification(problem_id=p.id, type="NEW", chat_id="c2", status="failed", created_at=T0),
        Notification(problem_id=p.id, type="SUPPLEMENT", chat_id="c1", status="sent", created_at=T0),
    ])
    session.commit()
    rate = metrics.delivery_rate(session)
    assert rate["total"] == 3
    assert rate["sent"] == 2
    assert rate["failed"] == 1
    assert rate["supplements_sent"] == 1
    assert rate["delivered_pct"] == round(200 / 3, 1)


def test_mttr_only_counts_resolved_with_timestamp(session):
    session.add(Problem(dedup_key="a", status="RESOLVED", symptom_class="node_down",
                         opened_at=T0, resolved_at=T0 + timedelta(minutes=10),
                         last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.add(Problem(dedup_key="b", status="OPEN", symptom_class="node_down",
                         opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.commit()
    assert metrics.average_mttr_seconds(session) == 600.0


def test_mttr_excludes_out_of_order_timestamps(session):
    """Раздел 6.6 — occurred_at не проверяется на упорядоченность при
    свёртке; живой прогон 2026-08-07 поймал resolved_at раньше opened_at
    при смешении данных из двух разных прогонов с разными "часами".
    Дашборд не должен показывать отрицательный MTTR."""
    session.add(Problem(dedup_key="a", status="RESOLVED", symptom_class="node_down",
                         opened_at=T0, resolved_at=T0 + timedelta(minutes=10),
                         last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.add(Problem(dedup_key="b", status="RESOLVED", symptom_class="node_down",
                         opened_at=T0, resolved_at=T0 - timedelta(days=1),
                         last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.commit()
    assert metrics.average_mttr_seconds(session) == 600.0  # только корректная строка "a"


def test_resolution_coverage(session):
    sig = Signal(source_system="zabbix", source_instance="x", received_at=T0, raw_body="x", hash="h1")
    session.add(sig)
    session.flush()
    session.add_all([
        Event(signal_id=sig.id, state="firing", occurred_at=T0, ingest_ts=T0,
              symptom_class="node_down", title="t", resolved=True),
        Event(signal_id=sig.id, state="firing", occurred_at=T0, ingest_ts=T0,
              symptom_class="node_down", title="t", resolved=False),
    ])
    session.commit()
    assert metrics.resolution_coverage(session) == 50.0


def test_top_symptom_classes_orders_by_count(session):
    for cls, n in (("node_down", 3), ("disk_space", 1)):
        for i in range(n):
            session.add(Problem(dedup_key=f"{cls}-{i}", status="OPEN", symptom_class=cls,
                                 opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.commit()
    top = metrics.top_symptom_classes(session)
    assert top[0] == ("node_down", 3)


def test_subsidiary_incident_stats_orders_by_total_and_counts_critical(session):
    session.add_all([
        CmdbOwnership(object_id="obj-a", subsidiary="gpn-noyabrsk"),
        CmdbOwnership(object_id="obj-b", subsidiary="gpn-khantos"),
    ])
    for i in range(3):
        session.add(Problem(dedup_key=f"a-{i}", status="OPEN", symptom_class="node_down",
                             object_id="obj-a", priority="P0" if i == 0 else "P3",
                             opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.add(Problem(dedup_key="b-0", status="OPEN", symptom_class="node_down",
                         object_id="obj-b", priority="P3",
                         opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.commit()
    stats = metrics.subsidiary_incident_stats(session)
    assert stats[0] == ("gpn-noyabrsk", 3, 1)
    assert stats[1] == ("gpn-khantos", 1, 0)


def test_subsidiary_incident_stats_empty_when_no_ownership(session):
    assert metrics.subsidiary_incident_stats(session) == []


def test_ai_scenario_counts(session):
    session.add(Problem(dedup_key="a", status="OPEN", symptom_class="node_down",
                         opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0))
    session.add(Problem(dedup_key="b", status="OPEN", symptom_class="host_unreachable",
                         opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0,
                         duplicate_of_problem_id=1))
    session.add(Problem(dedup_key="c", status="OPEN", symptom_class="power_lost",
                         opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0,
                         ai_root_cause_hypothesis="возможно, из-за питания"))
    session.commit()
    counts = metrics.ai_scenario_counts(session)
    assert counts["duplicates_detected"] == 1
    assert counts["root_cause_hypotheses"] == 1


def test_ai_recent_examples_empty_db_returns_all_none(session):
    examples = metrics.ai_recent_examples(session)
    assert examples["summary"] is None
    assert examples["recommendation"] is None
    assert examples["classification"] is None
    assert examples["duplicate"] is None
    assert examples["root_cause_hypothesis"] is None


def test_ai_recent_examples_picks_latest_and_joins_original(session):
    p1 = Problem(dedup_key="a", status="OPEN", symptom_class="node_down", object_id="sw-01",
                 opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0)
    session.add(p1)
    session.flush()
    p2 = Problem(dedup_key="b", status="OPEN", symptom_class="host_unreachable", object_id="sw-01",
                 opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0,
                 duplicate_of_problem_id=p1.id, ai_root_cause_hypothesis="скорее всего X")
    session.add(p2)
    session.flush()
    session.add(Notification(problem_id=p1.id, type="SUPPLEMENT", chat_id="c1", status="sent",
                              created_at=T0, ai_summary="сводка каскада", ai_recommendation="проверьте питание"))
    sig = Signal(source_system="zabbix", source_instance="x", received_at=T0, raw_body="x", hash="h1")
    session.add(sig)
    session.flush()
    session.add(Event(signal_id=sig.id, state="firing", occurred_at=T0, ingest_ts=T0,
                       symptom_class="disk_space", title="странный текст", symptom_class_source="ai"))
    session.commit()

    examples = metrics.ai_recent_examples(session)
    assert examples["summary"]["text"] == "сводка каскада"
    assert examples["recommendation"]["text"] == "проверьте питание"
    assert examples["classification"]["symptom_class"] == "disk_space"
    assert examples["duplicate"]["original_id"] == p1.id
    assert examples["root_cause_hypothesis"]["text"] == "скорее всего X"
