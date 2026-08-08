"""Исполнение сценариев (раздел «Сценарии») — граф, нарисованный в
редакторе (ReactFlow: Условие/Уведомить/Подождать/Проверка реакции/
Проверка подписки), на ИСПОЛНЕНИЕ приводится к структуре с ветвлением:
узлы-развилки (ack_check/subscription_check) имеют ровно два исходящих
ребра ("да"/"нет"), остальные узлы — не более одного. Осознанное,
явно проговорённое с пользователем упрощение (Этап 3, план платформы):
произвольные ЦИКЛЫ по-прежнему не поддерживаются (граф должен быть DAG),
но ветвление и слияние веток — да, этого не было в Этапе 2 (линейная
цепочка). Активация сценария с графом, который не проходит валидацию,
отклоняется на уровне API, не здесь (см. services/api/app_api.py).

Все функции здесь чистые (без обращения к БД/сети) — тестируются
напрямую, как и packages/ai/*. `advance()` принимает уже готовый словарь
`facts` (результат проверки ack/подписок) вместо того, чтобы обращаться
к БД самому — вызывающая сторона (services/delivery_trueconf/main.py)
считает факты один раз до вызова."""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import datetime, timedelta

from packages.common.routing import _priority_rank
from packages.models.db import Problem

LINEAR_TYPES = ("condition", "notify", "wait")
DECISION_TYPES = ("ack_check", "subscription_check")
STEP_TYPES = LINEAR_TYPES + DECISION_TYPES


@dataclass
class ParsedGraph:
    root_id: str
    nodes: dict[str, dict] = field(default_factory=dict)
    # Узлы-развилки: {"yes": id|None, "no": id|None}. Остальные: {"default": id|None}.
    edges: dict[str, dict[str, str | None]] = field(default_factory=dict)


def parse_graph(graph_json: str) -> ParsedGraph | None:
    """Граф ReactFlow -> валидированная структура для advance(), или None,
    если граф не проходит проверку: не ровно один узел без входящих
    рёбер (и это не "condition"), неизвестный тип узла, у не-развилки
    больше одного исходящего ребра, у развилки — рёбра не строго с
    sourceHandle "yes"/"no" без дублей, цикл (ребро на узел, который ещё
    в текущем стеке обхода), изолированный узел вне графа."""
    try:
        graph = json.loads(graph_json)
    except (json.JSONDecodeError, TypeError):
        return None

    nodes = graph.get("nodes") or []
    edges_raw = graph.get("edges") or []
    if not nodes:
        return None
    node_by_id = {n["id"]: n for n in nodes if "id" in n}
    if len(node_by_id) != len(nodes):
        return None
    for node in node_by_id.values():
        if node.get("type") not in STEP_TYPES:
            return None

    incoming_count: dict[str, int] = {nid: 0 for nid in node_by_id}
    outgoing: dict[str, list[tuple[str | None, str]]] = {nid: [] for nid in node_by_id}
    for edge in edges_raw:
        src, tgt = edge.get("source"), edge.get("target")
        if src not in node_by_id or tgt not in node_by_id:
            return None
        outgoing[src].append((edge.get("sourceHandle"), tgt))
        incoming_count[tgt] += 1

    roots = [nid for nid, count in incoming_count.items() if count == 0]
    if len(roots) != 1:
        return None
    root_id = roots[0]
    if node_by_id[root_id].get("type") != "condition":
        return None

    edge_map: dict[str, dict[str, str | None]] = {}
    for nid, node in node_by_id.items():
        node_type = node["type"]
        conns = outgoing[nid]
        if node_type in DECISION_TYPES:
            branch: dict[str, str | None] = {"yes": None, "no": None}
            for handle, tgt in conns:
                if handle not in ("yes", "no") or branch[handle] is not None:
                    return None  # неразмеченное/дублирующее ребро на развилке
                branch[handle] = tgt
            edge_map[nid] = branch
        else:
            if len(conns) > 1:
                return None  # обычный узел — не более одного исходящего ребра
            edge_map[nid] = {"default": conns[0][1] if conns else None}

    # Двухцветный DFS: WHITE (не посещён) -> GRAY (в текущем стеке) -> BLACK
    # (обработан). Ребро на GRAY = настоящий цикл (отклонить). Ребро на
    # BLACK = слияние веток в DAG (разрешено — например, две ветки
    # ack_check сходятся в одном общем узле-эскалации ниже).
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {nid: WHITE for nid in node_by_id}

    def visit(nid: str) -> bool:
        color[nid] = GRAY
        for target in edge_map[nid].values():
            if target is None:
                continue
            if color[target] == GRAY:
                return False  # цикл
            if color[target] == WHITE and not visit(target):
                return False
        color[nid] = BLACK
        return True

    if not visit(root_id):
        return None
    if any(c != BLACK for c in color.values()):
        return None  # изолированный узел, не достижимый из корня

    steps = {nid: {"type": node["type"], **(node.get("data") or {})} for nid, node in node_by_id.items()}
    return ParsedGraph(root_id=root_id, nodes=steps, edges=edge_map)


