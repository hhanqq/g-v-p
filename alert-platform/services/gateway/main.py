"""Шлюз приёма — раздел 5, стадия 0.

Аутентификация/HMAC/allow-list (раздел 14.3) здесь не реализованы — это
осознанный вырез объёма для текущего этапа (M1), не забытая деталь.

Контракт стадии выполняется дословно: сигнал сначала пишется в WORM и
публикуется в очередь, подтверждение источнику уходит только после этого
(раздел 15.3, RPO 0). Парсинг здесь не делается — его выполняет
pipeline-worker асинхронно, поэтому отказ конвейера не теряет события
(раздел 5, стадия 0: "события копятся, источник не теряет данные").
"""
from __future__ import annotations

from fastapi import FastAPI, HTTPException
from sqlalchemy import func, select

from packages.common.db import engine, get_session
from packages.common.ingest import ingest_raw as _ingest_raw
from packages.models.db import Base, SignalQueueEntry

from .schemas import HealthResponse, IngestAck, RawIngestRequest

app = FastAPI(title="Диспетчер — шлюз приёма")

Base.metadata.create_all(bind=engine)  # M0: без Alembic — таблицы по моделям при старте


@app.post("/api/v1/ingest/raw", response_model=IngestAck, status_code=202)
def ingest_raw(payload: RawIngestRequest) -> IngestAck:
    with get_session() as session:
        try:
            signal_id, status = _ingest_raw(
                session, source_system=payload.source_system, source_instance=payload.source_instance,
                raw_body=payload.raw_body, external_id=payload.external_id,
            )
        except ValueError as exc:
            # Postgres text не хранит NUL-байт — отказ явно, а не падение в 500.
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        return IngestAck(signal_id=signal_id, status=status)


@app.get("/api/v1/health", response_model=HealthResponse)
def health() -> HealthResponse:
    try:
        with get_session() as session:
            session.execute(select(1))
            queue_depth = session.execute(
                select(func.count()).select_from(SignalQueueEntry).where(SignalQueueEntry.status == "pending")
            ).scalar_one()
        return HealthResponse(status="ok", db="ok", queue_depth=queue_depth)
    except Exception as exc:  # noqa: BLE001 — health-эндпоинт не должен падать со стектрейсом
        raise HTTPException(status_code=503, detail=f"db unavailable: {exc}") from exc
