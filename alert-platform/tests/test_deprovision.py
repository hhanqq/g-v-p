"""Тесты автоотключения подписок при увольнении — раздел «Безопасность».
LDAP не поднимаем в тестах (это отдельный docker-сервис, проверен вживую
— см. память проекта) — здесь только логика деактивации, с LDAP-клиентом
через monkeypatch."""
from datetime import datetime

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.common import deprovision
from packages.models.db import AuditLog, Base, Subscriber

T0 = datetime(2026, 8, 6, 10, 0, 0)


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        s.add_all([
            Subscriber(trueconf_username="stays", access_token="t1", active=True, created_at=T0),
            Subscriber(trueconf_username="leaves", access_token="t2", active=True, created_at=T0),
            Subscriber(trueconf_username="already_inactive", access_token="t3", active=False, created_at=T0),
        ])
        s.commit()
        yield s


def test_deactivates_subscriber_missing_from_ldap(session, monkeypatch):
    monkeypatch.setattr(deprovision.ldap_auth, "list_active_usernames", lambda: {"stays"})
    n = deprovision.deactivate_departed_subscribers(session)
    assert n == 1
    stays = session.query(Subscriber).filter_by(trueconf_username="stays").one()
    leaves = session.query(Subscriber).filter_by(trueconf_username="leaves").one()
    assert stays.active is True
    assert leaves.active is False


def test_writes_audit_entry_for_deprovision(session, monkeypatch):
    monkeypatch.setattr(deprovision.ldap_auth, "list_active_usernames", lambda: {"stays"})
    deprovision.deactivate_departed_subscribers(session)
    entry = session.query(AuditLog).filter_by(action="deprovision").one()
    assert entry.target == "leaves"
    assert entry.actor == "system"


def test_ldap_unavailable_deactivates_nobody(session, monkeypatch):
    monkeypatch.setattr(deprovision.ldap_auth, "list_active_usernames", lambda: None)
    n = deprovision.deactivate_departed_subscribers(session)
    assert n == 0
    leaves = session.query(Subscriber).filter_by(trueconf_username="leaves").one()
    assert leaves.active is True  # раздел И5 — недоступность LDAP никого не деактивирует


def test_empty_ldap_response_deactivates_nobody(session, monkeypatch):
    monkeypatch.setattr(deprovision.ldap_auth, "list_active_usernames", lambda: set())
    n = deprovision.deactivate_departed_subscribers(session)
    assert n == 0


def test_already_inactive_subscriber_not_touched_again(session, monkeypatch):
    monkeypatch.setattr(deprovision.ldap_auth, "list_active_usernames", lambda: {"stays", "leaves"})
    deprovision.deactivate_departed_subscribers(session)
    audit_count = session.query(AuditLog).filter_by(action="deprovision").count()
    assert audit_count == 0  # already_inactive не в LDAP, но уже active=False — не логируется повторно
