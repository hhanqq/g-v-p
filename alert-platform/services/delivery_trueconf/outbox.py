"""Тонкий исходящий адаптер TrueConf.

Модуль знает только контракт delivery_outbox и API TrueConf. Здесь нет
Problem, Incident, CMDB, маршрутизации, шаблонов или LLM.
"""
from __future__ import annotations

import asyncio
import os
from datetime import datetime, timedelta
from typing import TYPE_CHECKING, Any

from sqlalchemy import select, update

from packages.common.db import get_session
from packages.models.db import DeliveryOutbox, Notification

if TYPE_CHECKING:
    from trueconf import Bot


BATCH_SIZE = int(os.environ.get("DELIVERY_BATCH_SIZE", "20"))
MAX_ATTEMPTS = int(os.environ.get("DELIVERY_MAX_ATTEMPTS", "8"))
POLL_INTERVAL_S = float(os.environ.get("DELIVERY_POLL_INTERVAL_S", "1"))
STUCK_TIMEOUT_S = int(os.environ.get("DELIVERY_STUCK_TIMEOUT_S", "120"))

_chat_cache: dict[str, str] = {}


def claim_batch() -> list[int]:
    now = datetime.utcnow()
    with get_session() as session:
        session.execute(
            update(DeliveryOutbox)
            .where(
                DeliveryOutbox.status == "processing",
                DeliveryOutbox.claimed_at < now - timedelta(seconds=STUCK_TIMEOUT_S),
            )
            .values(status="pending", claimed_at=None)
        )
        rows = session.execute(
            select(DeliveryOutbox)
            .where(
                DeliveryOutbox.channel == "trueconf",
                DeliveryOutbox.status == "pending",
                DeliveryOutbox.available_at <= now,
            )
            .order_by(DeliveryOutbox.id)
            .limit(BATCH_SIZE)
            .with_for_update(skip_locked=True)
        ).scalars().all()
        ids = []
        for row in rows:
            row.status = "processing"
            row.claimed_at = now
            row.attempts += 1
            ids.append(row.id)
        session.commit()
        return ids


async def _chat_id(bot: "Bot", recipient: str, domain: str) -> str:
    if recipient not in _chat_cache:
        chat = await bot.create_personal_chat(user_id=f"{recipient}@{domain}")
        _chat_cache[recipient] = chat.chat_id
    return _chat_cache[recipient]


def _parse_mode(value: str) -> Any:
    if value == "HTML":
        from trueconf.enums.parse_mode import ParseMode
        return ParseMode.HTML
    # Текущий контракт платформы использует HTML. Неизвестный режим
    # отклоняем явно, чтобы не отправить разметку как обычный текст.
    raise ValueError(f"unsupported TrueConf parse mode: {value}")


async def deliver_one(bot: "Bot", domain: str, command_id: int) -> None:
    with get_session() as session:
        command = session.get(DeliveryOutbox, command_id)
        if command is None or command.status != "processing":
            return
        notification = session.get(Notification, command.notification_id)
        reply_message_id = None
        if command.reply_to_notification_id is not None:
            parent = session.get(Notification, command.reply_to_notification_id)
            if parent is None or not parent.message_id:
                _retry(session, command, notification, "reply target has not been delivered yet")
                return

        try:
            chat_id = command.provider_chat_id
            if not chat_id:
                chat_id = await _chat_id(bot, command.recipient or "", domain)
            if command.reply_to_notification_id is not None:
                parent = session.get(Notification, command.reply_to_notification_id)
                reply_message_id = parent.message_id
            response = await bot.send_message(
                chat_id=chat_id,
                text=command.text,
                parse_mode=_parse_mode(command.parse_mode),
                reply_message_id=reply_message_id,
            )
        except Exception as exc:  # noqa: BLE001 - ошибка канала не роняет consumer
            _retry(session, command, notification, str(exc))
            return

        now = datetime.utcnow()
        command.status = "sent"
        command.sent_at = now
        command.claimed_at = None
        command.provider_chat_id = chat_id
        command.last_error = None
        # Для автоматизаций chat_id содержит доменный ключ шага сценария.
        # Реальный provider chat хранится в outbox; замена ключа сломала бы
        # возможность нескольких уведомлений одного сценария в тот же чат.
        if notification.type not in ("SCENARIO", "SLA_BREACH"):
            notification.chat_id = chat_id
        notification.message_id = response.message_id
        notification.status = "sent"
        notification.sent_at = now
        notification.error = None
        session.commit()


def _retry(session, command: DeliveryOutbox, notification: Notification, error: str) -> None:
    command.last_error = error[:2000]
    command.claimed_at = None
    notification.status = "failed"
    notification.error = error[:2000]
    if command.attempts >= MAX_ATTEMPTS:
        command.status = "failed"
    else:
        command.status = "pending"
        delay_s = min(2 ** command.attempts, 60)
        command.available_at = datetime.utcnow() + timedelta(seconds=delay_s)
    session.commit()


async def delivery_loop(bot: "Bot", domain: str) -> None:
    while True:
        command_ids = claim_batch()
        for command_id in command_ids:
            await deliver_one(bot, domain, command_id)
        await asyncio.sleep(POLL_INTERVAL_S)
