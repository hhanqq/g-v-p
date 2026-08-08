"""Загрузка и валидация коннектора — раздел 5, стадия 1.

Коннектор целиком описывается YAML-файлом в connectors/*.yaml. Это то,
что подключает новый источник без изменения ядра (раздел 5, 12.1, 17.3).
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path

import yaml


@dataclass
class SymptomRule:
    pattern: re.Pattern
    symptom_class: str
    component_group: int | None = None


@dataclass
class Connector:
    source_system: str
    version: int
    state_map: dict[str, str]
    severity_map: dict[str, int]
    time_format: str
    field_patterns: dict[str, re.Pattern]
    required_fields: list[str]
    symptom_rules: list[SymptomRule] = field(default_factory=list)

    def state_prefix_pattern(self) -> re.Pattern:
        return self.field_patterns["state_prefix"]


def load_connector(path: Path) -> Connector:
    cfg = yaml.safe_load(path.read_text(encoding="utf-8"))

    missing = [k for k in ("source_system", "version", "state_map", "fields", "required_fields")
               if k not in cfg]
    if missing:
        raise ValueError(f"{path}: коннектор не описывает обязательные поля {missing}")

    field_patterns = {name: re.compile(pattern) for name, pattern in cfg["fields"].items()}
    symptom_rules = [
        SymptomRule(pattern=re.compile(r["match"]), symptom_class=r["symptom_class"],
                    component_group=r.get("component_group"))
        for r in cfg.get("symptom_rules", [])
    ]
    return Connector(
        source_system=cfg["source_system"],
        version=cfg["version"],
        state_map=cfg["state_map"],
        severity_map=cfg.get("severity_map", {}),
        time_format=cfg.get("time_format", "%Y-%m-%dT%H:%M:%S"),
        field_patterns=field_patterns,
        required_fields=cfg["required_fields"],
        symptom_rules=symptom_rules,
    )


def load_connectors(connectors_dir: Path) -> dict[str, Connector]:
    result = {}
    for path in sorted(connectors_dir.glob("*.yaml")):
        if path.name == "sources.yaml":
            continue
        conn = load_connector(path)
        result[conn.source_system] = conn
    return result


def load_source_passports(path: Path) -> dict[str, dict]:
    """instance -> {system, site}, раздел 4.3/11.2 — площадка не в тексте алерта."""
    cfg = yaml.safe_load(path.read_text(encoding="utf-8"))
    return {s["instance"]: {"system": s["system"], "site": s["site"]} for s in cfg["sources"]}
