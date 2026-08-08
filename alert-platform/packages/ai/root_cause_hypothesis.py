"""ИИ-сценарий «определение вероятной первопричины инцидента» (раздел 6.1,
явный пункт списка сценариев ИИ из кейса) — для случая, когда правило-
ориентированный коррелятор (services/pipeline/correlator) НЕ смог связать
два события причинно-следственной связью. Его топологическая ось ловит
только «объект X упал ИЗ-ЗА объекта Y» (родитель→потомок по CMDB);
раздел 18.4, сценарий `site_outage_ambiguous`, намеренно сконструирован
так, чтобы этого не поймать: core-switch (node_down) и питание хоста
(power_lost) на одной площадке падают почти синхронно — разные объекты,
разные классы симптома, ни одно правило их не связывает, оба остаются
независимыми Problem без общего Incident.

Здесь LLM сравнивает ДОСЛОВНЫЕ исходные тексты обоих кандидатов (раздел
И2) и высказывает гипотезу, какой вероятнее первопричина. Раздел 13:
явно маркируется как предположение, не меняет структуру Incident/Problem
— только дополняет текст NEW-уведомления. Раздел И5: сбой ИИ — гипотезы
просто нет, оба уведомления уходят как обычно, раздельно."""
from __future__ import annotations

import asyncio

from packages.ai.client import ask

HYPOTHESIS_TIMEOUT_S = 20.0


def build_hypothesis_prompt(*, site: str, candidate_a_text: str, candidate_b_text: str) -> str:
    return (
        f"На площадке {site} промышленного предприятия почти одновременно зафиксированы "
        f"два независимых алерта, и правило автоматической корреляции не смогло однозначно "
        f"связать их причинно-следственной связью:\n\n"
        f'Событие А:\n"""\n{candidate_a_text.strip()[:600]}\n"""\n\n'
        f'Событие Б:\n"""\n{candidate_b_text.strip()[:600]}\n"""\n\n'
        f"Какое из двух вероятнее является первопричиной, а какое — следствием? "
        f"Ответь 2-3 предложениями, используя ТОЛЬКО факты из текстов выше, без домыслов, "
        f"и явно укажи, что это предположение, требующее проверки инженером, а не установленный факт."
    )


async def suggest_root_cause(*, site: str, candidate_a_text: str, candidate_b_text: str) -> str | None:
    prompt = build_hypothesis_prompt(site=site, candidate_a_text=candidate_a_text,
                                      candidate_b_text=candidate_b_text)
    return await ask(prompt, timeout_s=HYPOTHESIS_TIMEOUT_S, num_predict=200)


def suggest_root_cause_sync(*, site: str, candidate_a_text: str, candidate_b_text: str) -> str | None:
    """Синхронная обёртка для pipeline-worker — тот же паттерн, что и у
    classify.py/dedup.py (воркер без своего event loop, раздел 15.3)."""
    try:
        return asyncio.run(suggest_root_cause(site=site, candidate_a_text=candidate_a_text,
                                               candidate_b_text=candidate_b_text))
    except Exception:  # noqa: BLE001 — раздел И5
        return None
