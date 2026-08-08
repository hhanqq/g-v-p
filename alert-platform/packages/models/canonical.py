"""Каноническое событие — раздел 12.2. Общая модель, разделяемая между
сервисами (шлюз/парсер пишет, резолвер и API читают и дополняют).

На стадии парсера (services/pipeline/parser) заполнены только "сырые"
идентификаторы объекта (name/ip, взятые прямо из текста источника) и site
(из паспорта источника — раздел 4.3/11.2, не из текста). fqdn, inventory_id
и subnet — задача резолвера (стадия 2, ещё не реализована); здесь они None.

dedup_key на этой стадии — ПРОВИЗОРНЫЙ (раздел 4.1: sha256(site|object_id|
component|symptom_class)), считается по сырому имени объекта до резолва.
Резолвер обязан пересчитать его, когда сырое имя не совпадает с каноническим
object_id (алиас, FQDN vs короткое имя и т.п.).
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from datetime import datetime


def compute_dedup_key(site: str | None, object_id: str | None, component: str | None,
                       symptom_class: str) -> str | None:
    """sha256(site|object_id|component|symptom_class) — раздел 4.1.

    Идентификатор источника в ключ не входит намеренно: два источника,
    наблюдающие один объект, должны схлопнуться в одну проблему.
    Без site и object_id ключ не строится (это карантинный случай — раздел 4.3 п.6).
    """
    if not site or not object_id:
        return None
    raw = f"{site}|{object_id}|{component or ''}|{symptom_class}"
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


@dataclass
class CanonicalObjectRef:
    name: str | None = None
    fqdn: str | None = None
    ip: str | None = None
    inventory_id: str | None = None


@dataclass
class CanonicalEntity:
    object: CanonicalObjectRef = field(default_factory=CanonicalObjectRef)
    component: str | None = None
    site: str | None = None
    subnet: str | None = None


@dataclass
class CanonicalSource:
    system: str
    instance: str
    external_id: str | None = None


@dataclass
class CanonicalEvent:
    source: CanonicalSource
    occurred_at: datetime
    ingest_ts: datetime
    state: str                      # firing | resolved
    symptom_class: str
    severity_raw: str | None
    title: str
    body_raw: str                   # дословный оригинал — раздел И2, не трогать
    entity: CanonicalEntity
    dedup_key: str | None = None
    labels: dict = field(default_factory=dict)
    links: list = field(default_factory=list)
    parser_version: int | None = None
    resolved: bool = False           # True после стадии резолвера (раздел 4.3)

    def to_dict(self) -> dict:
        return {
            "source": {"system": self.source.system, "instance": self.source.instance,
                       "external_id": self.source.external_id},
            "occurred_at": self.occurred_at.isoformat() + "Z",
            "ingest_ts": self.ingest_ts.isoformat() + "Z",
            "state": self.state,
            "dedup_key": self.dedup_key,
            "entity": {
                "object": {
                    "name": self.entity.object.name,
                    "fqdn": self.entity.object.fqdn,
                    "ip": self.entity.object.ip,
                    "inventory_id": self.entity.object.inventory_id,
                },
                "component": self.entity.component,
                "site": self.entity.site,
                "subnet": self.entity.subnet,
            },
            "symptom_class": self.symptom_class,
            "severity_raw": self.severity_raw,
            "title": self.title,
            "body_raw": self.body_raw,
            "labels": self.labels,
            "links": self.links,
            "parser_version": self.parser_version,
            "resolved": self.resolved,
        }
