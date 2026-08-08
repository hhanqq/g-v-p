"""Тесты регистрации источников — раздел 4.3/11.2, кейс п.2 условий
("подключение новых источников без изменения ядра"). Проверяет затравку
из YAML на пустой БД и что повторный вызов её не дублирует, плюс формат
паспортов, отдаваемый worker'у."""
from pathlib import Path

import pytest
import yaml
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.common.sources import load_passports, seed_from_yaml_if_empty
from packages.models.db import Base, SourceInstance

SOURCES_YAML = Path(__file__).resolve().parents[1] / "connectors" / "sources.yaml"


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        yield s


def test_seed_populates_from_real_yaml(session):
    seed_from_yaml_if_empty(session, SOURCES_YAML)
    expected = yaml.safe_load(SOURCES_YAML.read_text(encoding="utf-8"))["sources"]
    count = session.query(SourceInstance).count()
    assert count == len(expected)


def test_seed_is_noop_when_not_empty(session, tmp_path):
    session.add(SourceInstance(instance="manual-01", system="zabbix", site="gpn-x",
                                created_at=__import__("datetime").datetime.utcnow()))
    session.commit()

    other_yaml = tmp_path / "sources.yaml"
    other_yaml.write_text(yaml.dump({"sources": [
        {"instance": "should-not-appear", "system": "zabbix", "site": "y"}
    ]}), encoding="utf-8")

    seed_from_yaml_if_empty(session, other_yaml)
    instances = {r.instance for r in session.query(SourceInstance).all()}
    assert instances == {"manual-01"}


def test_load_passports_format(session):
    from datetime import datetime
    session.add(SourceInstance(instance="zbx-x-01", system="zabbix", site="gpn-x",
                                created_at=datetime.utcnow()))
    session.commit()
    passports = load_passports(session)
    assert passports == {"zbx-x-01": {"system": "zabbix", "site": "gpn-x"}}
