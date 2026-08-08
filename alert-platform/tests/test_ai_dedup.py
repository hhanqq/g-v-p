"""Тесты детекции дублей между источниками — раздел 4.1. Саму LLM не
тестируем — только промпт (передаёт дословный текст, раздел И2) и разбор
вердикта, включая защиту от невнятного ответа."""
from packages.ai.dedup import _parse_verdict, build_dedup_prompt


def test_prompt_contains_both_verbatim_texts():
    prompt = build_dedup_prompt("PROBLEM: sw-01 down (zabbix)", "Node sw-01 is down (solarwinds)")
    assert "PROBLEM: sw-01 down (zabbix)" in prompt
    assert "Node sw-01 is down (solarwinds)" in prompt


def test_parse_verdict_same():
    assert _parse_verdict("same") is True
    assert _parse_verdict(" Same.\n") is True


def test_parse_verdict_different():
    assert _parse_verdict("different") is False


def test_parse_verdict_unintelligible_reply_is_none():
    assert _parse_verdict("возможно, но не уверен") is None


def test_parse_verdict_empty():
    assert _parse_verdict(None) is None
    assert _parse_verdict("") is None
