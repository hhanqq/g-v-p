"""ИИ-сценарий «рекомендации по устранению проблемы на основе базы
знаний» (раздел 5, раздел 20 п.6 списка сценариев из кейса).

Раздел 13 — тот же принцип, что и у саммаризации: LLM ничего не выдумывает
и не заменяет базу знаний, а выбирает и коротко формулирует релевантные
пункты ИЗ уже существующего чек-листа (packages/rules/runbooks.yaml) под
конкретику инцидента (объект, площадка, count связанных симптомов). Если
LLM недоступна — вызывающая сторона просто показывает пункты чек-листа
как есть, без ИИ-фразировки (раздел И5: деградация, не отказ)."""
from __future__ import annotations

from pathlib import Path

import yaml

from packages.ai.client import ask

RUNBOOKS_PATH = Path(__file__).resolve().parents[1] / "rules" / "runbooks.yaml"
_runbooks: dict[str, list[str]] | None = None


def load_runbooks() -> dict[str, list[str]]:
    global _runbooks
    if _runbooks is None:
        with open(RUNBOOKS_PATH, encoding="utf-8") as f:
            _runbooks = yaml.safe_load(f) or {}
    return _runbooks


def checklist_for(symptom_class: str) -> list[str]:
    return load_runbooks().get(symptom_class, [])


def build_recommendation_prompt(*, symptom_class: str, object_name: str, site: str,
                                 n_related: int, checklist: list[str]) -> str | None:
    if not checklist:
        return None
    items = "\n".join(f"- {step}" for step in checklist)
    return (
        f"Инцидент мониторинга: {object_name} на площадке {site}, симптом {symptom_class}, "
        f"связанных алертов в каскаде: {n_related}.\n\n"
        f"Чек-лист устранения из базы знаний для этого класса симптома:\n{items}\n\n"
        f"Выбери и коротко (1-3 предложения, без списка и заголовка) сформулируй для дежурного "
        f"инженера, с чего начать именно в этом случае — используй ТОЛЬКО пункты чек-листа выше, "
        f"ничего не добавляй от себя и не упоминай факты, которых нет в условии."
    )


async def recommend_remediation(*, symptom_class: str, object_name: str, site: str,
                                 n_related: int) -> str | None:
    checklist = checklist_for(symptom_class)
    prompt = build_recommendation_prompt(symptom_class=symptom_class, object_name=object_name,
                                          site=site, n_related=n_related, checklist=checklist)
    if prompt is None:
        return None
    return await ask(prompt, num_predict=200)
