"""ИИ-сценарий «определение дублей между источниками» (раздел 4.1, кейс —
явный пункт списка сценариев ИИ: "определение дубликатов").

Точный `dedup_key = sha256(site|object_id|component|symptom_class)`
(раздел 4.1) ловит только буквальное совпадение симптома. Раздел 18.4
сценарий `duplicate_cross_system` специально устроен так, чтобы его НЕ
поймать: один и тот же коммутатор почти одновременно репортится
SolarWinds как `node_down` и Zabbix как `host_unreachable` — тот же
реальный объект, но разный symptom_class, то есть разный dedup_key.
Коррелятор (services/pipeline/correlator) тоже не подходит: его
топологическая ось ловит "объект X упал ИЗ-ЗА объекта Y" (родитель →
потомок), а не "один и тот же объект описан двумя словами".

Здесь LLM сравнивает ДОСЛОВНЫЕ исходные тексты (не пересказ, раздел И2)
двух кандидатов на одном object_id и решает: одно и то же реальное
событие или нет. Раздел И5: сбой ИИ = дубль не пойман, оба алерта уходят
отдельно — не хуже поведения без этой возможности."""
from __future__ import annotations

import asyncio

from packages.ai.client import ask

DEDUP_TIMEOUT_S = 20.0


def build_dedup_prompt(text_a: str, text_b: str) -> str:
    return (
        f"Два алерта системы мониторинга промышленного предприятия по одному и тому же "
        f"объекту инфраструктуры, пришли почти одновременно от РАЗНЫХ систем мониторинга.\n\n"
        f'Алерт 1:\n"""\n{text_a.strip()[:600]}\n"""\n\n'
        f'Алерт 2:\n"""\n{text_b.strip()[:600]}\n"""\n\n'
        f"Это одно и то же реальное событие, описанное двумя системами разными словами, "
        f"или два разных события на одном объекте? Ответь ТОЛЬКО одним словом: "
        f"same — если одно и то же событие, different — если разные."
    )


def _parse_verdict(reply: str | None) -> bool | None:
    if not reply:
        return None
    word = reply.strip().lower().split()[0].strip(".,!\"'")
    if word == "same":
        return True
    if word == "different":
        return False
    return None


async def is_duplicate(text_a: str, text_b: str) -> bool | None:
    reply = await ask(build_dedup_prompt(text_a, text_b), timeout_s=DEDUP_TIMEOUT_S, num_predict=5)
    return _parse_verdict(reply)


def is_duplicate_sync(text_a: str, text_b: str) -> bool | None:
    """Синхронная обёртка для pipeline-worker (нет своего event loop,
    как и у packages.ai.classify). Любая ошибка — None, трактуется как
    "не дубль" (раздел И5 — деградация, не блокировка конвейера)."""
    try:
        return asyncio.run(is_duplicate(text_a, text_b))
    except Exception:  # noqa: BLE001
        return None
