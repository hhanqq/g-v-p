"""Наполнение справочников CMDB — раздел 17.1 (`scripts/seed_demo.py`).

Единый источник истины для резолвера и для генератора алертов: и датасет
datagen, и CMDB в БД строятся из одного и того же inventory.yaml с одним
и тем же --seed. Если сиды разойдутся, резолвер будет видеть другой набор
объектов, чем тот, что упоминается в сгенерированных алертах — сид ОБЯЗАН
совпадать с тем, что использовался при генерации демо-потока.

Использование:
    DATABASE_URL=postgresql+psycopg2://alert:alert@localhost:5433/alert_platform \\
        python3 scripts/seed_demo.py --seed 42
"""
from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from sqlalchemy import create_engine, delete  # noqa: E402
from sqlalchemy.orm import Session  # noqa: E402

import yaml  # noqa: E402

from datagen.generate import HERE as DATAGEN_DIR  # noqa: E402
from datagen.generate import load_yaml  # noqa: E402
from datagen.inventory_builder import build_inventory  # noqa: E402
from packages.models.db import (Base, CmdbAlias, CmdbObject, CmdbOwnership,  # noqa: E402
                                 CmdbService, CmdbServiceObject, CorrelationRule)

RULES_DIR = Path(__file__).resolve().parents[1] / "packages" / "rules" / "correlations"


def seed(database_url: str, seed_value: int) -> None:
    engine = create_engine(database_url)
    Base.metadata.create_all(bind=engine)

    cfg = load_yaml(DATAGEN_DIR / "inventory.yaml")
    inv = build_inventory(cfg, seed_value)

    with Session(engine) as session:
        # Реидемпотентно: чистим и наполняем заново для указанного seed.
        session.execute(delete(CmdbServiceObject))
        session.execute(delete(CmdbOwnership))
        session.execute(delete(CmdbAlias))
        session.execute(delete(CmdbService))
        session.execute(delete(CmdbObject))
        session.execute(delete(CorrelationRule))
        session.commit()

        # Сначала объекты и сервисы (родительские таблицы), затем всё, что
        # на них ссылается по FK — двумя отдельными commit(), чтобы порядок
        # вставки был явным и не зависел от планировщика unit-of-work.
        for host in inv.hosts.values():
            session.add(CmdbObject(id=host.id, kind=host.kind, site=host.site, name=host.name,
                                    fqdn=host.fqdn, ip=host.ip, inventory_id=host.inventory_id,
                                    subnet=host.subnet, parent_switch_id=host.access_switch_id))
        for switch in inv.switches.values():
            session.add(CmdbObject(id=switch.id, kind="switch", site=switch.site,
                                    name=switch.name, fqdn=None, ip=switch.ip, inventory_id=None,
                                    subnet=switch.subnet, parent_switch_id=switch.parent_id))
        for svc in inv.services.values():
            session.add(CmdbService(id=svc.id, name=svc.name, criticality=svc.criticality))
        session.commit()

        for host in inv.hosts.values():
            session.add(CmdbAlias(site=host.site, raw_name=host.name, object_id=host.id))
            for owner in host.owners:
                session.add(CmdbOwnership(object_id=host.id, subsidiary=owner))
        for switch in inv.switches.values():
            session.add(CmdbAlias(site=switch.site, raw_name=switch.name, object_id=switch.id))
        for svc in inv.services.values():
            for obj_id in svc.object_ids:
                session.add(CmdbServiceObject(service_id=svc.id, object_id=obj_id))
            for owner in svc.owners:
                session.add(CmdbOwnership(service_id=svc.id, subsidiary=owner))

        for rule_path in sorted(RULES_DIR.glob("*.yaml")):
            rule = yaml.safe_load(rule_path.read_text(encoding="utf-8"))
            session.add(CorrelationRule(
                rule_id=rule["rule_id"], name=rule["name"],
                trigger_symptom=rule["trigger_symptom"], cause_symptom=rule["cause_symptom"],
                match_axes=",".join(rule["match_axes"]), window_s=rule["window_s"],
                status=rule["status"], author=rule.get("author"), created_from=rule.get("created_from"),
            ))
        session.commit()

    print(f"CMDB наполнена: {len(inv.hosts)} хостов, {len(inv.switches)} коммутаторов, "
          f"{len(inv.services)} сервисов, правил корреляции: {len(list(RULES_DIR.glob('*.yaml')))} (seed={seed_value})")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--database-url", type=str,
                         default=os.environ.get("DATABASE_URL",
                                                 "postgresql+psycopg2://alert:alert@localhost:5433/alert_platform"))
    args = parser.parse_args()
    seed(args.database_url, args.seed)


if __name__ == "__main__":
    main()
