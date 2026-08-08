"""Сводка алертов сотрудника за сегодня — раздел «Использование ИИ»,
по команде /сводка в TrueConf-боте. Тот же принцип, что и у саммари
инцидента (services/delivery_trueconf/llm_summary.py): факты считает код
(подсчёт по Problem, раздел 8 — те же алерты, что реально пришли бы
уведомлением этому подписчику), ИИ только формулирует связный абзац
поверх уже посчитанных чисел, не придумывает свои."""
from __future__ import annotations

from packages.ai.client import ask


def build_prompt(*, date_str: str, total: int, open_count: int, resolved_count: int,
                  by_priority: dict[str, int], top_symptoms: list[tuple[str, int]]) -> str:
    priority_line = ", ".join(f"{p}: {n}" for p, n in sorted(by_priority.items())) or "не определён"
    symptom_line = ", ".join(f"{s} ({n})" for s, n in top_symptoms) or "нет"
    return (
        f"Сводка алертов дежурного инженера за {date_str}.\n"
        f"Всего адресовано этому сотруднику сегодня: {total}. Открыто сейчас: {open_count}, "
        f"устранено за день: {resolved_count}.\n"
        f"По приоритету: {priority_line}.\n"
        f"Частые типы: {symptom_line}.\n\n"
        f"Дай короткую сводку дня: 2-3 предложения, только факты из данных выше, "
        f"без домыслов и советов. Без списков и заголовков — сплошной текст."
    )


async def summarize_day(prompt: str) -> str | None:
    return await ask(prompt)
