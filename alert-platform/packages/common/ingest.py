"""Приём сигнала в WORM + очередь — раздел 5, стадия 0. Общая функция:
используется и настоящим шлюзом (services/gateway), и консолью запуска
демо-сценариев (services/api), чтобы не дублировать инвариант "запись в
WORM и очередь ДО подтверждения" в двух местах.
"""
from __future__ import annotations

import hashlib
from datetime import datetime

from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from packages.models.db import Signal, SignalQueueEntry


def ingest_raw(session: Session, *, source_system: str, source_instance: str, raw_body: str,
                external_id: str | None = None, received_at: datetime | None = None) -> tuple[int, str]:
    if "\x00" in raw_body:
        raise ValueError("raw_body содержит недопустимый NUL-байт")

    received_at = received_at or datetime.utcnow()
    body_hash = hashlib.sha256(raw_body.encode("utf-8")).hexdigest()

    signal = Signal(source_system=source_system, source_instance=source_instance,
                     external_id=external_id, received_at=received_at, raw_body=raw_body,
                     hash=body_hash)
    session.add(signal)
    try:
        session.commit()
    except IntegrityError:
        session.rollback()
        from sqlalchemy import select
        existing = session.execute(
            select(Signal).where(Signal.source_instance == source_instance, Signal.hash == body_hash)
        ).scalar_one()
        return existing.id, "duplicate"

    session.add(SignalQueueEntry(signal_id=signal.id, status="pending", enqueued_at=received_at))
    session.commit()
    return signal.id, "queued"
