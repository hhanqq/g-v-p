"""Тесты исполнения сценариев (раздел «Сценарии», Этап 3 — ветвление) —
чистая логика packages/scenarios/engine.py, без БД/бота (как packages/ai/*)."""
import json
from datetime import datetime, timedelta

from packages.scenarios.engine import advance, matches_condition, parse_graph

T0 = datetime(2026, 8, 6, 10, 0, 0)


def _problem(priority="P1", symptom_class="node_down"):
    from packages.models.db import Problem
    return Problem(dedup_key="x", status="OPEN", object_id="brd-noyabrsk/rig-01",
                    symptom_class=symptom_class, site="brd-noyabrsk", opened_at=T0,
                    last_seen_at=T0, repeat_count=1, toggle_count=0, priority=priority)


def _graph(nodes, edges):
    return json.dumps({"nodes": nodes, "edges": edges})


def _edge(source, target, handle=None):
    e = {"source": source, "target": target}
    if handle:
        e["sourceHandle"] = handle
    return e


# ---------- parse_graph: базовые случаи (наследие линейной цепочки) ----------

def test_parse_graph_valid_linear_chain():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {"priority_min": "P1"}},
            {"id": "2", "type": "notify", "data": {"employee_id": 5}},
            {"id": "3", "type": "wait", "data": {"minutes": 30}},
            {"id": "4", "type": "notify", "data": {"employee_id": 6}},
        ],
        edges=[_edge("1", "2"), _edge("2", "3"), _edge("3", "4")],
    )
    parsed = parse_graph(graph)
    assert parsed is not None
    assert parsed.root_id == "1"
    assert parsed.nodes["2"]["employee_id"] == 5
    assert parsed.edges["1"] == {"default": "2"}


def test_parse_graph_rejects_empty_graph():
    assert parse_graph(_graph([], [])) is None


def test_parse_graph_rejects_invalid_json():
    assert parse_graph("not json") is None


def test_parse_graph_rejects_multiple_roots():
    graph = _graph(
        nodes=[{"id": "1", "type": "condition", "data": {}}, {"id": "2", "type": "condition", "data": {}}],
        edges=[],
    )
    assert parse_graph(graph) is None


def test_parse_graph_rejects_isolated_node():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {}},
            {"id": "2", "type": "notify", "data": {}},
            {"id": "3", "type": "notify", "data": {}},  # изолирован
        ],
        edges=[_edge("1", "2")],
    )
    assert parse_graph(graph) is None


def test_parse_graph_rejects_when_root_not_condition():
    assert parse_graph(_graph([{"id": "1", "type": "notify", "data": {}}], [])) is None


def test_parse_graph_rejects_unknown_node_type():
    graph = _graph(
        nodes=[{"id": "1", "type": "condition", "data": {}}, {"id": "2", "type": "mystery", "data": {}}],
        edges=[_edge("1", "2")],
    )
    assert parse_graph(graph) is None


def test_parse_graph_rejects_non_decision_node_with_two_outgoing_edges():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {}},
            {"id": "2", "type": "notify", "data": {}},
            {"id": "3", "type": "notify", "data": {}},
        ],
        edges=[_edge("1", "2"), _edge("1", "3")],  # "1" не развилка, но 2 исходящих ребра
    )
    assert parse_graph(graph) is None


# ---------- parse_graph: ветвление (Этап 3) ----------

def test_parse_graph_valid_branching_ack_check():
    graph = _graph(
        nodes=[
            {"id": "cond", "type": "condition", "data": {}},
            {"id": "ack", "type": "ack_check", "data": {}},
            {"id": "yes-notify", "type": "notify", "data": {"employee_id": 1}},
            {"id": "no-notify", "type": "notify", "data": {"employee_id": 2}},
        ],
        edges=[
            _edge("cond", "ack"),
            _edge("ack", "yes-notify", "yes"),
            _edge("ack", "no-notify", "no"),
        ],
    )
    parsed = parse_graph(graph)
    assert parsed is not None
    assert parsed.edges["ack"] == {"yes": "yes-notify", "no": "no-notify"}


