"""Тесты приоритизации — раздел 7. Чистые функции, БД не нужна."""
from datetime import datetime

from packages.rules.priority import business_impact_from_criticality, compute_priority

DAY = datetime(2026, 8, 6, 14, 0, 0)   # день, не ночь
NIGHT = datetime(2026, 8, 6, 3, 0, 0)  # ночь


def test_business_impact_lookup():
    assert business_impact_from_criticality("высшая") == 5
    assert business_impact_from_criticality("низшая") == 0
    assert business_impact_from_criticality(None) == 0
    assert business_impact_from_criticality("не-существует") == 0


def test_matrix_cell_extremes():
    priority, breakdown = compute_priority(technical_severity=5, business_impact=5,
                                            repeat_count=0, occurred_at=DAY)
    assert priority == "P0"
    assert breakdown["matrix_cell"] == "P0"
    assert breakdown["modifiers_applied"] == []

    priority, breakdown = compute_priority(technical_severity=0, business_impact=0,
                                            repeat_count=0, occurred_at=DAY)
    assert priority == "P3"


def test_night_modifier_escalates():
    day_priority, _ = compute_priority(technical_severity=2, business_impact=2,
                                        repeat_count=0, occurred_at=DAY)
    night_priority, breakdown = compute_priority(technical_severity=2, business_impact=2,
                                                  repeat_count=0, occurred_at=NIGHT)
    order = ["P0", "P1", "P2", "P3"]
    assert order.index(night_priority) <= order.index(day_priority)
    assert "ночное время" in breakdown["modifiers_applied"]


def test_repeat_modifier_escalates():
    priority_no_repeat, _ = compute_priority(technical_severity=1, business_impact=1,
                                              repeat_count=1, occurred_at=DAY)
    priority_repeated, breakdown = compute_priority(technical_severity=1, business_impact=1,
                                                      repeat_count=50, occurred_at=DAY)
    order = ["P0", "P1", "P2", "P3"]
    assert order.index(priority_repeated) <= order.index(priority_no_repeat)
    assert any("повторяемость" in m for m in breakdown["modifiers_applied"])


def test_maintenance_window_deescalates():
    priority, breakdown = compute_priority(technical_severity=5, business_impact=5,
                                            repeat_count=0, occurred_at=DAY, maintenance_active=True)
    assert priority != "P0"  # де-эскалировано из максимума
    assert "активное окно плановых работ" in breakdown["modifiers_applied"]


def test_out_of_range_inputs_are_clamped():
    priority, breakdown = compute_priority(technical_severity=99, business_impact=-5,
                                            repeat_count=0, occurred_at=DAY)
    assert breakdown["technical_severity"] == 5
    assert breakdown["business_impact"] == 0
