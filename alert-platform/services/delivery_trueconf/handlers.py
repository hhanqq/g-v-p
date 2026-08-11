"""Входящие TrueConf-команды, зависящие от vendor event types."""
from __future__ import annotations

import os
import secrets
from collections import Counter
from datetime import datetime, timedelta

from sqlalchemy import select
from trueconf import Router
from trueconf.enums.parse_mode import ParseMode
from trueconf.types import Message

from packages.ai.daily_summary import build_prompt as build_daily_summary_prompt
from packages.ai.daily_summary import summarize_day
from packages.common.ack import mark_acknowledged
from packages.common.ai_request import request_ai_analysis, wants_ai_help
from packages.common.db import get_session
from packages.common.routing import resolve_recipients
from packages.models.db import Problem, Subscriber
from services.delivery_trueconf.templates import render_daily_summary


PUBLIC_CONSOLE_URL = os.environ.get(
    "TRUECONF_PUBLIC_CONSOLE_URL", "https://xn--80aebrvrg.xn--p1acf/console"
)

router = Router()


def _get_or_create_subscriber_with_token(session, username: str) -> Subscriber:
    subscriber = session.execute(
        select(Subscriber).where(Subscriber.trueconf_username == username)
    ).scalars().first()
    if subscriber is None:
        subscriber = Subscriber(
            trueconf_username=username,
            access_token=secrets.token_urlsafe(24),
            active=True,
            created_at=datetime.utcnow(),
        )
        session.add(subscriber)
        session.commit()
    return subscriber


def _daily_summary_facts(session, username: str, now: datetime) -> dict:
    day_start = datetime(now.year, now.month, now.day)
    day_end = day_start + timedelta(days=1)
    todays = session.execute(
        select(Problem).where(Problem.opened_at >= day_start, Problem.opened_at < day_end)
    ).scalars().all()
    mine = [problem for problem in todays if username in resolve_recipients(session, problem)]
    open_count = sum(problem.status in ("OPEN", "FLAPPING") for problem in mine)
    return {
        "date_str": day_start.strftime("%Y-%m-%d"), "total": len(mine),
        "open_count": open_count, "resolved_count": len(mine) - open_count,
        "by_priority": dict(Counter(problem.priority or "?" for problem in mine)),
        "top_symptoms": Counter(problem.symptom_class for problem in mine).most_common(5),
    }


@router.message()
async def on_any_message(message: Message) -> None:
    username = message.author.id.split("@", 1)[0]
    if message.reply_message_id:
        reply_text = message.text or ""
        with get_session() as session:
            acknowledged = mark_acknowledged(session, message.reply_message_id, username)
            ai_queued = (
                request_ai_analysis(session, message.reply_message_id, username)
                if wants_ai_help(reply_text) else False
            )
        if ai_queued:
            await message.answer(
                "Принял в анализ, разбираюсь — свяжу с историей связанных алертов и "
                "пришлю сообщением ниже (обычно 10-30 секунд)."
            )
        elif acknowledged:
            await message.answer("Принято, отмечено как реакция на инцидент.")
        return

    text = (message.text or "").strip().lower()
    if text in ("/старт", "/start"):
        await message.answer(
            "Бот «ADP» на связи. Присылаю уведомления NEW/CLOSURE по проблемам "
            "и инцидентам.\nЧтобы настроить, какие уведомления вам приходят — команда "
            "/кабинет.\nТекущие алерты — /алерты, дневная сводка — /сводка.\n"
            "Ответ на NEW отмечается как реакция на инцидент; ответьте словом "
            "«анализ» на любое уведомление, чтобы попросить ИИ разобрать этот "
            "алерт и связанные с ним."
        )
    elif text in ("/кабинет", "/подписки", "/subscriptions"):
        with get_session() as session:
            subscriber = _get_or_create_subscriber_with_token(session, username)
            link = f"{PUBLIC_CONSOLE_URL}/me/{username}/?token={subscriber.access_token}"
        await message.answer(
            f"Ваш личный кабинет подписок (раздел 8): {link}\n"
            f"Ссылка привязана к вашему TrueConf-логину — не пересылайте её."
        )
    elif text in ("/алерты", "/текущие", "/alerts"):
        with get_session() as session:
            subscriber = _get_or_create_subscriber_with_token(session, username)
            link = f"{PUBLIC_CONSOLE_URL}/alerts/{username}/?token={subscriber.access_token}"
        await message.answer(
            f"Ваши текущие алерты и их последовательность: {link}\n"
            "Ссылка привязана к вашему TrueConf-логину — не пересылайте её."
        )
    elif text in ("/сводка", "/итоги", "/summary"):
        with get_session() as session:
            facts = _daily_summary_facts(session, username, datetime.utcnow())
        ai_text = None
        if facts["total"] > 0:
            prompt = build_daily_summary_prompt(**facts)
            ai_text = await summarize_day(prompt)
        answer = render_daily_summary(**facts, ai_text=ai_text)
        await message.answer(answer, parse_mode=ParseMode.HTML)
    else:
        await message.answer("Команды: /старт, /кабинет, /алерты, /сводка")


async def on_health_check(status: dict) -> None:
    print(f"delivery_trueconf: health_check {status}")
