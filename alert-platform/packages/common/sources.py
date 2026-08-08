"""Паспорта источников (раздел 4.3/11.2) — теперь в БД (`SourceInstance`),
редактируются через консоль (`/sources`), а не только через
`connectors/sources.yaml`. Кейс, п.2 условий: "архитектура должна
предусматривать возможность... подключения новых источников/каналов без
изменения ядра системы" — регистрация нового инстанса существующей
системы мониторинга (пятый филиал того же Zabbix, например) больше не
требует правки репозитория и редеплоя, только формы в консоли.

YAML остаётся демо-затравкой: `seed_from_yaml_if_empty` копирует его в БД
один раз, если таблица пуста (свежий стенд) — дальше источник истины БД,
YAML не перечитывается."""
from __future__ import annotations

from datetime import datetime
from pathlib import Path

import yaml
from sqlalchemy import select

from packages.models.db import SourceInstance


def seed_from_yaml_if_empty(session, yaml_path: Path) -> None:
    if session.execute(select(SourceInstance.id).limit(1)).scalar_one_or_none() is not None:
        return
    cfg = yaml.safe_load(yaml_path.read_text(encoding="utf-8"))
    for s in cfg.get("sources", []):
        session.add(SourceInstance(instance=s["instance"], system=s["system"], site=s["site"],
                                    created_at=datetime.utcnow()))
    session.commit()


def load_passports(session) -> dict[str, dict]:
    """instance -> {system, site} — тот же формат, что раньше отдавал
    services.pipeline.parser.load_source_passports(YAML), чтобы worker.py
    менялся минимально."""
    rows = session.execute(select(SourceInstance)).scalars().all()
    return {r.instance: {"system": r.system, "site": r.site} for r in rows}
