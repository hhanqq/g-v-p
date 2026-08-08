package scenario

import (
	"testing"
	"time"
)

// ---------- Parse: базовые случаи (наследие линейной цепочки) ----------

func TestParseValidLinearChain(t *testing.T) {
	raw := `{"nodes":[{"id":"1","type":"condition","data":{}},{"id":"2","type":"notify","data":{"employee_id":5}},{"id":"3","type":"wait","data":{"minutes":30}},{"id":"4","type":"notify","data":{"employee_id":6}}],"edges":[{"source":"1","target":"2"},{"source":"2","target":"3"},{"source":"3","target":"4"}]}`
	graph, ok := Parse(raw)
	if !ok {
		t.Fatal("valid linear chain rejected")
	}
	if graph.RootID != "1" {
		t.Fatalf("unexpected root: %s", graph.RootID)
	}
	if *graph.Edges["1"]["default"] != "2" {
		t.Fatalf("unexpected default edge from root")
	}
}

func TestParseRejectsEmptyGraph(t *testing.T) {
	if _, ok := Parse(`{"nodes":[],"edges":[]}`); ok {
		t.Fatal("empty graph accepted")
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	if _, ok := Parse("not json"); ok {
		t.Fatal("invalid json accepted")
	}
}

func TestParseRejectsMultipleRoots(t *testing.T) {
	raw := `{"nodes":[{"id":"1","type":"condition","data":{}},{"id":"2","type":"condition","data":{}}],"edges":[]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("graph with two roots accepted")
	}
}

func TestParseRejectsIsolatedNode(t *testing.T) {
	raw := `{"nodes":[{"id":"1","type":"condition","data":{}},{"id":"2","type":"notify","data":{}},{"id":"3","type":"notify","data":{}}],"edges":[{"source":"1","target":"2"}]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("graph with isolated node accepted")
	}
}

func TestParseRejectsRootNotCondition(t *testing.T) {
	raw := `{"nodes":[{"id":"1","type":"notify","data":{}}],"edges":[]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("graph with non-condition root accepted")
	}
}

func TestParseRejectsUnknownNodeType(t *testing.T) {
	raw := `{"nodes":[{"id":"1","type":"condition","data":{}},{"id":"2","type":"mystery","data":{}}],"edges":[{"source":"1","target":"2"}]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("graph with unknown node type accepted")
	}
}

func TestParseRejectsNonDecisionNodeWithTwoOutgoingEdges(t *testing.T) {
	raw := `{"nodes":[{"id":"1","type":"condition","data":{}},{"id":"2","type":"notify","data":{}},{"id":"3","type":"notify","data":{}}],"edges":[{"source":"1","target":"2"},{"source":"1","target":"3"}]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("non-decision node with two outgoing edges accepted")
	}
}

// ---------- Parse: ветвление ----------

func TestParseAllowsBranchesAndRejectsCycles(t *testing.T) {
	valid := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"a","type":"ack_check","data":{}},{"id":"n","type":"notify","data":{}}],"edges":[{"source":"c","target":"a"},{"source":"a","target":"n","sourceHandle":"yes"}]}`
	if _, ok := Parse(valid); !ok {
		t.Fatal("valid branched graph rejected")
	}
	cyclic := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"n","type":"notify","data":{}}],"edges":[{"source":"c","target":"n"},{"source":"n","target":"c"}]}`
	if _, ok := Parse(cyclic); ok {
		t.Fatal("cyclic graph accepted")
	}
}

func TestParseRejectsDecisionNodeWithDuplicateHandle(t *testing.T) {
	raw := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"a","type":"ack_check","data":{}},{"id":"x","type":"notify","data":{}},{"id":"y","type":"notify","data":{}}],"edges":[{"source":"c","target":"a"},{"source":"a","target":"x","sourceHandle":"yes"},{"source":"a","target":"y","sourceHandle":"yes"}]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("decision node with duplicate handle accepted")
	}
}

func TestParseRejectsDecisionEdgeWithoutHandle(t *testing.T) {
	raw := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"a","type":"ack_check","data":{}},{"id":"x","type":"notify","data":{}}],"edges":[{"source":"c","target":"a"},{"source":"a","target":"x"}]}`
	if _, ok := Parse(raw); ok {
		t.Fatal("decision edge without sourceHandle accepted")
	}
}

