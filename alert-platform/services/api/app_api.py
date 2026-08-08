"""JSON API для нового SPA (платформа, Этап 1 — план в
~/.claude/plans/cheerful-mixing-pillow.md): живые данные для Главной/
Инцидентов/Алертов/Оборудования/Сотрудников, плюс задел (список без
исполнения) для Сценариев/SLA/Интеграций. Отдельный файл от
services/api/main.py (уже большой) — роуты монтируются туда как единый
APIRouter с префиксом /api.

Сессионная LDAP-авторизация (packages/common/session_auth.py) — отдельная
от HTTP Basic, которым защищены старые страницы /dashboard, /sources,
/audit в main.py; те не трогаем."""
from __future__ import annotations

from datetime import datetime

from fastapi import APIRouter, Depends, HTTPException, Request
from pydantic import BaseModel
from sqlalchemy import func, select

from packages.common import session_auth
from packages.common.audit import log_action
from packages.common.db import get_session
from packages.models.db import (CmdbObject, EmployeeAvailability, Event, Incident, IncidentProblem,
                                 Problem, SLARule, Scenario, Signal, Subscriber, Subscription)
from packages.scenarios.engine import parse_graph
from services.api import metrics

router = APIRouter(prefix="/api")


# --- Авторизация -------------------------------------------------------------

class LoginRequest(BaseModel):
    username: str
    password: str


@router.post("/auth/login")
def auth_login(payload: LoginRequest, request: Request) -> dict:
    return session_auth.login(request, payload.username, payload.password)


@router.post("/auth/logout")
def auth_logout(request: Request) -> dict:
    session_auth.logout(request)
    return {"ok": True}


@router.get("/auth/me")
def auth_me(request: Request) -> dict:
    user = session_auth.current_user(request)
    if user is None:
        raise HTTPException(401, "Не авторизован")
    return user


# --- Главная -----------------------------------------------------------------

