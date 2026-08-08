"""Исполнение сценариев (раздел «Сценарии», Этап 2 платформы) — граф,
нарисованный в редакторе (ReactFlow, узлы Условие/Уведомить/Подождать),
на ИСПОЛНЕНИЕ приводится к линейной цепочке шагов. Это осознанное, явно
проговорённое с пользователем упрощение (см. план платформы): полный
произвольный граф с ветвлением/циклами не реализуется — только то, что
реально покрывает исходный запрос (маршрутизация + эскалация по
дедлайну). Активация сценария с графом, который не сводится к цепочке,
отклоняется на уровне API, не здесь (см. services/api/app_api.py).

Все функции здесь чистые (без обращения к БД/сети) — тестируются
напрямую, как и packages/ai/*."""
from __future__ import annotations

import json
from datetime import datetime, timedelta

from packages.common.routing import _priority_rank
from packages.models.db import Problem

STEP_TYPES = ("condition", "notify", "wait")


def parse_chain(graph_json: str) -> list[dict] | None:
    """Граф ReactFlow -> линейная цепочка шагов, или None если граф не
    сводится к цепочке: не ровно один узел без входящих рёбер, ветвление
    (>1 исходящее ребро у узла), цикл, изолированные узлы вне цепочки,
    неизвестный тип узла, или первый шаг — не "condition"."""
    try:
        graph = json.loads(graph_json)
    except (json.JSONDecodeError, TypeError):
        return None

    nodes = graph.get("nodes") or []
    edges = graph.get("edges") or []
    if not nodes:
        return None
    node_by_id = {n["id"]: n for n in nodes if "id" in n}
    if len(node_by_id) != len(nodes):
        return None

    outgoing: dict[str, list[str]] = {}
    incoming_count: dict[str, int] = {nid: 0 for nid in node_by_id}
    for edge in edges:
        src, tgt = edge.get("source"), edge.get("target")
        if src not in node_by_id or tgt not in node_by_id:
            return None
        outgoing.setdefault(src, []).append(tgt)
        incoming_count[tgt] = incoming_count.get(tgt, 0) + 1

    roots = [nid for nid, count in incoming_count.items() if count == 0]
    if len(roots) != 1:
        return None

    chain: list[dict] = []
    visited: set[str] = set()
    current: str | None = roots[0]
    while current is not None:
        if current in visited:
            return None  # цикл
        visited.add(current)
        node = node_by_id[current]
        node_type = node.get("type")
        if node_type not in STEP_TYPES:
            return None
        chain.append({"type": node_type, **(node.get("data") or {})})
        nxt = outgoing.get(current, [])
        if len(nxt) > 1:
            return None  # ветвление — Этап 2 V1 не поддерживает
        current = nxt[0] if nxt else None

    if len(visited) != len(node_by_id):
        return None  # изолированные узлы вне цепочки
    if chain[0]["type"] != "condition":
        return None
    return chain


def matches_condition(condition: dict, problem: Problem, owning_subsidiaries: set[str]) -> bool:
    """Раздел «Условие» узла: незаполненное поле не сужает совпадение —
    та же семантика, что и у Subscription (раздел 8)."""
    priority_min = condition.get("priority_min")
    if priority_min:
        problem_rank = _priority_rank(problem.priority)
        min_rank = _priority_rank(priority_min)
        if problem_rank is None or min_rank is None or problem_rank > min_rank:
            return False
    subsidiary = condition.get("subsidiary")
    if subsidiary and subsidiary not in owning_subsidiaries:
        return False
    symptom_class = condition.get("symptom_class")
    if symptom_class and problem.symptom_class != symptom_class:
        return False
    return True


def next_action(
    current_step_index: int, step_entered_at: datetime, chain: list[dict],
    problem_status: str, now: datetime,
) -> tuple[str, dict | None, int, datetime]:
    """Продвигает прогон по цепочке ДЕЙСТВИЙ (шаг 0 — всегда "condition",
    уже удовлетворён на момент создания прогона, здесь не переоценивается).

    Возвращает (outcome, шаг-для-уведомления-или-None, новый_индекс,
    новое_время_входа_в_шаг):
      - "notify" — прямо сейчас нужно отправить уведомление по этому шагу;
        новый_индекс уже указывает на шаг ПОСЛЕ уведомления (эскалация,
        если следующий шаг "wait", будет отслеживаться от него).
      - "wait" — ждём дальше, вызывающая сторона просто фиксирует индекс/
        время как есть и ничего не отправляет.
      - "done" — проблема уже не открыта (решилась раньше эскалации —
        успех), или конец цепочки, или встретился нераспознанный тип шага
        (раздел И5 — не падаем на кривых данных, просто останавливаемся).
    """
    if problem_status not in ("OPEN", "FLAPPING"):
        return "done", None, current_step_index, step_entered_at

    index = current_step_index
    entered_at = step_entered_at
    if index == 0:
        index, entered_at = 1, now

    while index < len(chain):
        step = chain[index]
        step_type = step.get("type")
        if step_type == "notify":
            return "notify", step, index + 1, now
        if step_type == "wait":
            minutes = step.get("minutes") or 0
            if now - entered_at < timedelta(minutes=minutes):
                return "wait", None, index, entered_at
            index, entered_at = index + 1, now
            continue
        return "done", None, index, entered_at

    return "done", None, index, entered_at
