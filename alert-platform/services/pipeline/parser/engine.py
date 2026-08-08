"""Стадия 1 — парсер (раздел 5). Регулярки коннектора превращают сырой
raw_body в каноническое событие с сырыми (не резолвленными) идентификаторами
объекта. Резолюция объекта, subnet, fqdn/inventory_id — стадия 2 (не здесь).

Инвариант И5 (деградация в сторону шума): при неудаче разбора обязательных
полей raw_body не теряется — ParseResult.success=False, но raw_body при
этом возвращается вызывающему коду для отправки в резервный канал "как есть".
Несовпадение symptom_rules — не отказ: symptom_class="unknown", доставка
продолжается.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

from packages.models.canonical import (CanonicalEntity, CanonicalEvent,
                                        CanonicalObjectRef, CanonicalSource,
                                        compute_dedup_key)

from .connector import Connector


@dataclass
class ParseResult:
    success: bool
    event: CanonicalEvent | None = None
    error: str | None = None
    raw_body: str = ""
    source_system: str = ""
    source_instance: str = ""


def _extract(connector: Connector, name: str, text: str) -> str | None:
    pattern = connector.field_patterns.get(name)
    if pattern is None:
        return None
    m = pattern.search(text)
    return m.group(1) if m else None


def _match_symptom(connector: Connector, title: str) -> tuple[str, str | None]:
    for rule in connector.symptom_rules:
        m = rule.pattern.search(title)
        if m:
            component = m.group(rule.component_group) if rule.component_group else None
            return rule.symptom_class, component
    return "unknown", None


def parse(connector: Connector, raw_body: str, source_instance: str,
          received_at: datetime, site: str | None) -> ParseResult:
    base = dict(raw_body=raw_body, source_system=connector.source_system,
                source_instance=source_instance)

    missing = [f for f in connector.required_fields
               if _extract(connector, f, raw_body) is None]
    if missing:
        return ParseResult(success=False, error=f"обязательные поля не найдены: {missing}", **base)

    state_prefix = _extract(connector, "state_prefix", raw_body)
    state = connector.state_map.get(state_prefix)
    if state is None:
        return ParseResult(success=False, error=f"неизвестный префикс состояния: {state_prefix}", **base)

    title = _extract(connector, "title", raw_body)
    host_raw = _extract(connector, "host_raw", raw_body) or _extract(connector, "node_raw", raw_body)
    ip_raw = _extract(connector, "ip_raw", raw_body)
    severity_raw = _extract(connector, "severity_raw", raw_body)
    time_raw = _extract(connector, "time_raw", raw_body)

    try:
        occurred_at = datetime.strptime(time_raw, connector.time_format)
    except (ValueError, TypeError):
        occurred_at = received_at  # раздел 6.6: расхождение часов — деградация, не отказ

    symptom_class, component = _match_symptom(connector, title)

    dedup_key = compute_dedup_key(site, host_raw, component, symptom_class)

    event = CanonicalEvent(
        source=CanonicalSource(system=connector.source_system, instance=source_instance),
        occurred_at=occurred_at,
        ingest_ts=received_at,
        state=state,
        symptom_class=symptom_class,
        severity_raw=severity_raw,
        title=title,
        body_raw=raw_body,          # дословно — И2
        entity=CanonicalEntity(
            object=CanonicalObjectRef(name=host_raw, ip=ip_raw),
            component=component,
            site=site,
        ),
        dedup_key=dedup_key,
        parser_version=connector.version,
        resolved=False,
    )
    return ParseResult(success=True, event=event, **base)
