"""Тесты коррелятора — раздел 6. Воспроизводит буквально пример corr-114
из спецификации: коммутатор доступа падает, хосты за ним становятся
недоступны — должен получиться один инцидент с одним корнем."""
from datetime import datetime, timedelta

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.models.db import Base, CmdbObject, CorrelationRule, Incident, IncidentProblem, Problem
from services.pipeline.correlator import try_correlate

T0 = datetime(2026, 8, 6, 10, 0, 0)


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        s.add(CmdbObject(id="sw-acc-01", kind="switch", site="brd-noyabrsk", name="sw-acc-01",
                          subnet="10.42.2.0/24", parent_switch_id="sw-core-01"))
        s.add(CmdbObject(id="brd-noyabrsk/app-01", kind="server", site="brd-noyabrsk", name="app-01",
                          subnet="10.42.2.0/24", parent_switch_id="sw-acc-01"))
        s.add(CmdbObject(id="brd-noyabrsk/app-02", kind="server", site="brd-noyabrsk", name="app-02",
                          subnet="10.42.2.0/24", parent_switch_id="sw-acc-01"))
        s.add(CmdbObject(id="brd-noyabrsk/other-01", kind="server", site="brd-noyabrsk", name="other-01",
                          subnet="10.42.9.0/24", parent_switch_id="sw-acc-09"))  # другая подсеть/switch
        s.add(CorrelationRule(rule_id="corr-114", name="test", trigger_symptom="host_unreachable",
                               cause_symptom="node_down", match_axes="subnet,site,topology", window_s=120,
                               status="active"))
        s.commit()
        yield s


def _problem(session, dedup_key, symptom_class, object_id, site, opened_at) -> Problem:
    p = Problem(dedup_key=dedup_key, status="OPEN", object_id=object_id, symptom_class=symptom_class,
                site=site, opened_at=opened_at, last_seen_at=opened_at, repeat_count=1, toggle_count=0)
    session.add(p)
    session.flush()
    return p


def test_cascade_clusters_into_one_incident_with_correct_root(session):
    cause = _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    sym1 = _problem(session, "dk-app01", "host_unreachable", "brd-noyabrsk/app-01", "brd-noyabrsk",
                     T0 + timedelta(seconds=10))
    sym2 = _problem(session, "dk-app02", "host_unreachable", "brd-noyabrsk/app-02", "brd-noyabrsk",
                     T0 + timedelta(seconds=30))
    session.commit()

    inc1 = try_correlate(session, sym1)
    inc2 = try_correlate(session, sym2)

    assert inc1 is not None
    assert inc1.id == inc2.id
    assert inc1.root_problem_id == cause.id

    members = session.query(IncidentProblem).filter_by(incident_id=inc1.id).all()
    roles = {m.problem_id: m.role for m in members}
    assert roles[cause.id] == "root"
    assert roles[sym1.id] == "symptom"
    assert roles[sym2.id] == "symptom"
    assert session.query(Incident).count() == 1


def test_no_correlation_across_different_subnets(session):
    _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    unrelated = _problem(session, "dk-other", "host_unreachable", "brd-noyabrsk/other-01",
                          "brd-noyabrsk", T0 + timedelta(seconds=10))
    session.commit()

    result = try_correlate(session, unrelated)
    assert result is None


def test_still_active_cause_correlates_no_matter_how_long_ago_it_opened(session):
    """Раздел 6.1: причина остаётся причиной, пока не устранена — коммутатор,
    лежащий 4 часа, всё ещё причина недоступности хоста на 4-м часу."""
    _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    much_later = _problem(session, "dk-app01-late", "host_unreachable", "brd-noyabrsk/app-01",
                           "brd-noyabrsk", T0 + timedelta(hours=4))
    session.commit()

    result = try_correlate(session, much_later)
    assert result is not None


def test_no_correlation_once_cause_resolved_outside_grace_window(session):
    cause = _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    cause.status = "RESOLVED"
    cause.resolved_at = T0 + timedelta(seconds=5)
    late = _problem(session, "dk-app01-late", "host_unreachable", "brd-noyabrsk/app-01",
                     "brd-noyabrsk", T0 + timedelta(seconds=126))  # 121с после resolved_at, окно 120с
    session.commit()

    result = try_correlate(session, late)
    assert result is None


def test_correlation_within_grace_window_after_cause_resolved(session):
    cause = _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    cause.status = "RESOLVED"
    cause.resolved_at = T0 + timedelta(seconds=5)
    soon_after = _problem(session, "dk-app01-soon", "host_unreachable", "brd-noyabrsk/app-01",
                           "brd-noyabrsk", T0 + timedelta(seconds=60))  # 55с после resolved_at
    session.commit()

    result = try_correlate(session, soon_after)
    assert result is not None


def test_root_candidate_never_becomes_someone_elses_symptom(session):
    """Раздел 6.7: кандидат в первопричину не подавляется."""
    cause = _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    sym = _problem(session, "dk-app01", "host_unreachable", "brd-noyabrsk/app-01", "brd-noyabrsk",
                    T0 + timedelta(seconds=10))
    session.commit()
    try_correlate(session, sym)  # делает cause корнем инцидента

    # Гипотетическое правило, где node_down сам является триггером чего-то ещё,
    # не должно суметь свернуть уже-корневую проблему в чужой инцидент.
    session.add(CorrelationRule(rule_id="corr-bogus", name="bogus", trigger_symptom="node_down",
                                 cause_symptom="host_unreachable", match_axes="site", window_s=999999,
                                 status="active"))
    session.commit()

    result = try_correlate(session, cause)
    assert result is None


def test_already_correlated_problem_is_not_reprocessed(session):
    _problem(session, "dk-switch", "node_down", "sw-acc-01", "brd-noyabrsk", T0)
    sym = _problem(session, "dk-app01", "host_unreachable", "brd-noyabrsk/app-01", "brd-noyabrsk",
                    T0 + timedelta(seconds=10))
    session.commit()

    first = try_correlate(session, sym)
    second = try_correlate(session, sym)
    assert first is not None
    assert second is None  # уже incident_id стоит — повторно не обрабатывается
