"""Общий клиент локальной LLM (Ollama, раздел 1.4 — открытые веса, без
обращений к внешним API) — единая точка входа для всех ИИ-сценариев
платформы (саммаризация, семантическая нормализация, рекомендации).

Раздел И5/13: сбой или таймаут LLM никогда не бросает исключение наружу и
не блокирует конвейер — вызывающая сторона получает None и продолжает без
ИИ-компонента (для саммаризации это пропуск абзаца-гипотезы, для
классификации — event остаётся "unknown" как и до этой возможности, для
рекомендаций — сообщение уходит с фактами и без совета)."""
from __future__ import annotations

import os

import httpx

OLLAMA_URL = os.environ.get("OLLAMA_URL", "http://127.0.0.1:11434")
OLLAMA_MODEL = os.environ.get("OLLAMA_MODEL", "log-reader")
OLLAMA_TIMEOUT_S = float(os.environ.get("OLLAMA_TIMEOUT_S", "90"))


async def ask(prompt: str, *, timeout_s: float | None = None, num_predict: int = 300) -> str | None:
    try:
        async with httpx.AsyncClient(timeout=timeout_s or OLLAMA_TIMEOUT_S) as client:
            resp = await client.post(
                f"{OLLAMA_URL}/api/chat",
                json={
                    "model": OLLAMA_MODEL,
                    "messages": [{"role": "user", "content": prompt}],
                    "stream": False,
                    "think": False,
                    "keep_alive": "30m",
                    "options": {"num_predict": num_predict},
                },
            )
            resp.raise_for_status()
            content = resp.json().get("message", {}).get("content", "").strip()
            return content or None
    except Exception:  # noqa: BLE001 — раздел И5: сбой ИИ не должен ронять вызывающий сервис
        return None
