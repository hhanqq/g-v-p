"""Раздел 8 (M5) — адресация уведомлений по подпискам вместо одного
захардкоженного тестового получателя. Владение объектом/сервисом идёт из
CMDB (packages.models.db.CmdbOwnership); подписчик сам решает, на какой
срез этого владения он хочет получать уведомления. У объекта/сервиса
может не быть владельца вовсе (раздел 4.2) — это нормальное состояние
данных, не ошибка: такие Problem получат только подписчики "вижу всё"
(пустая подписка — роль дежурной смены НОЦ из описания кейса).
"""
from __future__ import annotations

from sqlalchemy import select

from packages.models.db import CmdbOwnership, CmdbServiceObject, Problem, Subscriber, Subscription


def _priority_rank(priority: str | None) -> int | None:
    if not priority or not priority.startswith("P"):
        return None
    try:
        return int(priority[1:])
    except ValueError:
        return None


def owning_subsidiaries(session, problem: Problem) -> set[str]:
    if not problem.object_id:
        return set()
    direct = session.execute(
        select(CmdbOwnership.subsidiary).where(CmdbOwnership.object_id == problem.object_id)
    ).scalars().all()
    via_service = session.execute(
        select(CmdbOwnership.subsidiary)
        .join(CmdbServiceObject, CmdbServiceObject.service_id == CmdbOwnership.service_id)
        .where(CmdbServiceObject.object_id == problem.object_id)
    ).scalars().all()
    return set(direct) | set(via_service)


def owning_service_ids(session, problem: Problem) -> set[str]:
    if not problem.object_id:
        return set()
    return set(session.execute(
        select(CmdbServiceObject.service_id).where(CmdbServiceObject.object_id == problem.object_id)
    ).scalars().all())


def resolve_recipients(session, problem: Problem) -> list[str]:
    """Возвращает уникальные trueconf_username подписчиков, которым
    полагается это Problem. Каждая ось подписки, если заполнена, обязана
    совпасть (AND); незаполненная ось не сужает выборку."""
    subsidiaries = owning_subsidiaries(session, problem)
    service_ids = owning_service_ids(session, problem)
    problem_rank = _priority_rank(problem.priority)

    rows = session.execute(
        select(Subscriber.trueconf_username, Subscription)
        .join(Subscription, Subscription.subscriber_id == Subscriber.id)
        .where(Subscriber.active.is_(True))
        # раздел «Безопасность» — автоотключение при увольнении
        # (packages/common/deprovision.py): подписки деактивированного
        # подписчика остаются в БД для аудита, но маршрутизация их не видит.
    ).all()

    recipients: set[str] = set()
    for username, sub in rows:
        if sub.subsidiary and sub.subsidiary not in subsidiaries:
            continue
        if sub.service_id and sub.service_id not in service_ids:
            continue
        if sub.priority_threshold is not None:
            threshold_rank = _priority_rank(sub.priority_threshold)
            if threshold_rank is not None and problem_rank is not None and problem_rank > threshold_rank:
                continue
        recipients.add(username)
    return sorted(recipients)