@router.get("/home/summary")
def home_summary(user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        return metrics.dashboard_snapshot(session)


# --- Аналитика (историческая, раздел 7, Этап 4) -------------------------------

@router.get("/analytics/summary")
def analytics_summary(user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        return metrics.analytics_summary(session)


# --- Инциденты -----------------------------------------------------------------

@router.get("/incidents")
def list_incidents(status: str | None = None, limit: int = 100,
                    user: dict = Depends(session_auth.require_session_user)) -> list[dict]:
    with get_session() as session:
        incidents = session.execute(
            select(Incident).order_by(Incident.id.desc()).limit(limit)
        ).scalars().all()
        result = []
        for inc in incidents:
            root = session.get(Problem, inc.root_problem_id)
            is_open = bool(root and root.status in ("OPEN", "FLAPPING"))
            if status == "open" and not is_open:
                continue
            if status == "closed" and is_open:
                continue
            member_count = session.execute(
                select(func.count()).select_from(IncidentProblem)
                .where(IncidentProblem.incident_id == inc.id)
            ).scalar_one()
            result.append({
                "id": inc.id, "priority": inc.priority, "opened_at": inc.opened_at.isoformat(),
                "closed_at": inc.closed_at.isoformat() if inc.closed_at else None,
                "root_object_id": root.object_id if root else None,
                "root_symptom_class": root.symptom_class if root else None,
                "status": root.status if root else "?",
                "member_count": member_count,
            })
        return result


@router.get("/incidents/{incident_id}")
def get_incident(incident_id: int, user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        inc = session.get(Incident, incident_id)
        if inc is None:
            raise HTTPException(404, "Инцидент не найден")
        members = session.execute(
            select(IncidentProblem, Problem)
            .join(Problem, Problem.id == IncidentProblem.problem_id)
            .where(IncidentProblem.incident_id == incident_id)
        ).all()
        return {
            "id": inc.id, "priority": inc.priority, "opened_at": inc.opened_at.isoformat(),
            "closed_at": inc.closed_at.isoformat() if inc.closed_at else None,
            "root_problem_id": inc.root_problem_id,
            "members": [
                {"problem_id": p.id, "role": ip.role, "rule_id": ip.rule_id, "object_id": p.object_id,
                 "symptom_class": p.symptom_class, "status": p.status, "priority": p.priority,
                 "opened_at": p.opened_at.isoformat(),
                 "resolved_at": p.resolved_at.isoformat() if p.resolved_at else None,
                 "ai_root_cause_hypothesis": p.ai_root_cause_hypothesis,
                 "acknowledged_at": p.acknowledged_at.isoformat() if p.acknowledged_at else None,
                 "acknowledged_by": p.acknowledged_by}
                for ip, p in members
            ],
        }


# --- Алерты (журнал) ----------------------------------------------------------

@router.get("/alerts")
def list_alerts(limit: int = 100, offset: int = 0, source_system: str | None = None,
                 symptom_class: str | None = None,
                 user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        base = select(Event, Signal).join(Signal, Signal.id == Event.signal_id)
        if source_system:
            base = base.where(Signal.source_system == source_system)
        if symptom_class:
            base = base.where(Event.symptom_class == symptom_class)
        total = session.execute(select(func.count()).select_from(base.subquery())).scalar_one()
        rows = session.execute(base.order_by(Event.id.desc()).limit(limit).offset(offset)).all()
        return {
            "total": total,
            "items": [
                {"id": e.id, "signal_id": e.signal_id, "source_system": s.source_system,
                 "source_instance": s.source_instance, "symptom_class": e.symptom_class,
                 "symptom_class_source": e.symptom_class_source, "state": e.state, "site": e.site,
                 "object_id": e.object_id, "resolved": e.resolved, "title": e.title,
                 "occurred_at": e.occurred_at.isoformat(), "problem_id": e.problem_id}
                for e, s in rows
            ],
        }


# --- Оборудование ---------------------------------------------------------------

@router.get("/equipment")
def list_equipment(site: str | None = None,
                    user: dict = Depends(session_auth.require_session_user)) -> list[dict]:
    with get_session() as session:
        q = select(CmdbObject)
        if site:
            q = q.where(CmdbObject.site == site)
        objs = session.execute(q).scalars().all()
        return [
            {"id": o.id, "kind": o.kind, "equipment_type": o.equipment_type, "site": o.site,
             "name": o.name, "fqdn": o.fqdn, "ip": o.ip}
            for o in objs
        ]


@router.get("/equipment/{object_id}")
def get_equipment(object_id: str, user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        obj = session.get(CmdbObject, object_id)
        if obj is None:
            raise HTTPException(404, "Объект не найден")
        problems = session.execute(
            select(Problem).where(Problem.object_id == object_id)
            .order_by(Problem.opened_at.desc()).limit(50)
        ).scalars().all()
        return {
            "id": obj.id, "kind": obj.kind, "equipment_type": obj.equipment_type, "site": obj.site,
            "name": obj.name, "fqdn": obj.fqdn, "ip": obj.ip, "subnet": obj.subnet,
            "install_date": obj.install_date.isoformat() if obj.install_date else None,
            "spec_json": obj.spec_json,
            "related_problems": [
                {"id": p.id, "symptom_class": p.symptom_class, "status": p.status, "priority": p.priority,
                 "opened_at": p.opened_at.isoformat(),
                 "resolved_at": p.resolved_at.isoformat() if p.resolved_at else None,
                 "incident_id": p.incident_id, "duplicate_of_problem_id": p.duplicate_of_problem_id}
                for p in problems
            ],
        }


# --- Сотрудники ------------------------------------------------------------------

@router.get("/employees")
def list_employees(user: dict = Depends(session_auth.require_session_user)) -> list[dict]:
    with get_session() as session:
        subs = session.execute(select(Subscriber)).scalars().all()
        result = []
        for s in subs:
            sub_count = session.execute(
                select(func.count()).select_from(Subscription).where(Subscription.subscriber_id == s.id)
            ).scalar_one()
            availability = session.execute(
                select(EmployeeAvailability).where(EmployeeAvailability.subscriber_id == s.id)
                .order_by(EmployeeAvailability.valid_from.desc()).limit(1)
            ).scalars().first()
            result.append({
                "id": s.id, "trueconf_username": s.trueconf_username, "full_name": s.full_name,
                "phone": s.phone, "email": s.email, "position": s.position, "active": s.active,
                "subscription_count": sub_count,
                "availability_status": availability.status if availability else None,
            })
        return result


@router.get("/employees/{employee_id}")
def get_employee(employee_id: int, user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        s = session.get(Subscriber, employee_id)
        if s is None:
            raise HTTPException(404, "Сотрудник не найден")
        subs = session.execute(
            select(Subscription).where(Subscription.subscriber_id == employee_id)
        ).scalars().all()
        availability = session.execute(
            select(EmployeeAvailability).where(EmployeeAvailability.subscriber_id == employee_id)
            .order_by(EmployeeAvailability.valid_from.desc())
        ).scalars().all()
        return {
            "id": s.id, "trueconf_username": s.trueconf_username, "full_name": s.full_name,
            "phone": s.phone, "email": s.email, "position": s.position, "active": s.active,
            "subscriptions": [
                {"id": sub.id, "subsidiary": sub.subsidiary, "service_id": sub.service_id,
                 "priority_threshold": sub.priority_threshold} for sub in subs
            ],
            "availability_history": [
                {"id": a.id, "status": a.status, "valid_from": a.valid_from.isoformat(),
                 "valid_until": a.valid_until.isoformat() if a.valid_until else None,
                 "source": a.source, "note": a.note} for a in availability
            ],
        }


class AvailabilityRequest(BaseModel):
    status: str
    valid_until: str | None = None
    note: str | None = None


@router.post("/employees/{employee_id}/availability")
def set_employee_availability(employee_id: int, payload: AvailabilityRequest,
                               user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        subscriber = session.get(Subscriber, employee_id)
        if subscriber is None:
            raise HTTPException(404, "Сотрудник не найден")
        valid_until = datetime.fromisoformat(payload.valid_until) if payload.valid_until else None
        row = EmployeeAvailability(subscriber_id=employee_id, status=payload.status,
                                    valid_from=datetime.utcnow(), valid_until=valid_until,
                                    source="manual", note=payload.note, created_at=datetime.utcnow())
        session.add(row)
        session.commit()
        log_action(session, actor=user["username"], action="set_availability",
                   target=subscriber.trueconf_username, detail=payload.status)
        return {"ok": True, "id": row.id}


# --- Сценарии (раздел «Сценарии», Этап 2 — редактор + исполнение) --------------
# Исполнение самого графа — packages/scenarios/engine.py, вызывается из
# services/delivery_trueconf/main.py::run_scenarios. Здесь только CRUD +
# активация (с серверной валидацией через parse_graph — раздел И5: кривой
# граф не должен молча "исполняться" никак).

@router.get("/scenarios")
def list_scenarios(user: dict = Depends(session_auth.require_session_user)) -> list[dict]:
    with get_session() as session:
        rows = session.execute(select(Scenario).order_by(Scenario.updated_at.desc())).scalars().all()
        return [{"id": r.id, "name": r.name, "description": r.description, "status": r.status,
                 "updated_at": r.updated_at.isoformat()} for r in rows]


@router.get("/scenarios/{scenario_id}")
def get_scenario(scenario_id: int, user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        row = session.get(Scenario, scenario_id)
        if row is None:
            raise HTTPException(404, "Сценарий не найден")
        return {"id": row.id, "name": row.name, "description": row.description,
                "graph_json": row.graph_json, "status": row.status, "created_by": row.created_by,
                "updated_at": row.updated_at.isoformat()}


class ScenarioCreateRequest(BaseModel):
    name: str
    description: str | None = None
    graph_json: str


@router.post("/scenarios")
def create_scenario(payload: ScenarioCreateRequest,
                     user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        now = datetime.utcnow()
        row = Scenario(name=payload.name, description=payload.description, graph_json=payload.graph_json,
                        status="draft", created_by=user["username"], created_at=now, updated_at=now)
        session.add(row)
        session.commit()
        log_action(session, actor=user["username"], action="create_scenario", target=row.name)
        return {"ok": True, "id": row.id}


class ScenarioUpdateRequest(BaseModel):
    name: str | None = None
    description: str | None = None
    graph_json: str | None = None


@router.put("/scenarios/{scenario_id}")
def update_scenario(scenario_id: int, payload: ScenarioUpdateRequest,
                     user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        row = session.get(Scenario, scenario_id)
        if row is None:
            raise HTTPException(404, "Сценарий не найден")
        if payload.name is not None:
            row.name = payload.name
        if payload.description is not None:
            row.description = payload.description
        if payload.graph_json is not None:
            row.graph_json = payload.graph_json
            if row.status == "active":
                # раздел И5 — граф изменился, старая проверка parse_graph
                # больше не гарантированно верна: снимаем с исполнения,
                # пока не подтвердят заново через /activate.
                row.status = "draft"
        row.updated_at = datetime.utcnow()
        session.commit()
        return {"ok": True, "status": row.status}


@router.post("/scenarios/{scenario_id}/activate")
def activate_scenario(scenario_id: int, user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        row = session.get(Scenario, scenario_id)
        if row is None:
            raise HTTPException(404, "Сценарий не найден")
        if parse_graph(row.graph_json) is None:
            raise HTTPException(
                400, "Граф не сводится к линейной цепочке: нужен один узел «Условие» на входе, "
                     "без ветвлений и циклов (раздел «Сценарии», Этап 2)")
        row.status = "active"
        row.updated_at = datetime.utcnow()
        session.commit()
        log_action(session, actor=user["username"], action="activate_scenario", target=row.name)
        return {"ok": True}


@router.post("/scenarios/{scenario_id}/deactivate")
def deactivate_scenario(scenario_id: int, user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        row = session.get(Scenario, scenario_id)
        if row is None:
            raise HTTPException(404, "Сценарий не найден")
        row.status = "draft"
        row.updated_at = datetime.utcnow()
        session.commit()
        log_action(session, actor=user["username"], action="deactivate_scenario", target=row.name)
        return {"ok": True}


# --- SLA -------------------------------------------------------------------------

@router.get("/sla-rules")
def list_sla_rules(user: dict = Depends(session_auth.require_session_user)) -> list[dict]:
    with get_session() as session:
        rows = session.execute(select(SLARule)).scalars().all()
        return [{"id": r.id, "name": r.name, "priority": r.priority, "subsidiary": r.subsidiary,
                 "service_id": r.service_id, "response_minutes": r.response_minutes,
                 "resolution_minutes": r.resolution_minutes} for r in rows]


class SlaRuleCreateRequest(BaseModel):
    name: str
    priority: str
    subsidiary: str | None = None
    service_id: str | None = None
    response_minutes: int
    resolution_minutes: int


@router.post("/sla-rules")
def create_sla_rule(payload: SlaRuleCreateRequest,
                     user: dict = Depends(session_auth.require_session_user)) -> dict:
    with get_session() as session:
        row = SLARule(name=payload.name, priority=payload.priority, subsidiary=payload.subsidiary,
                       service_id=payload.service_id, response_minutes=payload.response_minutes,
                       resolution_minutes=payload.resolution_minutes, created_at=datetime.utcnow())
        session.add(row)
        session.commit()
        log_action(session, actor=user["username"], action="create_sla_rule", target=row.name)
        return {"ok": True, "id": row.id}


# --- Интеграции (статус-факты, управлять пока нечем) ---------------------------

@router.get("/integrations/status")
def integrations_status(user: dict = Depends(session_auth.require_session_user)) -> list[dict]:
    # Раздел «Интеграции» — статус-факты, не выдуманные подключения.
    # HR-система явно помечена открытым вопросом (пользователь сам
    # сказал, что источник доступности сотрудников нужно уточнить у
    # менторов) — не изображаем работающую интеграцию, которой нет.
    return [
        {"name": "TrueConf", "status": "active", "detail": "Основной канал доставки, раздел 9"},
        {"name": "Системы мониторинга (Zabbix/SolarWinds)", "status": "active",
         "detail": "Подключение источников — /sources/, без редеплоя"},
        {"name": "SMS", "status": "planned", "detail": "Опционально по кейсу — не реализовано в Этапе 1"},
        {"name": "E-mail", "status": "planned", "detail": "Опционально по кейсу — не реализовано в Этапе 1"},
        {"name": "HR-система (доступность сотрудников)", "status": "open_question",
         "detail": "Источник данных не определён — требует уточнения у менторов"},
    ]
