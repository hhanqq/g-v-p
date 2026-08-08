"""Тесты маршрутизации по подпискам — раздел 8 (M5). Проверяет, что
разные подписчики получают только то, на что подписаны, а объект/сервис
без владельца не роняет резолвинг (раздел 4.2 — это нормальное состояние
данных)."""
from datetime import datetime

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.common.routing import resolve_recipients
from packages.models.db import (Base, CmdbObject, CmdbOwnership, CmdbService, CmdbServiceObject,
                                 Problem, Subscriber, Subscription)

T0 = datetime(2026, 8, 6, 10, 0, 0)


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        s.add(CmdbObject(id="brd-noyabrsk/rig-01", kind="controller", site="brd-noyabrsk", name="rig-01"))
        s.add(CmdbObject(id="brd-khantos/rig-02", kind="controller", site="brd-khantos", name="rig-02"))
        s.add(CmdbObject(id="brd-noyabrsk/orphan-01", kind="server", site="brd-noyabrsk", name="orphan-01"))
        s.add(CmdbService(id="svc-drilling", name="Бурение", criticality="high"))
        s.add(CmdbServiceObject(service_id="svc-drilling", object_id="brd-noyabrsk/rig-01"))
        s.add(CmdbOwnership(object_id="brd-noyabrsk/rig-01", subsidiary="gpn-noyabrsk"))
        s.add(CmdbOwnership(object_id="brd-khantos/rig-02", subsidiary="gpn-khantos"))
        # orphan-01 намеренно без CmdbOwnership — раздел 4.2

        noc = Subscriber(trueconf_username="noc", access_token="t-noc", created_at=T0)
        noyabrsk_owner = Subscriber(trueconf_username="noyabrsk_owner", access_token="t-noyabrsk", created_at=T0)
        p2_only = Subscriber(trueconf_username="p2_only", access_token="t-p2", created_at=T0)
        s.add_all([noc, noyabrsk_owner, p2_only])
        s.flush()
        s.add(Subscription(subscriber_id=noc.id, created_at=T0))  # без фильтров — "вижу всё"
        s.add(Subscription(subscriber_id=noyabrsk_owner.id, subsidiary="gpn-noyabrsk", created_at=T0))
        s.add(Subscription(subscriber_id=p2_only.id, priority_threshold="P2", created_at=T0))
        s.commit()
        yield s


def _problem(object_id, priority) -> Problem:
    return Problem(dedup_key="x", status="OPEN", object_id=object_id, symptom_class="node_down",
                    site="brd-noyabrsk", opened_at=T0, last_seen_at=T0, repeat_count=1,
                    toggle_count=0, priority=priority)


def test_owner_and_noc_get_it_stranger_does_not(session):
    # P3, чтобы не зацепить p2_only (порог приоритета — другая ось, проверяется отдельно)
    recipients = resolve_recipients(session, _problem("brd-noyabrsk/rig-01", "P3"))
    assert recipients == ["noc", "noyabrsk_owner"]


def test_priority_threshold_filters_out_low_priority(session):
    recipients = resolve_recipients(session, _problem("brd-noyabrsk/rig-01", "P3"))
    assert "p2_only" not in recipients
    assert "noc" in recipients  # у noc порога нет — приходит всё


def test_priority_threshold_lets_through_critical(session):
    recipients = resolve_recipients(session, _problem("brd-noyabrsk/rig-01", "P0"))
    assert "p2_only" in recipients


def test_different_subsidiary_not_routed_to_unrelated_owner(session):
    recipients = resolve_recipients(session, _problem("brd-khantos/rig-02", "P1"))
    assert "noyabrsk_owner" not in recipients
    assert "noc" in recipients


def test_object_without_owner_still_reaches_catch_all_subscriber(session):
    """Раздел 4.2: объект без владельца — не ошибка, просто адресных
    подписчиков не находится; подписчик "вижу всё" получает его всё равно."""
    recipients = resolve_recipients(session, _problem("brd-noyabrsk/orphan-01", "P3"))
    assert recipients == ["noc"]


def test_deactivated_subscriber_excluded_from_routing(session):
    """Раздел «Безопасность» — автоотключение при увольнении
    (packages/common/deprovision.py): active=False подписчик не должен
    получать уведомления, даже если его Subscription формально совпадает."""
    noyabrsk_owner = session.query(Subscriber).filter_by(trueconf_username="noyabrsk_owner").one()
    noyabrsk_owner.active = False
    session.commit()
    recipients = resolve_recipients(session, _problem("brd-noyabrsk/rig-01", "P3"))
    assert "noyabrsk_owner" not in recipients
    assert "noc" in recipients
