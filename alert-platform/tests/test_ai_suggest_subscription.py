"""Тесты подсказки подписки — раздел 5/8. Проверяет, что промпт строится
только из реальных переданных чисел (модель ничего не добавляет от себя),
и что пустая история не роняет вызов."""
from packages.ai.suggest_subscription import build_suggestion_prompt, extract_recommended_subsidiary


def test_prompt_contains_all_stats():
    prompt = build_suggestion_prompt([("gpn-noyabrsk", 40, 5), ("gpn-khantos", 12, 1)])
    assert "gpn-noyabrsk" in prompt
    assert "40" in prompt and "5" in prompt
    assert "gpn-khantos" in prompt
    assert "ничего не добавляй от себя" in prompt


def test_prompt_none_when_no_history():
    assert build_suggestion_prompt([]) is None


STATS = [("gpn-noyabrsk", 6, 1), ("gpn-khantos", 2, 2)]


def test_extract_uses_whatever_model_actually_named():
    # Регрессия на живой баг: текст рекомендовал gpn-khantos (по критичности),
    # а если бы кнопка вела на топ-1 по total (gpn-noyabrsk), текст и кнопка
    # разошлись бы. Кнопка обязана следовать за текстом.
    assert extract_recommended_subsidiary("gpn-khantos — потому что...", STATS) == "gpn-khantos"


def test_extract_falls_back_to_top_total_when_unparseable():
    assert extract_recommended_subsidiary("Сложно сказать, оба варианта...", STATS) == "gpn-noyabrsk"


def test_extract_falls_back_when_named_subsidiary_not_in_stats():
    assert extract_recommended_subsidiary("gpn-nonexistent — самый важный", STATS) == "gpn-noyabrsk"


def test_extract_handles_none_reply():
    assert extract_recommended_subsidiary(None, STATS) == "gpn-noyabrsk"
