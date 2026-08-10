"""Автоотключение подписок при увольнении — раздел «Безопасность» кейса.
LDAP-каталог (packages/common/ldap_auth.py) — источник истины о том, кто
ещё сотрудник компании; периодически сверяем с ним активных Subscriber.

Раздел И5: недоступность LDAP (`list_active_usernames()` вернула `None`)
или подозрительно пустой ответ каталога НЕ деактивируют никого — это
принципиально отличается от «логина реально нет в списке уволенных»,
которое трактуется как настоящее увольнение/удаление учётной записи."""
from __future__ import annotations

from sqlalchemy import select

from packages.common import ldap_auth
from packages.common.audit import log_action
from packages.models.db import Subscriber


def deactivate_departed_subscribers(session) -> int:
    active_usernames = ldap_auth.list_active_usernames()
    if not active_usernames:
        return 0  # LDAP недоступен ИЛИ пустой ответ — подозрительно, никого не трогаем

    departed = session.execute(
        select(Subscriber).where(Subscriber.active.is_(True),
                                  Subscriber.trueconf_username.not_in(active_usernames))
    ).scalars().all()
    for subscriber in departed:
        subscriber.active = False
        log_action(session, actor="system", action="deprovision",
                   target=subscriber.trueconf_username,
                   detail="логин отсутствует в LDAP-каталоге — подписки деактивированы",
                   resource_type="subscriber", resource_id=str(subscriber.id), actor_role="system")
    if departed:
        session.commit()
    return len(departed)
