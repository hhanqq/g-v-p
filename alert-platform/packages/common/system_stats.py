"""Нагрузка сервера в реальном времени — раздел «Инфраструктура»/demo-
страница. ЦП читается напрямую из /proc внутри контейнера: на Linux/
Docker без дополнительной изоляции контейнер по умолчанию видит хостовый
/proc/stat и /proc/loadavg (проверено вживую перед тем, как полагаться на
это, а не предположено). ГПУ недоступен из контейнера без
nvidia-container-runtime — отдельный лёгкий HTTP-сервис на самом хосте
(gpu_stats_server.py, systemd-юнит gpu-stats.service), опрашиваемый через
host.docker.internal тем же приёмом, что и Ollama."""
from __future__ import annotations

import os
import time

import httpx

GPU_STATS_URL = os.environ.get("GPU_STATS_URL", "http://host.docker.internal:9101/")


def _read_cpu_times() -> tuple[int, int]:
    with open("/proc/stat") as f:
        parts = f.readline().split()[1:]
    values = [int(v) for v in parts]
    idle = values[3] + values[4]  # idle + iowait
    total = sum(values)
    return idle, total


def cpu_percent(sample_interval_s: float = 0.3) -> float | None:
    """Блокирующая (короткий sleep для дельты) — вызывать из потока
    (asyncio.to_thread), не напрямую из async-обработчика."""
    try:
        idle1, total1 = _read_cpu_times()
        time.sleep(sample_interval_s)
        idle2, total2 = _read_cpu_times()
        total_delta = total2 - total1
        if total_delta <= 0:
            return None
        return round(100 * (1 - (idle2 - idle1) / total_delta), 1)
    except Exception:  # noqa: BLE001 — раздел И5
        return None


def cpu_count() -> int:
    return os.cpu_count() or 1


def load_average() -> list[float] | None:
    try:
        with open("/proc/loadavg") as f:
            parts = f.read().split()
        return [float(parts[0]), float(parts[1]), float(parts[2])]
    except Exception:  # noqa: BLE001
        return None


async def gpu_stats() -> dict | None:
    try:
        async with httpx.AsyncClient(timeout=3.0) as client:
            resp = await client.get(GPU_STATS_URL)
            resp.raise_for_status()
            data = resp.json()
            return None if "error" in data else data
    except Exception:  # noqa: BLE001 — раздел И5: недоступность GPU-сервиса не роняет дашборд
        return None
