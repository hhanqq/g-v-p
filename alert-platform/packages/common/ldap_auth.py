"""Раздел «Безопасность» кейса — авторизация через LDAP/AD и разграничение
доступа по ролям. Настоящий LDAP-протокол (bind + search), не имитация в
коде: тестовый каталог glauth (docker-compose сервис `ldap`,
`ldap/glauth.cfg`) с синтетическими пользователями/группами — реальной
корпоративной AD у вымышленной компании нет и быть не может (раздел 18.8).

Схема каталога (проверено вживую через ldap3, не угадано):
  cn=<uid>,ou=<группа>,ou=users,<baseDN>   — пользователи
  ou=admins,ou=groups,<baseDN>              — роль администратора платформы
  ou=employees,ou=groups,<baseDN>           — обычные сотрудники
Атрибут `memberOf` на записи пользователя содержит DN его группы.

Двухшажная проверка — так же устроена любая реальная интеграция с
LDAP/AD: обычное приложение не имеет прав искать каталог от имени
случайного пользователя, поэтому DN и членство в группе ищет отдельная
служебная учётная запись (`svc-search`), а bind под ИМЕННО этим DN и
паролем пользователя — единственный момент, где проверяется пароль.
Платформа пароль пользователя нигде не хранит."""
from __future__ import annotations

import os

from ldap3 import Connection, Server
from ldap3.utils.conv import escape_filter_chars

LDAP_URL = os.environ.get("LDAP_URL", "ldap://ldap:389")
LDAP_BASE_DN = os.environ.get("LDAP_BASE_DN", "dc=gpn-dispatcher,dc=local")
LDAP_SEARCH_USER = os.environ.get("LDAP_SEARCH_USER", "svc-search")
LDAP_SEARCH_PASSWORD = os.environ.get("LDAP_SEARCH_PASSWORD", "svc123")
ADMIN_GROUP_DN = f"ou=admins,ou=groups,{LDAP_BASE_DN}"

_server: Server | None = None


def _get_server() -> Server:
    global _server
    if _server is None:
        host, _, port = LDAP_URL.replace("ldap://", "").partition(":")
        _server = Server(host, port=int(port or 389))
    return _server


def _service_connection() -> Connection | None:
    conn = Connection(_get_server(), user=f"cn={LDAP_SEARCH_USER},ou=employees,ou=users,{LDAP_BASE_DN}",
                       password=LDAP_SEARCH_PASSWORD)
    return conn if conn.bind() else None


def _find_user(username: str) -> tuple[str, list[str]] | None:
    """(dn, member_of) для найденного в каталоге пользователя, иначе
    None — раздел «автоотключение подписок при увольнении» трактует
    отсутствие в каталоге как «уволен/удалён», не как ошибку конфигурации."""
    conn = _service_connection()
    if conn is None:
        return None
    # Экранирование обязательно: username приходит из формы логина, без
    # него можно было бы манипулировать фильтром (LDAP-инъекция), как уже
    # учтено на стороне Go (ldap.EscapeFilter, go-platform/internal/adminapi/auth.go).
    conn.search(f"ou=users,{LDAP_BASE_DN}", f"(&(objectClass=posixAccount)(uid={escape_filter_chars(username)}))",
                attributes=["memberOf"])
    if not conn.entries:
        return None
    entry = conn.entries[0]
    member_of = [str(v) for v in entry.memberOf] if "memberOf" in entry else []
    return entry.entry_dn, member_of


def authenticate(username: str, password: str) -> tuple[bool, bool]:
    """(аутентифицирован, является_админом). Раздел «Безопасность»:
    пароль проверяет LDAP/AD, а не платформа."""
    if not username or not password:
        return False, False
    found = _find_user(username)
    if found is None:
        return False, False
    dn, member_of = found
    conn = Connection(_get_server(), user=dn, password=password)
    if not conn.bind():
        return False, False
    is_admin = any(g == ADMIN_GROUP_DN for g in member_of)
    return True, is_admin


def list_active_usernames() -> set[str] | None:
    """Все uid, реально присутствующие в каталоге сейчас — сырьё для
    «автоотключения подписок при увольнении» (packages/common/deprovision.py):
    подписчик, чей TrueConf-логин не найден здесь, считается ушедшим.
    None (НЕ пустое множество) при недоступности LDAP — раздел И5: сбой
    каталога не должен массово деактивировать всех подписчиков разом."""
    conn = _service_connection()
    if conn is None:
        return None
    ok = conn.search(f"ou=users,{LDAP_BASE_DN}", "(objectClass=posixAccount)", attributes=["uid"])
    if not ok:
        return None
    return {str(e.uid) for e in conn.entries}
