package adminapi

import "testing"

func TestCompileFilterNodeLeaf(t *testing.T) {
	args := make([]any, 0)
	sql, err := compileFilterNode(FilterNode{Field: "priority", Op: "in", Value: []string{"P0", "P1"}}, &args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "problem.priority = ANY($1)" {
		t.Fatalf("unexpected sql: %s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
}

func TestCompileFilterNodeGroupAllAny(t *testing.T) {
	args := make([]any, 0)
	node := FilterNode{
		Match: "all",
		Conditions: []FilterNode{
			{Field: "priority", Op: "in", Value: []string{"P0", "P1"}},
			{
				Match: "any",
				Conditions: []FilterNode{
					{Field: "reaction", Op: "in", Value: []string{"no_reaction"}},
					{Field: "reaction", Op: "in", Value: []string{"escalated"}},
				},
			},
		},
	}
	sql, err := compileFilterNode(node, &args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "(problem.priority = ANY($1) AND (((problem.acknowledged_at IS NULL AND problem.status IN ('OPEN','FLAPPING'))) OR (EXISTS (\n\t\tSELECT 1 FROM scenario_run_steps srs JOIN scenario_runs sr ON sr.id = srs.run_id\n\t\tWHERE sr.problem_id = problem.id AND srs.node_type = 'notify'\n\t\tGROUP BY sr.id HAVING count(*) > 1\n\t))))"
	if sql != want {
		t.Fatalf("unexpected sql:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileFilterNodeUnknownField(t *testing.T) {
	args := make([]any, 0)
	if _, err := compileFilterNode(FilterNode{Field: "password", Op: "eq", Value: "x"}, &args); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestCompileFilterNodeUnknownOp(t *testing.T) {
	args := make([]any, 0)
	if _, err := compileFilterNode(FilterNode{Field: "priority", Op: "gt", Value: "P0"}, &args); err == nil {
		t.Fatal("expected error for disallowed op")
	}
}

func TestCompileFilterNodeUnknownStatusValue(t *testing.T) {
	args := make([]any, 0)
	if _, err := compileFilterNode(FilterNode{Field: "status", Op: "in", Value: []string{"bogus"}}, &args); err == nil {
		t.Fatal("expected error for unknown status enum value")
	}
}

func TestCompileFilterNodeHasIncident(t *testing.T) {
	args := make([]any, 0)
	sql, err := compileFilterNode(FilterNode{Field: "has_incident", Op: "eq", Value: true}, &args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "problem.incident_id IS NOT NULL" {
		t.Fatalf("unexpected sql: %s", sql)
	}
	if len(args) != 0 {
		t.Fatalf("expected no bound args for has_incident, got %d", len(args))
	}
}

func TestCompileFilterNodeEmptyGroupIsNoop(t *testing.T) {
	args := make([]any, 0)
	sql, err := compileFilterNode(FilterNode{Match: "all"}, &args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "" {
		t.Fatalf("expected empty sql for empty group, got %q", sql)
	}
}
