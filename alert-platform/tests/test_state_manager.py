"""Тесты менеджера состояний — раздел 5, стадия 3. Критерий M3 (раздел 17.2):
проблема открывается и закрывается, MTTR считается. SQLite in-memory,
без Postgres-специфики (в отличие от pipeline-worker с очередью)."""
from datetime import datetime, timedelta

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.models.db import Base, Problem
from services.pipeline.state_manager import apply_event, close_stale_problems, mttr_seconds

# Наивные datetime намеренно: SQLite (в отличие от Postgres, на котором
# реально работает воркер) не round-trip'ит tzinfo из DateTime(timezone=True)
# после flush, что ломало бы сравнение дат в этих тестах — сама логика
# менеджера состояний часовые зоны не интерпретирует, только вычитает.
DK = "brd-noyabrsk|brd-noyabrsk/app-01|C:|disk_space"
T0 = datetime(2026, 8, 6, 3, 0, 0)


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        yield s


def _fire(session, dedup_key=DK, at=T0, symptom_class="disk_space", **kw):
    return apply_event(session, dedup_key=dedup_key, state="firing", occurred_at=at,
                        object_id="brd-noyabrsk/app-01", component="C:",
                        symptom_class=symptom_class, site="brd-noyabrsk", **kw)


def _resolve(session, dedup_key=DK, at=T0, symptom_class="disk_space", **kw):
    return apply_event(session, dedup_key=dedup_key, state="resolved", occurred_at=at,
                        object_id="brd-noyabrsk/app-01", component="C:",
                        symptom_class=symptom_class, site="brd-noyabrsk", **kw)


def test_first_firing_opens_problem(session):
    problem = _fire(session)
    assert problem.status == "OPEN"
    assert problem.repeat_count == 1
    assert problem.opened_at == T0


def test_repeated_firing_folds_into_same_problem(session):
    p1 = _fire(session, at=T0)
    p2 = _fire(session, at=T0 + timedelta(seconds=30))
    assert p1.id == p2.id
    assert p2.repeat_count == 2
    assert session.query(Problem).count() == 1


def test_resolve_closes_problem_and_mttr_is_computed(session):
    _fire(session, at=T0)
    resolved = _resolve(session, at=T0 + timedelta(minutes=26))
    assert resolved.status == "RESOLVED"
    assert mttr_seconds(resolved) == 26 * 60


def test_resolve_without_active_problem_is_noop(session):
    result = _resolve(session, at=T0)
    assert result is None
    assert session.query(Problem).count() == 0


def test_event_without_dedup_key_is_ignored(session):
    result = apply_event(session, dedup_key=None, state="firing", occurred_at=T0,
                          object_id=None, component=None, symptom_class="host_unreachable",
                          site="brd-noyabrsk")
    assert result is None


def test_reopen_within_flap_window_reuses_same_problem_row(session):
    p1 = _fire(session, at=T0)
    r1 = _resolve(session, at=T0 + timedelta(seconds=20))
    p2 = _fire(session, at=T0 + timedelta(seconds=40), flap_window_s=180)
    assert p2.id == p1.id == r1.id
    assert p2.status == "OPEN"
    assert p2.resolved_at is None
    assert p2.toggle_count == 1
    assert session.query(Problem).count() == 1


def test_reopen_after_flap_window_creates_new_episode(session):
    p1 = _fire(session, at=T0)
    _resolve(session, at=T0 + timedelta(seconds=20))
    p2 = _fire(session, at=T0 + timedelta(seconds=300), flap_window_s=180)
    assert p2.id != p1.id
    assert session.query(Problem).count() == 2


def test_enough_toggles_flip_problem_to_flapping(session):
    t = T0
    problem = _fire(session, at=t)
    for _ in range(6):
        t += timedelta(seconds=10)
        _resolve(session, at=t)
        t += timedelta(seconds=10)
        problem = _fire(session, at=t, flap_threshold=6, flap_window_s=180)
    assert problem.status == "FLAPPING"
    assert problem.toggle_count >= 6
    assert session.query(Problem).count() == 1  # ни одного лишнего эпизода


def test_close_stale_problems_ttl_fallback(session):
    _fire(session, dedup_key="a", at=T0, symptom_class="host_unreachable")
    _fire(session, dedup_key="b", at=T0, symptom_class="disk_space")

    now = T0 + timedelta(seconds=700)  # > TTL host_unreachable (600s), < TTL disk_space (1800s)
    closed = close_stale_problems(session, now)

    assert closed == 1
    a = session.query(Problem).filter_by(dedup_key="a").one()
    b = session.query(Problem).filter_by(dedup_key="b").one()
    assert a.status == "RESOLVED"
    assert a.closed_by_reconciliation is True
    assert b.status == "OPEN"
