"""Резолвер — раздел 4.3. Каскад сопоставления события с объектом CMDB.

Шаг 4 (MAC/серийный/инвентарный номер) в этой версии не реализован —
ни один из наших источников не передаёт эти идентификаторы в теле алерта
(реалистично: Zabbix/SolarWinds текстовые уведомления их обычно не несут).
Шаг помечен явно, а не молча пропущен, чтобы не создавать иллюзию покрытия.
"""
from __future__ import annotations

import difflib
from dataclasses import dataclass

from sqlalchemy import select
from sqlalchemy.orm import Session

from packages.models.db import CmdbAlias, CmdbObject

FUZZY_CUTOFF = 0.75


@dataclass
class ResolveResult:
    object_id: str | None
    method: str  # alias | fqdn | ip | fuzzy | quarantine
    confidence: float | None = None


def _alias_lookup(session: Session, site: str, raw_name: str) -> str | None:
    return session.execute(
        select(CmdbAlias.object_id).where(CmdbAlias.site == site, CmdbAlias.raw_name == raw_name)
    ).scalar_one_or_none()


def _ip_lookup(session: Session, site: str, ip: str) -> str | None:
    return session.execute(
        select(CmdbObject.id).where(CmdbObject.site == site, CmdbObject.ip == ip)
    ).scalar_one_or_none()


def _fuzzy_lookup(session: Session, site: str, raw_name: str) -> tuple[str | None, float]:
    rows = session.execute(select(CmdbObject.id, CmdbObject.name).where(CmdbObject.site == site)).all()
    if not rows:
        return None, 0.0
    names = [name for _, name in rows]
    matches = difflib.get_close_matches(raw_name, names, n=1, cutoff=FUZZY_CUTOFF)
    if not matches:
        return None, 0.0
    best_name = matches[0]
    ratio = difflib.SequenceMatcher(a=raw_name, b=best_name).ratio()
    object_id = next(oid for oid, name in rows if name == best_name)
    return object_id, ratio


def resolve(session: Session, site: str | None, object_name_raw: str | None,
            ip_raw: str | None) -> ResolveResult:
    if not site or not object_name_raw:
        # Без площадки или без сырого имени объекта каскад не имеет смысла
        # запускать — площадка обязательна во всех шагах (раздел 4.1).
        return ResolveResult(object_id=None, method="quarantine")

    # 1. Точный алиас.
    object_id = _alias_lookup(session, site, object_name_raw)
    if object_id:
        return ResolveResult(object_id=object_id, method="alias", confidence=1.0)

    # 2. Нормализованный FQDN — берём короткую часть до первой точки.
    if "." in object_name_raw:
        short_name = object_name_raw.split(".", 1)[0]
        object_id = _alias_lookup(session, site, short_name)
        if object_id:
            return ResolveResult(object_id=object_id, method="fqdn", confidence=0.95)

    # 3. IP в пределах площадки.
    if ip_raw:
        object_id = _ip_lookup(session, site, ip_raw)
        if object_id:
            return ResolveResult(object_id=object_id, method="ip", confidence=0.9)

    # 4. MAC / серийный / инвентарный номер — не реализовано (см. докстринг).

    # 5. Нечёткое сравнение имени.
    object_id, ratio = _fuzzy_lookup(session, site, object_name_raw)
    if object_id:
        return ResolveResult(object_id=object_id, method="fuzzy", confidence=round(ratio, 3))

    # 6. Карантин.
    return ResolveResult(object_id=None, method="quarantine")
