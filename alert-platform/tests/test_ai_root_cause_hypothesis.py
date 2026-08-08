"""Тесты промпта гипотезы первопричины — раздел 6.1. Саму LLM не тестируем
— только то, что промпт передаёт дословные тексты обоих кандидатов
(раздел И2) и явно требует пометки "предположение"."""
from packages.ai.root_cause_hypothesis import build_hypothesis_prompt


def test_prompt_contains_both_verbatim_texts_and_site():
    prompt = build_hypothesis_prompt(
        site="brd-noyabrsk",
        candidate_a_text="PROBLEM: sw-core-01: Unavailable by ICMP ping",
        candidate_b_text="PROBLEM: Power lost on PDU well1-plc-01",
    )
    assert "brd-noyabrsk" in prompt
    assert "PROBLEM: sw-core-01: Unavailable by ICMP ping" in prompt
    assert "PROBLEM: Power lost on PDU well1-plc-01" in prompt


def test_prompt_requires_hypothesis_label():
    prompt = build_hypothesis_prompt(site="x", candidate_a_text="a", candidate_b_text="b")
    assert "предположение" in prompt


def test_prompt_truncates_long_texts():
    prompt = build_hypothesis_prompt(site="x", candidate_a_text="a" * 5000, candidate_b_text="b" * 5000)
    assert len(prompt) < 3000
