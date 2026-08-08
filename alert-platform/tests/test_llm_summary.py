"""Тесты построения промпта для саммаризации — раздел 13. Сам вызов LLM
(сетевой, в отдельном сервисе на VM) здесь не тестируется — только то, что
мы честно передаём модели факты и не подсовываем домыслов."""
from services.delivery_trueconf.llm_summary import build_prompt


def test_prompt_contains_all_facts_and_no_invented_data():
    prompt = build_prompt(
        root_symptom="node_down", root_object="sw-brd-noyabrsk-acc-04",
        root_site="БРД-Ноябрьск", opened_at="2026-08-06 11:00:01",
        symptoms=[("well1-plc-02", "host_unreachable"), ("well3-plc-02", "host_unreachable")],
        rule_names=["corr-114"],
    )
    for fact in ["node_down", "sw-brd-noyabrsk-acc-04", "БРД-Ноябрьск", "2026-08-06 11:00:01",
                 "well1-plc-02", "well3-plc-02", "corr-114"]:
        assert fact in prompt
    assert "без домыслов" in prompt


def test_prompt_handles_no_rule_gracefully():
    prompt = build_prompt(root_symptom="power_lost", root_object="well1-plc-01", root_site="x",
                           opened_at="now", symptoms=[], rule_names=[])
    assert "не определено" in prompt


def test_prompt_explicitly_forbids_priority_guessing():
    """Живой прогон 06.08.2026 показал: модель может НЕВЕРНО назвать
    переданный ей приоритет (P3 в данных -> P2 в ответе). Приоритет больше
    не передаётся вообще, и модели явно запрещено его упоминать."""
    prompt = build_prompt(root_symptom="node_down", root_object="x", root_site="y",
                           opened_at="now", symptoms=[], rule_names=["corr-114"])
    assert "приоритет" not in prompt.lower().split("не упоминай приоритет")[0]
    assert "не упоминай приоритет" in prompt.lower()
