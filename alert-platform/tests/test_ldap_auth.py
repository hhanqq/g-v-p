"""Тесты packages/common/ldap_auth.py — без живого LDAP (Connection
подменяется фейком, тот же паттерн, что и у остальных AI/внешних-
зависимостей модулей проекта). Основной фокус — что username из формы
логина всегда экранируется перед вставкой в LDAP-фильтр (раздел
«Безопасность»: та же дисциплина, что уже применена на стороне Go,
ldap.EscapeFilter в go-platform/internal/adminapi/auth.go)."""
from __future__ import annotations

from packages.common import ldap_auth


class FakeEntry:
    def __init__(self, dn: str, member_of: list[str] | None = None):
        self.entry_dn = dn
        self._member_of = member_of or []

    def __contains__(self, name: str) -> bool:
        return name == "memberOf" and bool(self._member_of)

    @property
    def memberOf(self):  # noqa: N802 - ldap3 attribute-style access
        return self._member_of


class FakeConnection:
    """Первый bind (служебный) всегда успешен; search записывает точный
    переданный фильтр для проверки экранирования; последующий bind
    (проверка пароля пользователя) управляется through `bind_result`."""

    last_search_filter: str | None = None
    bind_result = True
    entries: list[FakeEntry] = []

    def __init__(self, server, user=None, password=None):
        self.user = user
        self.password = password

    def bind(self) -> bool:
        return FakeConnection.bind_result

    def search(self, base, filter_, attributes=None):
        FakeConnection.last_search_filter = filter_
        self.entries = FakeConnection.entries
        return True


def test_find_user_escapes_malicious_username(monkeypatch):
    """Без экранирования 'admin1)(uid=*' превратило бы фильтр в
    (&(objectClass=posixAccount)(uid=admin1)(uid=*)) — классическая
    LDAP-инъекция, расширяющая совпадение на произвольных пользователей."""
    monkeypatch.setattr(ldap_auth, "Connection", FakeConnection)
    FakeConnection.entries = []
    malicious = "admin1)(uid=*"

    ldap_auth._find_user(malicious)

    assert FakeConnection.last_search_filter is not None
    assert "*" not in FakeConnection.last_search_filter or "\\2a" in FakeConnection.last_search_filter
    assert "admin1)(uid=*" not in FakeConnection.last_search_filter
    assert FakeConnection.last_search_filter == "(&(objectClass=posixAccount)(uid=admin1\\29\\28uid=\\2a))"


def test_authenticate_still_works_for_a_normal_username(monkeypatch):
    monkeypatch.setattr(ldap_auth, "Connection", FakeConnection)
    FakeConnection.entries = [FakeEntry(dn="cn=engineer1,ou=employees,ou=users,dc=x", member_of=[])]
    FakeConnection.bind_result = True

    authenticated, is_admin = ldap_auth.authenticate("engineer1", "whatever")

    assert authenticated is True
    assert is_admin is False
    assert FakeConnection.last_search_filter == "(&(objectClass=posixAccount)(uid=engineer1))"