def test_parse_graph_rejects_decision_node_with_duplicate_handle():
    graph = _graph(
        nodes=[
            {"id": "cond", "type": "condition", "data": {}},
            {"id": "ack", "type": "ack_check", "data": {}},
            {"id": "a", "type": "notify", "data": {}},
            {"id": "b", "type": "notify", "data": {}},
        ],
        edges=[_edge("cond", "ack"), _edge("ack", "a", "yes"), _edge("ack", "b", "yes")],
    )
    assert parse_graph(graph) is None


def test_parse_graph_rejects_decision_node_edge_without_handle():
    graph = _graph(
        nodes=[
            {"id": "cond", "type": "condition", "data": {}},
            {"id": "ack", "type": "ack_check", "data": {}},
            {"id": "a", "type": "notify", "data": {}},
        ],
        edges=[_edge("cond", "ack"), _edge("ack", "a")],  # без sourceHandle
    )
    assert parse_graph(graph) is None


def test_parse_graph_rejects_true_cycle():
    graph = _graph(
        nodes=[
            {"id": "cond", "type": "condition", "data": {}},
            {"id": "wait1", "type": "wait", "data": {"minutes": 5}},
            {"id": "wait2", "type": "wait", "data": {"minutes": 5}},
        ],
        edges=[_edge("cond", "wait1"), _edge("wait1", "wait2"), _edge("wait2", "wait1")],
    )
    assert parse_graph(graph) is None


def test_parse_graph_allows_branches_to_converge():
    """Diamond-паттерн из референса пользователя: обе ветки развилки
    сходятся в одном общем узле-эскалации — это НЕ цикл (чёрный узел),
    должно проходить валидацию."""
    graph = _graph(
        nodes=[
            {"id": "cond", "type": "condition", "data": {}},
            {"id": "ack", "type": "ack_check", "data": {}},
            {"id": "wait-a", "type": "wait", "data": {"minutes": 1}},
            {"id": "wait-b", "type": "wait", "data": {"minutes": 2}},
            {"id": "escalate", "type": "notify", "data": {"employee_id": 9}},
        ],
        edges=[
            _edge("cond", "ack"),
            _edge("ack", "wait-a", "yes"),
            _edge("ack", "wait-b", "no"),
            _edge("wait-a", "escalate"),
            _edge("wait-b", "escalate"),
        ],
    )
    parsed = parse_graph(graph)
    assert parsed is not None
    assert parsed.edges["wait-a"]["default"] == "escalate"
    assert parsed.edges["wait-b"]["default"] == "escalate"


# ---------- matches_condition (без изменений по семантике) ----------

def test_matches_condition_empty_matches_everything():
    assert matches_condition({}, _problem(), owning_subsidiaries={"gpn-noyabrsk"}) is True


def test_matches_condition_priority_min_filters_lower_priority():
    condition = {"priority_min": "P1"}
    assert matches_condition(condition, _problem(priority="P0"), set()) is True
    assert matches_condition(condition, _problem(priority="P2"), set()) is False


def test_matches_condition_subsidiary_and_symptom_filters():
    assert matches_condition({"subsidiary": "gpn-noyabrsk"}, _problem(), {"gpn-noyabrsk"}) is True
    assert matches_condition({"subsidiary": "gpn-noyabrsk"}, _problem(), {"gpn-khantos"}) is False
    assert matches_condition({"symptom_class": "power_loss"}, _problem(symptom_class="power_loss"), set()) is True
    assert matches_condition({"symptom_class": "power_loss"}, _problem(symptom_class="node_down"), set()) is False


# ---------- advance(): линейный случай (наследие Этапа 2) ----------

LINEAR = parse_graph(_graph(
    nodes=[
        {"id": "cond", "type": "condition", "data": {}},
        {"id": "n1", "type": "notify", "data": {"employee_id": 1}},
        {"id": "w1", "type": "wait", "data": {"minutes": 30}},
        {"id": "n2", "type": "notify", "data": {"employee_id": 2}},
    ],
    edges=[_edge("cond", "n1"), _edge("n1", "w1"), _edge("w1", "n2")],
))


def test_advance_fresh_run_sends_first_notify():
    outcome, step, node_id, entered_at = advance("cond", T0, LINEAR, "OPEN", {}, T0)
    assert outcome == "notify"
    assert step["employee_id"] == 1
    assert node_id == "w1"
    assert entered_at == T0


