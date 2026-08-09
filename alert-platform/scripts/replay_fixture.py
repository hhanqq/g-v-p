"""Реплей JSONL-фикстуры (datagen) в реальный шлюз — наполняет БД
исторической историей алертов. Не тест-хелпер, ops-инструмент: raw_body
уходит как есть, occurred_at парсер вытащит из самого текста сообщения
(раздел 6.6, services/pipeline/parser/engine.py), поэтому реплей
"сегодня" корректно создаёт события с историческими датами, встроенными
в фикстуру datagen'ом.

CMDB должна быть засеяна ТЕМ ЖЕ --seed, что и фикстура (scripts/seed_demo.py)
— иначе object_id в сообщениях не совпадут с CmdbObject и всё уйдёт в
карантин резолвера.

Пример:
    python3 scripts/replay_fixture.py fixtures/demo_history_extra.jsonl \
        --url https://xn--80aebrvrg.xn--p1acf/ingest/api/v1/ingest/raw --concurrency 25
"""
from __future__ import annotations

import argparse
import asyncio
import json

import httpx


async def _post_one(client: httpx.AsyncClient, url: str, sem: asyncio.Semaphore, record: dict,
                     token_map: dict[str, str]) -> str:
    instance = record["source"]["instance"]
    payload = {
        "source_system": record["source"]["system"],
        "source_instance": instance,
        "raw_body": record["raw_body"],
    }
    headers = {}
    if instance in token_map:
        headers["X-Source-Token"] = token_map[instance]
    async with sem:
        try:
            resp = await client.post(url, json=payload, headers=headers, timeout=30)
            resp.raise_for_status()
            return resp.json()["status"]
        except Exception as exc:  # noqa: BLE001 — ops-скрипт, считаем и продолжаем, не падаем на одной ошибке
            return f"error: {exc}"


async def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("path", help="Путь к JSONL-фикстуре (datagen.generate --out ...)")
    parser.add_argument("--url", required=True, help="Полный URL POST /api/v1/ingest/raw")
    parser.add_argument("--concurrency", type=int, default=25)
    parser.add_argument("--token-map", help="JSON-файл {instance: X-Source-Token} для источников, "
                                             "зарегистрированных с токеном (раздел «Источники»); "
                                             "источники без записи в файле отправляются как раньше, без заголовка")
    args = parser.parse_args()

    with open(args.path, encoding="utf-8") as f:
        records = [json.loads(line) for line in f]

    token_map: dict[str, str] = {}
    if args.token_map:
        with open(args.token_map, encoding="utf-8") as f:
            token_map = json.load(f)

    sem = asyncio.Semaphore(args.concurrency)
    counts: dict[str, int] = {}
    async with httpx.AsyncClient() as client:
        tasks = [asyncio.create_task(_post_one(client, args.url, sem, r, token_map)) for r in records]
        for i, coro in enumerate(asyncio.as_completed(tasks), 1):
            status = await coro
            counts[status] = counts.get(status, 0) + 1
            if i % 1000 == 0 or i == len(records):
                print(f"{i}/{len(records)} — {counts}", flush=True)

    print(f"Готово: {len(records)} сообщений — {counts}")


if __name__ == "__main__":
    asyncio.run(main())
