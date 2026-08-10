"""Аудит действий пользователей и администраторов — раздел «Безопасность»
топ-балла кейса. Единая точка записи, чтобы формат строки не расходился
между вызывающими местами (личный кабинет, консоль источников, LDAP-вход).

Каждый вызов также пишет строку в change_events (история изменений,
0011_change_events.sql) — тот же факт, но со структурированным
resource_type/resource_id вместо свободного target-текста, чтобы карта
объекта/сотрудника и low-code поиск видели и Python-, и Go-мутации
одинаково."""
from __future__ import annotations

from datetime import datetime

from packages.models.db import AuditLog, ChangeEvent


def log_action(session, *, actor: str, action: str, target: str | None = None,
                detail: str | None = None, resource_type: str | None = None,
                resource_id: str | None = None, actor_role: str = "admin") -> None:
    now = datetime.utcnow()
    session.add(AuditLog(actor=actor, action=action, target=target, detail=detail,
                          created_at=now))
    session.add(ChangeEvent(
        occurred_at=now, actor=actor, actor_role=actor_role, action=action,
        resource_type=resource_type or "misc", resource_id=resource_id or target or "unknown",
        detail=detail, result="success",
    ))
    session.commit()
