import asyncio
from datetime import datetime
from types import SimpleNamespace

from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from packages.models.db import Base, DeliveryOutbox, Notification, Problem
from services.delivery_trueconf import outbox as adapter


def _database():
    engine = create_engine(
        "sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool
    )
    Base.metadata.create_all(engine)
    return sessionmaker(bind=engine)


def _problem(session) -> Problem:
    now = datetime.utcnow()
    problem = Problem(
        dedup_key="key",
        status="OPEN",
        symptom_class="host_unreachable",
        opened_at=now,
        last_seen_at=now,
    )
    session.add(problem)
    session.flush()
    return problem


def _enqueue(session, notification, recipient="ivanov", text="alert"):
    session.add(notification)
    session.flush()
    command = DeliveryOutbox(
        notification_id=notification.id,
        contract_version=1,
        channel="trueconf",
        idempotency_key=f"trueconf:notification:{notification.id}",
        recipient=recipient,
        text=text,
        parse_mode="HTML",
        status="pending",
        attempts=0,
        available_at=datetime.utcnow(),
        created_at=datetime.utcnow(),
    )
    session.add(command)
    return command


def test_notification_and_outbox_are_created_together():
    sessions = _database()
    with sessions() as session:
        problem = _problem(session)
        notification = Notification(
            problem_id=problem.id,
            type="NEW",
            chat_id="recipient:ivanov",
            status="queued",
            created_at=datetime.utcnow(),
        )
        command = _enqueue(session, notification)
        session.commit()

        assert command.notification_id == notification.id
        assert command.idempotency_key == f"trueconf:notification:{notification.id}"
        assert command.status == "pending"
        assert command.contract_version == 1


def test_trueconf_adapter_only_uses_outbox_contract(monkeypatch):
    sessions = _database()
    with sessions() as session:
        problem = _problem(session)
        notification = Notification(
            problem_id=problem.id,
            type="NEW",
            chat_id="recipient:ivanov",
            status="queued",
            created_at=datetime.utcnow(),
        )
        command = _enqueue(session, notification)
        command.status = "processing"
        command.attempts = 1
        session.commit()
        command_id = command.id
        notification_id = notification.id

    monkeypatch.setattr(adapter, "get_session", sessions)
    monkeypatch.setattr(adapter, "_parse_mode", lambda value: value)
    adapter._chat_cache.clear()

    class FakeBot:
        def __init__(self):
            self.sent = []

        async def create_personal_chat(self, *, user_id):
            assert user_id == "ivanov@corp.local"
            return SimpleNamespace(chat_id="chat-1")

        async def send_message(self, **kwargs):
            self.sent.append(kwargs)
            return SimpleNamespace(message_id="message-1")

    bot = FakeBot()
    asyncio.run(adapter.deliver_one(bot, "corp.local", command_id))

    with sessions() as session:
        command = session.get(DeliveryOutbox, command_id)
        notification = session.get(Notification, notification_id)
        assert command.status == "sent"
        assert command.provider_chat_id == "chat-1"
        assert notification.status == "sent"
        assert notification.chat_id == "chat-1"
        assert notification.message_id == "message-1"
    assert bot.sent[0]["text"] == "alert"


def test_record_write_failure_after_send_does_not_crash_or_retry(monkeypatch):
    """Живой инцидент 2026-08-08: сообщение реально ушло получателю через
    TrueConf, но запись notification упала на уникальном ограничении
    (гонка — та же Problem уже была уведомлена другим путём). Раньше это
    необработанное исключение роняло весь consumer и оставляло команду в
    processing — на следующем TTL-sweep она переотправлялась бы получателю
    ПОВТОРНО. Теперь: не падаем, не повторяем отправку, команда терминально
    помечается sent."""
    sessions = _database()
    with sessions() as session:
        problem = _problem(session)
        # Уже существующее "отправлено" уведомление — та же (problem, type,
        # chat_id) тройка, на которую наткнётся вторая команда.
        existing = Notification(
            problem_id=problem.id, type="NEW", chat_id="chat-1",
            status="sent", created_at=datetime.utcnow(),
        )
        session.add(existing)

        notification = Notification(
            problem_id=problem.id, type="NEW", chat_id="recipient:ivanov",
            status="queued", created_at=datetime.utcnow(),
        )
        command = _enqueue(session, notification)
        command.status = "processing"
        command.attempts = 1
        session.commit()
        command_id = command.id

    monkeypatch.setattr(adapter, "get_session", sessions)
    monkeypatch.setattr(adapter, "_parse_mode", lambda value: value)
    adapter._chat_cache.clear()

    class FakeBot:
        sent = []

        async def create_personal_chat(self, *, user_id):
            return SimpleNamespace(chat_id="chat-1")  # тот же chat_id, что и у existing

        async def send_message(self, **kwargs):
            FakeBot.sent.append(kwargs)  # сообщение реально "ушло"
            return SimpleNamespace(message_id="message-2")

    # Не должно бросить исключение наружу.
    asyncio.run(adapter.deliver_one(FakeBot(), "corp.local", command_id))

    assert len(FakeBot.sent) == 1  # отправка была ровно одна — не задвоилась

    with sessions() as session:
        command = session.get(DeliveryOutbox, command_id)
        assert command.status == "sent"  # не "processing" и не "pending" — TTL-sweep не переотправит
        assert command.provider_chat_id == "chat-1"
