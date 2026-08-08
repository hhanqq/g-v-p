"""Тесты исполнения сценариев (раздел «Сценарии», Этап 2) — чистая
логика packages/scenarios/engine.py, без БД/бота (как packages/ai/*)."""
import json
from datetime import datetime, timedelta

from packages.scenarios.engine import matches_condition, next_action, parse_chain

T0 = datetime(2026, 8, 6, 10, 0, 0)


def _problem(priority="P1", symptom_class="node_down"):
    from packages.models.db import Problem
    return Problem(dedup_key="x", status="OPEN", object_id="brd-noyabrsk/rig-01",
                    symptom_class=symptom_class, site="brd-noyabrsk", opened_at=T0,
                    last_seen_at=T0, repeat_count=1, toggle_count=0, priority=priority)


def _graph(nodes, edges):
    return json.dumps({"nodes": nodes, "edges": edges})


# ---------- parse_chain ----------

def test_parse_chain_valid_linear_chain():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {"priority_min": "P1"}},
            {"id": "2", "type": "notify", "data": {"employee_id": 5}},
            {"id": "3", "type": "wait", "data": {"minutes": 30}},
            {"id": "4", "type": "notify", "data": {"employee_id": 6}},
        ],
        edges=[{"source": "1", "target": "2"}, {"source": "2", "target": "3"}, {"source": "3", "target": "4"}],
    )
    chain = parse_chain(graph)
    assert chain is not None
    assert [s["type"] for s in chain] == ["condition", "notify", "wait", "notify"]
    assert chain[1]["employee_id"] == 5


def test_parse_chain_rejects_empty_graph():
    assert parse_chain(_graph([], [])) is None


def test_parse_chain_rejects_invalid_json():
    assert parse_chain("not json") is None


def test_parse_chain_rejects_branching():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {}},
            {"id": "2", "type": "notify", "data": {}},
            {"id": "3", "type": "notify", "data": {}},
        ],
        edges=[{"source": "1", "target": "2"}, {"source": "1", "target": "3"}],
    )
    assert parse_chain(graph) is None


def test_parse_chain_rejects_cycle():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {}},
            {"id": "2", "type": "notify", "data": {}},
        ],
        edges=[{"source": "1", "target": "2"}, {"source": "2", "target": "1"}],
    )
    assert parse_chain(graph) is None


def test_parse_chain_rejects_multiple_roots():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {}},
            {"id": "2", "type": "condition", "data": {}},
        ],
        edges=[],
    )
    assert parse_chain(graph) is None


def test_parse_chain_rejects_isolated_node():
    graph = _graph(
        nodes=[
            {"id": "1", "type": "condition", "data": {}},
            {"id": "2", "type": "notify", "data": {}},
            {"id": "3", "type": "notify", "data": {}},  # изолирован — не связан рёбрами
        ],
        edges=[{"source": "1", "target": "2"}],
    )
    assert parse_chain(graph) is None


def test_parse_chain_rejects_when_first_node_not_condition():
    graph = _graph(
        nodes=[{"id": "1", "type": "notify", "data": {}}],
        edges=[],
    )
    assert parse_chain(graph) is None


def test_parse_chain_rejects_unknown_node_type():
    graph = _graph(
        nodes=[{"id": "1", "type": "condition", "data": {}}, {"id": "2", "type": "mystery", "data": {}}],
        edges=[{"source": "1", "target": "2"}],
    )
    assert parse_chain(graph) is None


# ---------- matches_condition ----------

def test_matches_condition_empty_matches_everything():
    assert matches_condition({}, _problem(), owning_subsidiaries={"gpn-noyabrsk"}) is True


def test_matches_condition_priority_min_filters_lower_priority():
    condition = {"priority_min": "P1"}
    assert matches_condition(condition, _problem(priority="P0"), set()) is True
    assert matches_condition(condition, _problem(priority="P1"), set()) is True
    assert matches_condition(condition, _problem(priority="P2"), set()) is False


def test_matches_condition_subsidiary_filter():
    condition = {"subsidiary": "gpn-noyabrsk"}
    assert matches_condition(condition, _problem(), owning_subsidiaries={"gpn-noyabrsk"}) is True
    assert matches_condition(condition, _problem(), owning_subsidiaries={"gpn-khantos"}) is False


def test_matches_condition_symptom_class_filter():
    condition = {"symptom_class": "power_loss"}
    assert matches_condition(condition, _problem(symptom_class="power_loss"), set()) is True
    assert matches_condition(condition, _problem(symptom_class="node_down"), set()) is False


# ---------- next_action ----------

CHAIN = [
    {"type": "condition"},
    {"type": "notify", "employee_id": 1},
    {"type": "wait", "minutes": 30},
    {"type": "notify", "employee_id": 2},
]


def test_next_action_fresh_run_sends_first_notify():
    outcome, step, index, entered_at = next_action(0, T0, CHAIN, "OPEN", T0)
    assert outcome == "notify"
    assert step["employee_id"] == 1
    assert index == 2  # указывает на "wait" — следующий шаг после уведомления
    assert entered_at == T0


def test_next_action_waits_before_deadline():
    outcome, step, index, entered_at = next_action(2, T0, CHAIN, "OPEN", T0 + timedelta(minutes=10))
    assert outcome == "wait"
    assert step is None
    assert index == 2
    assert entered_at == T0


def test_next_action_escalates_after_deadline():
    now = T0 + timedelta(minutes=31)
    outcome, step, index, entered_at = next_action(2, T0, CHAIN, "OPEN", now)
    assert outcome == "notify"
    assert step["employee_id"] == 2
    assert index == 4
    assert entered_at == now


def test_next_action_done_when_problem_resolved_before_escalation():
    outcome, step, index, entered_at = next_action(2, T0, CHAIN, "RESOLVED", T0 + timedelta(minutes=10))
    assert outcome == "done"
    assert step is None


def test_next_action_done_at_end_of_chain():
    outcome, step, index, entered_at = next_action(4, T0, CHAIN, "OPEN", T0)
    assert outcome == "done"


def test_next_action_short_chain_without_escalation_is_done_after_single_notify():
    chain = [{"type": "condition"}, {"type": "notify", "employee_id": 1}]
    outcome, step, index, entered_at = next_action(0, T0, chain, "OPEN", T0)
    assert outcome == "notify"
    assert index == 2
    outcome2, _, _, _ = next_action(index, entered_at, chain, "OPEN", T0 + timedelta(minutes=5))
    assert outcome2 == "done"
