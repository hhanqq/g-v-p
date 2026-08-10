package availability

import (
	"testing"
	"time"
)

func TestOutranksByPriorityRegardlessOfRecency(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	override := intervalRow{id: 1, kind: "override_unavailable", createdAt: older}
	vacation := intervalRow{id: 2, kind: "vacation", createdAt: newer}
	if !outranks(override, vacation) {
		t.Fatal("override_unavailable (rank 100) should outrank a newer vacation (rank 85)")
	}
	if outranks(vacation, override) {
		t.Fatal("vacation should not outrank override_unavailable even though it's newer")
	}
}

func TestOutranksTiesByCreatedAtThenID(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := intervalRow{id: 5, kind: "shift", createdAt: t0}
	b := intervalRow{id: 6, kind: "shift", createdAt: t0.Add(time.Minute)}
	if outranks(a, b) {
		t.Fatal("older row of equal rank should not outrank a newer one")
	}
	if !outranks(b, a) {
		t.Fatal("newer row of equal rank should outrank an older one")
	}
	c := intervalRow{id: 7, kind: "shift", createdAt: t0}
	d := intervalRow{id: 8, kind: "shift", createdAt: t0}
	if outranks(c, d) {
		t.Fatal("equal rank and timestamp should tie-break on lower id losing")
	}
	if !outranks(d, c) {
		t.Fatal("equal rank and timestamp should tie-break on higher id winning")
	}
}

func TestPriorityTableExhaustive(t *testing.T) {
	cases := []struct {
		kind      string
		available bool
	}{
		{"override_available", true},
		{"override_unavailable", false},
		{"sick_leave", false},
		{"vacation", false},
		{"delegation", false},
		{"shift", true},
		{"on_call", true},
		{"unavailable", false},
		{"available", true},
	}
	seenRanks := make(map[int]string)
	for _, c := range cases {
		rule, ok := priorities[c.kind]
		if !ok {
			t.Fatalf("missing priority rule for kind=%q", c.kind)
		}
		if rule.available != c.available {
			t.Fatalf("kind=%q: available=%v, want %v", c.kind, rule.available, c.available)
		}
		if c.kind != "override_available" && c.kind != "override_unavailable" {
			if existing, dup := seenRanks[rule.rank]; dup {
				t.Fatalf("kind=%q shares rank %d with %q — ranks must be unique except the two overrides", c.kind, rule.rank, existing)
			}
			seenRanks[rule.rank] = c.kind
		}
	}
	if len(priorities) != len(cases) {
		t.Fatalf("priorities table has %d entries, test covers %d — keep them in sync", len(priorities), len(cases))
	}
}

func TestResolveDefaultsToAvailableWithNoSubscribers(t *testing.T) {
	result, err := Resolve(nil, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error for empty subscriber list: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty result map, got %+v", result)
	}
}
