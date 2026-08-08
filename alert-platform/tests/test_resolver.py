"""Тесты резолвера — раздел 4.3. Каскад проверяется на SQLite in-memory,
резолвер не использует Postgres-специфичные конструкции, так что реальный
Postgres для этих тестов не нужен (в отличие от pipeline-worker с
FOR UPDATE SKIP LOCKED, который тестируется живьём через docker compose).
"""
import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.models.db import Base, CmdbAlias, CmdbObject
from services.pipeline.resolver import resolve


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        s.add(CmdbObject(id="brd-noyabrsk/app-01", kind="server", site="brd-noyabrsk",
                          name="app-01", fqdn="app-01.brd-noyabrsk.local", ip="10.42.2.5"))
        s.add(CmdbObject(id="brd-noyabrsk/app-02", kind="server", site="brd-noyabrsk",
                          name="app-02", fqdn=None, ip="10.42.2.6"))
        s.add(CmdbAlias(site="brd-noyabrsk", raw_name="app-01", object_id="brd-noyabrsk/app-01"))
        s.add(CmdbAlias(site="brd-noyabrsk", raw_name="app-02", object_id="brd-noyabrsk/app-02"))
        # Тот же короткий "app-01" в другом филиале — намеренная коллизия имён (раздел 4.1).
        s.add(CmdbObject(id="brd-khantos/app-01", kind="server", site="brd-khantos",
                          name="app-01", fqdn=None, ip="10.55.2.5"))
        s.add(CmdbAlias(site="brd-khantos", raw_name="app-01", object_id="brd-khantos/app-01"))
        s.commit()
        yield s


def test_exact_alias_match(session):
    result = resolve(session, "brd-noyabrsk", "app-01", "10.42.2.5")
    assert result.method == "alias"
    assert result.object_id == "brd-noyabrsk/app-01"
    assert result.confidence == 1.0


def test_name_collision_across_sites_resolved_by_site(session):
    r1 = resolve(session, "brd-noyabrsk", "app-01", None)
    r2 = resolve(session, "brd-khantos", "app-01", None)
    assert r1.object_id == "brd-noyabrsk/app-01"
    assert r2.object_id == "brd-khantos/app-01"
    assert r1.object_id != r2.object_id


def test_fqdn_normalized_match(session):
    result = resolve(session, "brd-noyabrsk", "app-01.brd-noyabrsk.local", "10.42.2.5")
    assert result.method == "fqdn"
    assert result.object_id == "brd-noyabrsk/app-01"


def test_ip_match_when_name_unknown(session):
    result = resolve(session, "brd-noyabrsk", "totally-unknown-name", "10.42.2.6")
    assert result.method == "ip"
    assert result.object_id == "brd-noyabrsk/app-02"


def test_fuzzy_match_on_typo(session):
    result = resolve(session, "brd-noyabrsk", "app-01x", None)  # опечатка, но однозначно ближе к app-01
    assert result.method == "fuzzy"
    assert result.object_id == "brd-noyabrsk/app-01"
    assert 0 < result.confidence < 1.0


def test_quarantine_when_nothing_matches(session):
    result = resolve(session, "brd-noyabrsk", "ghost-9999", "10.250.1.2")
    assert result.method == "quarantine"
    assert result.object_id is None


def test_quarantine_without_site():
    result = resolve(None, None, "app-01", "10.42.2.5")
    assert result.method == "quarantine"
    assert result.object_id is None
