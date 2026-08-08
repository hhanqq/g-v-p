"""Тонкий runner синтетических сценариев для демонстрационного стенда.

Администрирование, HTML и аутентификация принадлежат Go admin-console.
Этот процесс сохраняет только Python datagen/Ollama utilities, которые не
участвуют в production-тракте реальных событий.
"""
from __future__ import annotations

import asyncio
import os
import random
from datetime import datetime, timedelta

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from datagen.generate import HERE as DATAGEN_DIR
from datagen.generate import load_scenario_defs, load_templates, load_yaml, render_event
from datagen.inventory_builder import build_inventory
from datagen.scenarios import SCENARIOS
from packages.ai import client as ai_client
from packages.ai.suggest_subscription import extract_recommended_subsidiary, suggest_subscription
from packages.common import system_stats
from packages.common.db import get_session
from packages.common.ingest import ingest_raw
from services.api import metrics

MAX_DEMO_WINDOW_S = 60.0
CMDB_SEED = int(os.environ.get("CMDB_SEED", "42"))
app = FastAPI(title="Диспетчер — demo runner")

_inventory = None
_templates = None
_scenario_defs = None


def inventory():
    global _inventory
    if _inventory is None:
        _inventory = build_inventory(load_yaml(DATAGEN_DIR / "inventory.yaml"), CMDB_SEED)
    return _inventory


def templates():
    global _templates
    if _templates is None:
        _templates = load_templates()
    return _templates


def scenario_defs():
    global _scenario_defs
    if _scenario_defs is None:
        _scenario_defs = load_scenario_defs()
    return _scenario_defs


class TriggerResult(BaseModel):
    scenario_id: str
    events_scheduled: int
    demo_duration_s: float


async def deliver_with_delays(records: list[tuple[float, dict]]) -> None:
    started = datetime.utcnow()
    for offset_s, record in records:
        remaining = (started + timedelta(seconds=offset_s) - datetime.utcnow()).total_seconds()
        if remaining > 0:
            await asyncio.sleep(remaining)
        with get_session() as session:
            try:
                ingest_raw(
                    session,
                    source_system=record["source"]["system"],
                    source_instance=record["source"]["instance"],
                    raw_body=record["raw_body"],
                )
            except ValueError:
                continue


@app.get("/api/demo/scenarios")
def list_demo_scenarios() -> dict:
    return {
        scenario_id: {"name": config["scenario"], "description": config["description"]}
        for scenario_id, config in scenario_defs().items()
    }


@app.post("/api/trigger/{scenario_id}", response_model=TriggerResult)
async def trigger_scenario(scenario_id: str) -> TriggerResult:
    if scenario_id not in SCENARIOS:
        raise HTTPException(404, f"неизвестный сценарий: {scenario_id}")
    inv = inventory()
    config = scenario_defs()[scenario_id]
    rnd = random.Random()
    anchor = datetime.utcnow()
    events = sorted(SCENARIOS[scenario_id](rnd, inv, config["params"], anchor), key=lambda event: event["ts"])
    if not events:
        return TriggerResult(scenario_id=scenario_id, events_scheduled=0, demo_duration_s=0.0)
    first = events[0]["ts"]
    max_offset = max((event["ts"] - first).total_seconds() for event in events) or 1.0
    scale = min(1.0, MAX_DEMO_WINDOW_S / max_offset)
    instances = {
        "zabbix": {code: f"zbx-{code}-01" for code in inv.sites},
        "solarwinds": {code: f"sw-mon-{code}-01" for code in inv.sites},
    }
    records = [
        ((event["ts"] - first).total_seconds() * scale, render_event(rnd, templates(), event, inv, instances))
        for event in events
    ]
    asyncio.create_task(deliver_with_delays(records))
    return TriggerResult(
        scenario_id=scenario_id,
        events_scheduled=len(records),
        demo_duration_s=round(max_offset * scale, 1),
    )


@app.get("/api/ai-selftest")
async def ai_selftest() -> dict:
    started = datetime.utcnow()
    prompt = "Одним предложением на русском: зачем нужен коррелятор алертов в системе мониторинга промышленного предприятия?"
    reply = await ai_client.ask(prompt, num_predict=100)
    return {
        "requested_at": started.strftime("%Y-%m-%d %H:%M:%S"),
        "elapsed_seconds": round((datetime.utcnow() - started).total_seconds(), 2),
        "model": ai_client.OLLAMA_MODEL,
        "prompt": prompt,
        "reply": reply,
        "success": reply is not None,
    }


@app.get("/api/ai-demo/{scenario}")
async def ai_demo(scenario: str) -> dict:
    if scenario == "suggest_subscription":
        with get_session() as session:
            stats = metrics.subsidiary_incident_stats(session)
        reply = await suggest_subscription(stats)
        recommended = extract_recommended_subsidiary(reply, stats) if stats else None
        return {"scenario": scenario, "stats": stats, "reply": reply, "recommended_subsidiary": recommended, "success": reply is not None}
    if scenario == "classify":
        from packages.ai.classify import classify_symptom

        sample = "PROBLEM: Filesystem /var is critically full (92% used)"
        reply = await classify_symptom(sample)
        return {"scenario": scenario, "sample_text": sample, "reply": reply, "success": reply is not None}
    raise HTTPException(404, f"неизвестный демо-сценарий: {scenario}")


@app.get("/api/system-stats")
async def live_system_stats() -> dict:
    cpu_task = asyncio.create_task(asyncio.to_thread(system_stats.cpu_percent))
    gpu_task = asyncio.create_task(system_stats.gpu_stats())
    return {
        "cpu_percent": await cpu_task,
        "cpu_count": system_stats.cpu_count(),
        "load_average": system_stats.load_average(),
        "gpu": await gpu_task,
    }
