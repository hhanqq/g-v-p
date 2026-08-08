"""Тесты построения промпта сводки за день — раздел «Использование ИИ»,
команда /сводка. Сам вызов LLM (сетевой) не тестируется — только то, что
модели передаются реальные посчитанные факты, без домыслов."""
from packages.ai.daily_summary import build_prompt


def test_prompt_contains_all_facts():
    prompt = build_prompt(date_str="2026-08-08", total=7, open_count=2, resolved_count=5,
                           by_priority={"P0": 1, "P1": 3, "P2": 3},
                           top_symptoms=[("host_unreachable", 4), ("disk_space", 2)])
    for fact in ["2026-08-08", "7", "2", "5", "P0: 1", "host_unreachable (4)", "disk_space (2)"]:
        assert fact in prompt
    assert "без домыслов" in prompt


def test_prompt_handles_empty_breakdown_gracefully():
    prompt = build_prompt(date_str="2026-08-08", total=0, open_count=0, resolved_count=0,
                           by_priority={}, top_symptoms=[])
    assert "не определён" in prompt
    assert "нет" in prompt
