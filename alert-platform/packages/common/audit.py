"""Аудит действий пользователей и администраторов — раздел «Безопасность»
топ-балла кейса. Единая точка записи, чтобы формат строки не расходился
между вызывающими местами (личный кабинет, консоль источников, LDAP-вход)."""
from __future__ import annotations

from datetime import datetime

from packages.models.db import AuditLog


def log_action(session, *, actor: str, action: str, target: str | None = None,
                detail: str | None = None) -> None:
    session.add(AuditLog(actor=actor, action=action, target=target, detail=detail,
                          created_at=datetime.utcnow()))
    session.commit()
