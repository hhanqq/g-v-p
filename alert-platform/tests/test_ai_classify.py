"""Тесты семантической классификации — раздел 5. Саму LLM не тестируем
(сетевая, в Ollama на VM) — только промпт и разбор её ответа, включая
защиту от "уверенного, но незнакомого" ответа модели."""
from packages.ai.classify import KNOWN_SYMPTOM_CLASSES, _parse_classification, build_classify_prompt


def test_prompt_lists_only_known_classes():
    prompt = build_classify_prompt("Disk usage on srv-01 reached 92%")
    for cls in KNOWN_SYMPTOM_CLASSES:
        assert cls in prompt


def test_prompt_truncates_long_raw_text():
    prompt = build_classify_prompt("x" * 5000)
    assert len(prompt) < 2000


def test_parse_accepts_known_class():
    assert _parse_classification("disk_space") == "disk_space"
    assert _parse_classification(" disk_space.\n") == "disk_space"


def test_parse_rejects_unknown_word():
    assert _parse_classification("это не похоже ни на что из списка") is None


def test_parse_handles_empty_reply():
    assert _parse_classification(None) is None
    assert _parse_classification("") is None


def test_parse_explicit_unknown_stays_unknown():
    assert _parse_classification("unknown") is None  # "unknown" не входит в KNOWN_SYMPTOM_CLASSES
