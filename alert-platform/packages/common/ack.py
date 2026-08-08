"""Отслеживание реакции на инцидент — раздел «Сценарии», Этап 3. Полная
трёхвариантная семантика ответов из раздела 9.4 кейса («взял»/«не мой»/
«следствие») остаётся за рамками (M8/M9, отдельная более крупная задача)
— здесь только бинарный факт "кто-то ответил на NEW-уведомление",
которого достаточно для узла-развилки «Проверка реакции» в сценариях."""
from __future__ import annotations

from datetime import datetime

from sqlalchemy import select

from packages.models.db import Notification, Problem


def mark_acknowledged(session, reply_message_id: str, by_username: str) -> bool:
    """Ищет Notification(message_id=reply_message_id, type="NEW"). Если
    найден и Problem ещё не подтверждена — фиксирует acknowledged_at/by
    (первый ответ побеждает, повторные ответы ничего не переписывают) и
    возвращает True. Возвращает False, если уведомление не найдено или
    проблема уже была подтверждена ранее — решение, отвечать ли
    пользователю чем-то отличным, остаётся за вызывающей стороной."""
    notification = session.execute(
        select(Notification).where(Notification.message_id == reply_message_id,
                                    Notification.type == "NEW")
    ).scalars().first()
    if notification is None:
        return False

    problem = session.get(Problem, notification.problem_id)
    if problem is None or problem.acknowledged_at is not None:
        return False

    problem.acknowledged_at = datetime.utcnow()
    problem.acknowledged_by = by_username
    session.commit()
    return True