def matches_condition(condition: dict, problem: Problem, owning_subsidiaries: set[str]) -> bool:
    """Раздел «Условие» корневого узла: незаполненное поле не сужает
    совпадение — та же семантика, что и у Subscription (раздел 8)."""
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


def advance(
    current_node_id: str, step_entered_at: datetime, graph: ParsedGraph,
    problem_status: str, facts: dict[str, bool], now: datetime,
) -> tuple[str, dict | None, str | None, datetime]:
    """Продвигает прогон по графу от текущего узла. Корень ("condition")
    уже удовлетворён на момент создания прогона, здесь не переоценивается
    — с него сразу уходим на следующий узел.

    Возвращает (outcome, шаг-для-уведомления-или-None, новый_id_узла,
    новое_время_входа_в_узел):
      - "notify" — прямо сейчас нужно отправить уведомление по этому
        узлу; новый_id_узла уже указывает на узел ПОСЛЕ уведомления
        (может быть None, если дальше графа нет — тогда вызывающая
        сторона сама переводит прогон в status="done", отдельного
        сигнала для этого не требуется).
      - "wait" — ждём дальше, вызывающая сторона фиксирует узел/время
        как есть и ничего не отправляет.
      - "done" — проблема уже не открыта (решилась раньше — успех), путь
        уткнулся в узел без исходящего ребра, или встретился
        нераспознанный тип узла (раздел И5 — не падаем на кривых данных).

    Развилки (ack_check/subscription_check) не тратят тик: один вызов
    может пройти несколько развилок подряд, пока не упрётся в
    notify/wait/тупик — переход берётся из facts[node_id] (True → "yes",
    False → "no", отсутствие ключа трактуется как False)."""
    if problem_status not in ("OPEN", "FLAPPING"):
        return "done", None, current_node_id, step_entered_at

    node_id = current_node_id
    entered_at = step_entered_at
    if node_id == graph.root_id:
        nxt = graph.edges[node_id].get("default")
        if nxt is None:
            return "done", None, node_id, entered_at
        node_id, entered_at = nxt, now

    for _ in range(len(graph.nodes) + 1):  # защитный предел — parse_graph уже гарантирует DAG
        step = graph.nodes[node_id]
        step_type = step.get("type")

        if step_type == "notify":
            return "notify", step, graph.edges[node_id].get("default"), now

        if step_type == "wait":
            minutes = step.get("minutes") or 0
            if now - entered_at < timedelta(minutes=minutes):
                return "wait", None, node_id, entered_at
            nxt = graph.edges[node_id].get("default")
            if nxt is None:
                return "done", None, node_id, entered_at
            node_id, entered_at = nxt, now
            continue

        if step_type in DECISION_TYPES:
            branch = "yes" if facts.get(node_id, False) else "no"
            nxt = graph.edges[node_id].get(branch)
            if nxt is None:
                return "done", None, node_id, entered_at
            node_id, entered_at = nxt, now
            continue

        return "done", None, node_id, entered_at  # неизвестный тип — раздел И5

    return "done", None, node_id, entered_at
