"""Тесты рекомендаций из базы знаний — раздел 5. Проверяет, что промпт
строится только из реальных пунктов runbooks.yaml (модель ничего не
добавляет от себя, раздел 13), и что отсутствие чек-листа для класса не
роняет вызов, а просто не даёт рекомендации (раздел И5)."""
from packages.ai.recommend import build_recommendation_prompt, checklist_for, load_runbooks


def test_runbooks_cover_all_symptom_classes_known_to_correlator():
    # Тот же список, что и в packages/ai/classify.KNOWN_SYMPTOM_CLASSES —
    # если сценарий классификации может назвать класс, для него должна
    # быть база знаний, иначе рекомендация тихо пропадёт без причины.
    from packages.ai.classify import KNOWN_SYMPTOM_CLASSES
    runbooks = load_runbooks()
    for cls in KNOWN_SYMPTOM_CLASSES:
        assert cls in runbooks, f"нет чек-листа для {cls}"
        assert len(runbooks[cls]) >= 2


def test_checklist_for_unknown_class_is_empty_not_error():
    assert checklist_for("totally_made_up_class") == []


def test_prompt_contains_only_real_checklist_items():
    checklist = checklist_for("disk_space")
    prompt = build_recommendation_prompt(
        symptom_class="disk_space", object_name="srv-01", site="БРД-Ноябрьск",
        n_related=3, checklist=checklist,
    )
    for step in checklist:
        assert step in prompt
    assert "ничего не добавляй от себя" in prompt


def test_prompt_is_none_when_no_checklist():
    prompt = build_recommendation_prompt(
        symptom_class="totally_made_up_class", object_name="x", site="y",
        n_related=1, checklist=[],
    )
    assert prompt is None