def test_advance_waits_before_deadline():
    outcome, step, node_id, entered_at = advance("w1", T0, LINEAR, "OPEN", {}, T0 + timedelta(minutes=10))
    assert outcome == "wait"
    assert node_id == "w1"
    assert entered_at == T0


def test_advance_escalates_after_deadline():
    now = T0 + timedelta(minutes=31)
    outcome, step, node_id, entered_at = advance("w1", T0, LINEAR, "OPEN", {}, now)
    assert outcome == "notify"
    assert step["employee_id"] == 2
    assert node_id is None
    assert entered_at == now


def test_advance_done_when_problem_resolved_before_escalation():
    outcome, step, _, _ = advance("w1", T0, LINEAR, "RESOLVED", {}, T0 + timedelta(minutes=10))
    assert outcome == "done"
    assert step is None


def test_advance_done_when_wait_node_has_no_outgoing_edge():
    dead_end = parse_graph(_graph(
        nodes=[{"id": "cond", "type": "condition", "data": {}}, {"id": "w", "type": "wait", "data": {"minutes": 5}}],
        edges=[_edge("cond", "w")],
    ))
    outcome, step, node_id, _ = advance("w", T0, dead_end, "OPEN", {}, T0 + timedelta(minutes=6))
    assert outcome == "done"
    assert step is None


# ---------- advance(): ветвление ----------

BRANCHING = parse_graph(_graph(
    nodes=[
        {"id": "cond", "type": "condition", "data": {}},
        {"id": "ack", "type": "ack_check", "data": {}},
        {"id": "yes-notify", "type": "notify", "data": {"employee_id": 1}},
        {"id": "wait-escalate", "type": "wait", "data": {"minutes": 15}},
        {"id": "no-notify", "type": "notify", "data": {"employee_id": 2}},
    ],
    edges=[
        _edge("cond", "ack"),
        _edge("ack", "yes-notify", "yes"),
        _edge("ack", "wait-escalate", "no"),
        _edge("wait-escalate", "no-notify"),
    ],
))


def test_advance_takes_yes_branch_in_same_tick():
    outcome, step, node_id, entered_at = advance("cond", T0, BRANCHING, "OPEN", {"ack": True}, T0)
    assert outcome == "notify"
    assert step["employee_id"] == 1
    assert node_id is None  # у "yes-notify" нет исходящего ребра


def test_advance_takes_no_branch_and_then_waits():
    outcome, step, node_id, entered_at = advance("cond", T0, BRANCHING, "OPEN", {"ack": False}, T0)
    assert outcome == "wait"
    assert node_id == "wait-escalate"
    assert entered_at == T0


def test_advance_missing_fact_defaults_to_no_branch():
    outcome, step, node_id, entered_at = advance("cond", T0, BRANCHING, "OPEN", {}, T0)
    assert outcome == "wait"
    assert node_id == "wait-escalate"


def test_advance_no_branch_escalates_after_wait():
    now = T0 + timedelta(minutes=16)
    outcome, step, node_id, entered_at = advance("wait-escalate", T0, BRANCHING, "OPEN", {}, now)
    assert outcome == "notify"
    assert step["employee_id"] == 2
    assert node_id is None


def test_advance_multiple_decisions_in_one_tick():
    graph = parse_graph(_graph(
        nodes=[
            {"id": "cond", "type": "condition", "data": {}},
            {"id": "d1", "type": "ack_check", "data": {}},
            {"id": "d2", "type": "subscription_check", "data": {}},
            {"id": "final", "type": "notify", "data": {"employee_id": 7}},
            {"id": "dead-end", "type": "notify", "data": {"employee_id": 8}},
        ],
        edges=[
            _edge("cond", "d1"),
            _edge("d1", "d2", "yes"),
            _edge("d1", "dead-end", "no"),
            _edge("d2", "final", "yes"),
            _edge("d2", "dead-end", "no"),
        ],
    ))
    outcome, step, node_id, _ = advance("cond", T0, graph, "OPEN", {"d1": True, "d2": True}, T0)
    assert outcome == "notify"
    assert step["employee_id"] == 7
