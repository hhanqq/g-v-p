"""Сессионная LDAP-авторизация для SPA (платформа, Этап 1) — параллельно
уже существующему `require_admin` в services/api/main.py (HTTP Basic,
используется старыми server-rendered страницами — не трогаем, оставляем
рабочим как есть). Cookie-сессия удобнее для одностраничного React-
приложения: логин один раз, а не Basic Auth на каждый XHR-запрос.

Тот же LDAP-бэкенд (packages/common/ldap_auth.py) — один источник истины
про пароли и роли для обоих механизмов входа."""
from __future__ import annotations

from datetime import datetime

from fastapi import HTTPException, Request

from packages.common import ldap_auth
from packages.common.audit import log_action
from packages.common.db import get_session


def login(request: Request, username: str, password: str) -> dict:
    authenticated, is_admin = ldap_auth.authenticate(username, password)
    if not authenticated:
        with get_session() as session:
            log_action(session, actor=username or "?", action="session_login_failed")
        raise HTTPException(401, "Неверный логин или пароль LDAP")
    request.session["username"] = username
    request.session["is_admin"] = is_admin
    request.session["logged_in_at"] = datetime.utcnow().isoformat()
    return {"username": username, "is_admin": is_admin}


def logout(request: Request) -> None:
    request.session.clear()


def current_user(request: Request) -> dict | None:
    username = request.session.get("username")
    if not username:
        return None
    return {"username": username, "is_admin": bool(request.session.get("is_admin", False))}


def require_session_user(request: Request) -> dict:
    user = current_user(request)
    if user is None:
        raise HTTPException(401, "Требуется вход")
    return user


def require_session_admin(request: Request) -> dict:
    user = require_session_user(request)
    if not user["is_admin"]:
        raise HTTPException(403, "Требуется роль администратора платформы (группа admins в LDAP)")
    return user
