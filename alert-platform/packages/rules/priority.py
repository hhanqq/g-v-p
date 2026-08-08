"""Приоритизация — раздел 7. Две независимые оси, матрица + модификаторы.

Раздел 7: "Любое значение раскрывается до ячейки матрицы и применённых
модификаторов" — поэтому compute_priority возвращает не только итог, но и
breakdown с исходной ячейкой и списком того, что её изменило (раздел И4).
"""
from __future__ import annotations

from datetime import datetime
from pathlib import Path

import yaml

HERE = Path(__file__).parent
PRIORITY_ORDER = ["P0", "P1", "P2", "P3"]

_cfg_cache: dict | None = None


def _load_cfg() -> dict:
    global _cfg_cache
    if _cfg_cache is None:
        _cfg_cache = yaml.safe_load((HERE / "priority_matrix.yaml").read_text(encoding="utf-8"))
    return _cfg_cache


def business_impact_from_criticality(criticality: str | None, cfg: dict | None = None) -> int:
    cfg = cfg or _load_cfg()
    return cfg["business_impact_by_criticality"].get(criticality, 0)


def compute_priority(*, technical_severity: int, business_impact: int, repeat_count: int,
                      occurred_at: datetime, maintenance_active: bool = False,
                      cfg: dict | None = None) -> tuple[str, dict]:
    cfg = cfg or _load_cfg()
    ts = max(0, min(5, technical_severity))
    bi = max(0, min(5, business_impact))
    base = cfg["matrix"][ts][bi]
    idx = PRIORITY_ORDER.index(base)

    applied: list[str] = []
    mods = cfg["modifiers"]

    if repeat_count > mods["repeat_threshold"]:
        idx = max(0, idx - mods["repeat_bump"])
        applied.append(f"повторяемость > {mods['repeat_threshold']} (+{mods['repeat_bump']})")

    if occurred_at.hour in mods["night_hours"]:
        idx = max(0, idx - mods["night_bump"])
        applied.append("ночное время")

    if maintenance_active:
        idx = min(len(PRIORITY_ORDER) - 1, idx + mods["maintenance_active_debump"])
        applied.append("активное окно плановых работ")

    final_priority = PRIORITY_ORDER[idx]
    breakdown = {
        "technical_severity": ts,
        "business_impact": bi,
        "matrix_cell": base,
        "modifiers_applied": applied,
        "final_priority": final_priority,
    }
    return final_priority, breakdown
