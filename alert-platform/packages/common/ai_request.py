"""Запрос ИИ-разбора алерта по инициативе сотрудника (reply в TrueConf
со словом-триггером). Тот же узкий факт-контракт, что уже есть у
mark_acknowledged (packages/common/ack.py) — Python не решает, что
писать в разбор, только фиксирует, что его попросили сделать. Сам
разбор (сбор связанных алертов, вызов Ollama, отправка ответа) делает
Go delivery-planner (internal/planner/ai_analysis.go)."""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import select

from packages.models.db import AiAnalysisRequest, Notification

AI_HELP_TRIGGERS = {"ии", "ai", "анализ", "помощь", "разбор", "/анализ", "/ии"}


def wants_ai_help(text: str) -> bool:
    return text.strip().lower() in AI_HELP_TRIGGERS


def request_ai_analysis(session, reply_message_id: str, by_username: str) -> bool:
    """Ищет Notification(message_id=reply_message_id) — без фильтра по
    типу, разбор можно попросить и у SUPPLEMENT/CLOSURE, не только NEW.
    Если для этой Problem уже есть необработанный запрос — не плодит
    дубли (не спамим Ollama повторными тапами), но всё равно считает
    запрос принятым для ответа пользователю. Возвращает False только
    если уведомление не найдено вовсе."""
    notification = session.execute(
        select(Notification).where(Notification.message_id == reply_message_id)
    ).scalars().first()
    if notification is None:
        return False

    existing = session.execute(
        select(AiAnalysisRequest).where(
            AiAnalysisRequest.problem_id == notification.problem_id,
            AiAnalysisRequest.status == "pending",
        )
    ).scalars().first()
    if existing is not None:
        return True

    session.add(AiAnalysisRequest(
        problem_id=notification.problem_id,
        requested_by=by_username,
        reply_to_notification_id=notification.id,
        status="pending",
        created_at=datetime.utcnow(),
    ))
    session.commit()
    return True
