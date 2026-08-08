"""ИИ-сценарий «умная маршрутизация на основе истории» (раздел 5, раздел 8
— личный кабинет; кейс явно называет этот сценарий в списке). Подписчик
без настроенных фильтров (или зашедший впервые) видит подсказку: на какой
филиал исторически стоит подписаться в первую очередь — по РЕАЛЬНОЙ
частоте и критичности прошлых Problem (services/api/metrics.py), не по
догадке. LLM только выбирает и коротко объясняет топ-кандидата из уже
посчитанных чисел — раздел 13: ничего не добавляет от себя. Раздел И5:
сбой ИИ — кабинет просто не показывает подсказку, ручная подписка не
затронута."""
from __future__ import annotations

from packages.ai.client import ask


def build_suggestion_prompt(stats: list[tuple[str, int, int]]) -> str | None:
    """stats: [(subsidiary, total_problems, critical_count), ...], по убыванию total."""
    if not stats:
        return None
    lines = "\n".join(
        f"- {subsidiary}: проблем всего {total}, из них P0/P1: {critical}"
        for subsidiary, total, critical in stats
    )
    return (
        f"Статистика проблем по филиалам за историю работы платформы мониторинга:\n{lines}\n\n"
        f"Порекомендуй дежурному инженеру, на какой ОДИН филиал из списка выше стоит "
        f"подписаться в первую очередь. Начни ответ с точного названия этого филиала "
        f"(как оно написано в статистике), затем коротко (1-2 предложения, без списка) "
        f"объясни почему — используй ТОЛЬКО цифры из статистики выше, ничего не добавляй "
        f"от себя и не упоминай филиалы, которых нет в списке."
    )


async def suggest_subscription(stats: list[tuple[str, int, int]]) -> str | None:
    prompt = build_suggestion_prompt(stats)
    if prompt is None:
        return None
    return await ask(prompt, num_predict=150)


def extract_recommended_subsidiary(ai_text: str | None, stats: list[tuple[str, int, int]]) -> str | None:
    """Промпт просит модель НАЧАТЬ ответ с точного названия филиала —
    но модель вольна выбрать другой критерий важности, чем "просто
    больше всего проблем" (например, приоритет критичности важнее
    объёма). Кнопка быстрой подписки обязана вести на ТО, что реально
    порекомендовал текст, а не на отдельно посчитанный топ-1 по total —
    иначе текст и кнопка расходятся (живой баг, пойманный при проверке:
    текст рекомендовал один филиал, кнопка вела на другой). Если распознать
    не удалось — откатываемся на топ-1 по total, не оставляем пусто."""
    valid = {subsidiary for subsidiary, _, _ in stats}
    if ai_text:
        candidate = ai_text.strip().split()[0].strip(".,:;!\"'") if ai_text.strip() else ""
        if candidate in valid:
            return candidate
    return stats[0][0] if stats else None