func TestParseAllowsBranchesToConverge(t *testing.T) {
	// Diamond-паттерн: обе ветки развилки сходятся в одном общем узле —
	// это НЕ цикл (чёрный узел при повторном посещении), должно проходить.
	raw := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"a","type":"ack_check","data":{}},{"id":"w1","type":"wait","data":{"minutes":1}},{"id":"w2","type":"wait","data":{"minutes":2}},{"id":"e","type":"notify","data":{"employee_id":9}}],"edges":[{"source":"c","target":"a"},{"source":"a","target":"w1","sourceHandle":"yes"},{"source":"a","target":"w2","sourceHandle":"no"},{"source":"w1","target":"e"},{"source":"w2","target":"e"}]}`
	graph, ok := Parse(raw)
	if !ok {
		t.Fatal("diamond-shaped graph incorrectly rejected as a cycle")
	}
	if *graph.Edges["w1"]["default"] != "e" || *graph.Edges["w2"]["default"] != "e" {
		t.Fatal("converging edges not preserved")
	}
}

// ---------- Advance: линейный случай ----------

func linearGraph(t *testing.T) *Graph {
	raw := `{"nodes":[{"id":"cond","type":"condition","data":{}},{"id":"n1","type":"notify","data":{"employee_id":1}},{"id":"w1","type":"wait","data":{"minutes":30}},{"id":"n2","type":"notify","data":{"employee_id":2}}],"edges":[{"source":"cond","target":"n1"},{"source":"n1","target":"w1"},{"source":"w1","target":"n2"}]}`
	graph, ok := Parse(raw)
	if !ok {
		t.Fatal("failed to parse linear fixture graph")
	}
	return graph
}

func TestAdvanceFreshRunSendsFirstNotify(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := linearGraph(t)
	out := Advance("cond", now, graph, "OPEN", nil, now)
	if out.Kind != "notify" || out.CurrentNodeID != "w1" {
		t.Fatalf("unexpected outcome: %+v", out)
	}
}

func TestAdvanceWaitsBeforeDeadline(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := linearGraph(t)
	out := Advance("w1", t0, graph, "OPEN", nil, t0.Add(10*time.Minute))
	if out.Kind != "wait" || out.CurrentNodeID != "w1" {
		t.Fatalf("unexpected outcome: %+v", out)
	}
}

func TestAdvanceEscalatesAfterDeadline(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := linearGraph(t)
	now := t0.Add(31 * time.Minute)
	out := Advance("w1", t0, graph, "OPEN", nil, now)
	if out.Kind != "notify" || out.CurrentNodeID != "" {
		t.Fatalf("unexpected outcome: %+v", out)
	}
}

func TestAdvanceDoneWhenProblemResolvedBeforeEscalation(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := linearGraph(t)
	out := Advance("w1", t0, graph, "RESOLVED", nil, t0.Add(10*time.Minute))
	if out.Kind != "done" {
		t.Fatalf("expected done, got: %+v", out)
	}
}

// ---------- Advance: ветвление ----------

func branchingGraph(t *testing.T) *Graph {
	raw := `{"nodes":[{"id":"cond","type":"condition","data":{}},{"id":"ack","type":"ack_check","data":{}},{"id":"yes-notify","type":"notify","data":{"employee_id":1}},{"id":"wait-escalate","type":"wait","data":{"minutes":15}},{"id":"no-notify","type":"notify","data":{"employee_id":2}}],"edges":[{"source":"cond","target":"ack"},{"source":"ack","target":"yes-notify","sourceHandle":"yes"},{"source":"ack","target":"wait-escalate","sourceHandle":"no"},{"source":"wait-escalate","target":"no-notify"}]}`
	graph, ok := Parse(raw)
	if !ok {
		t.Fatal("failed to parse branching fixture graph")
	}
	return graph
}

func TestAdvanceTakesYesBranchInSameTick(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := branchingGraph(t)
	out := Advance("cond", t0, graph, "OPEN", map[string]bool{"ack": true}, t0)
	if out.Kind != "notify" || out.CurrentNodeID != "" {
		t.Fatalf("unexpected outcome on yes-branch: %+v", out)
	}
}

func TestAdvanceTakesNoBranchAndWaits(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := branchingGraph(t)
	out := Advance("cond", t0, graph, "OPEN", map[string]bool{"ack": false}, t0)
	if out.Kind != "wait" || out.CurrentNodeID != "wait-escalate" {
		t.Fatalf("unexpected outcome on no-branch: %+v", out)
	}
}

func TestAdvanceMissingFactDefaultsToNoBranch(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	graph := branchingGraph(t)
	out := Advance("cond", t0, graph, "OPEN", map[string]bool{}, t0)
	if out.Kind != "wait" || out.CurrentNodeID != "wait-escalate" {
		t.Fatalf("missing fact should default to false/no: %+v", out)
	}
}

func TestAdvanceMultipleDecisionsInOneTick(t *testing.T) {
	raw := `{"nodes":[{"id":"cond","type":"condition","data":{}},{"id":"d1","type":"ack_check","data":{}},{"id":"d2","type":"subscription_check","data":{}},{"id":"final","type":"notify","data":{"employee_id":7}},{"id":"dead","type":"notify","data":{"employee_id":8}}],"edges":[{"source":"cond","target":"d1"},{"source":"d1","target":"d2","sourceHandle":"yes"},{"source":"d1","target":"dead","sourceHandle":"no"},{"source":"d2","target":"final","sourceHandle":"yes"},{"source":"d2","target":"dead","sourceHandle":"no"}]}`
	graph, ok := Parse(raw)
	if !ok {
		t.Fatal("failed to parse multi-decision fixture graph")
	}
	t0 := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	out := Advance("cond", t0, graph, "OPEN", map[string]bool{"d1": true, "d2": true}, t0)
	if out.Kind != "notify" || out.Step == nil || out.Step.ID != "final" {
		t.Fatalf("expected to walk through both decisions to 'final' in one tick: %+v", out)
	}
}
