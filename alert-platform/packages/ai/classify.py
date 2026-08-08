"""ИИ-сценарий «семантическая нормализация событий» (раздел 5, раздел 20
п.5 списка сценариев из кейса). Регэксп-парсер (services/pipeline/parser)
намеренно НЕ пытается угадывать формулировки — раздел 5 явно требует
деградации в symptom_class="unknown", а не хрупких эвристик. Но
"unknown"-событие всё равно должно куда-то попасть: сейчас оно просто не
участвует в свёртке/корреляции (раздел 4.3), то есть теряет часть пользы
конвейера. Здесь — best-effort попытка распознать класс по смыслу текста,
СТРОГО из того же закрытого списка классов, что знают корреляция и
приоритизация: если LLM не уверена, результат остаётся "unknown", как и
раньше — регресса относительно допараллельного поведения нет (раздел И5).

Используется из pipeline-worker (services/pipeline/worker.py) синхронно,
но с КОРОТКИМ таймаутом (в разы меньше, чем у саммаризации в
delivery_trueconf) — событие идёт по конвейеру одно за другим в
process_batch, и медленная ИИ-классификация не должна ощутимо тормозить
приём остального потока (раздел 15 — тысячи событий в сутки)."""
from __future__ import annotations

import asyncio

from packages.ai.client import ask

# Тот же список, что и в connectors/*.yaml symptom_rules — раздел 4.1.
KNOWN_SYMPTOM_CLASSES = [
    "host_unreachable", "node_down", "interface_down",
    "disk_space", "service_down", "power_lost",
]

# 8с оказалось мало на практике (2026-08-07): "холодный" вызов модели (не
# было запросов в течение keep_alive-окна) занял ~12.5с, из которых ~10с —
# именно load_duration модели, а не сама генерация. 20с даёт запас и всё
# ещё на порядок меньше 90с таймаута саммаризации (там события редки и
# фоновые, здесь — потенциально в потоке, поэтому бюджет держим отдельным).
CLASSIFY_TIMEOUT_S = 20.0


def build_classify_prompt(raw_text: str) -> str:
    classes = ", ".join(KNOWN_SYMPTOM_CLASSES)
    snippet = raw_text.strip()[:1000]
    return (
        f"Текст алерта системы мониторинга промышленного предприятия:\n"
        f'"""\n{snippet}\n"""\n\n'
        f"Определи класс симптома СТРОГО из списка: {classes}.\n"
        f"Если ни один класс уверенно не подходит по смыслу — ответь ровно одним словом: unknown.\n"
        f"Ответь ТОЛЬКО одним словом из списка, без пояснений и знаков препинания."
    )


def _parse_classification(reply: str | None) -> str | None:
    if not reply:
        return None
    candidate = reply.strip().lower().split()[0].strip(".,!\"'")
    return candidate if candidate in KNOWN_SYMPTOM_CLASSES else None


async def classify_symptom(raw_text: str) -> str | None:
    reply = await ask(build_classify_prompt(raw_text), timeout_s=CLASSIFY_TIMEOUT_S, num_predict=10)
    return _parse_classification(reply)


def classify_symptom_sync(raw_text: str) -> str | None:
    """Синхронная обёртка для pipeline-worker (раздел 15.3 — воркер без
    своего event loop). Любая ошибка (включая таймаут) — None, не
    исключение: вызывающая сторона обязана трактовать это как "остаёмся
    unknown", а не как сбой обработки события."""
    try:
        return asyncio.run(classify_symptom(raw_text))
    except Exception:  # noqa: BLE001 — раздел И5
        return None
