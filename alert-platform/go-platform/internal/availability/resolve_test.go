package availability

import (
	"testing"
	"time"
)

func TestOutranksByPriorityRegardlessOfRecency(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	override := Interval{ID: 1, Kind: "override_unavailable", CreatedAt: older}
	vacation := Interval{ID: 2, Kind: "vacation", CreatedAt: newer}
	if !outranks(override, vacation) {
		t.Fatal("override_unavailable (rank 100) should outrank a newer vacation (rank 85)")
	}
	if outranks(vacation, override) {
		t.Fatal("vacation should not outrank override_unavailable even though it's newer")
	}
}

func TestOutranksTiesByCreatedAtThenID(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := Interval{ID: 5, Kind: "shift", CreatedAt: t0}
	b := Interval{ID: 6, Kind: "shift", CreatedAt: t0.Add(time.Minute)}
	if outranks(a, b) {
		t.Fatal("older row of equal rank should not outrank a newer one")
	}
	if !outranks(b, a) {
		t.Fatal("newer row of equal rank should outrank an older one")
	}
	c := Interval{ID: 7, Kind: "shift", CreatedAt: t0}
	d := Interval{ID: 8, Kind: "shift", CreatedAt: t0}
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

func TestResolveFromIntervalsHonorsValidFromAndValidUntil(t *testing.T) {
	t0 := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	until := t0.Add(24 * time.Hour)
	intervals := []Interval{
		{ID: 1, SubscriberID: 42, Kind: "vacation", ValidFrom: t0, ValidUntil: &until, CreatedAt: t0},
	}
	before := ResolveFromIntervals(intervals, []int64{42}, t0.Add(-time.Hour))
	if !before[42].Available {
		t.Fatalf("subscriber should be available before the interval starts: %+v", before[42])
	}
	during := ResolveFromIntervals(intervals, []int64{42}, t0.Add(time.Hour))
	if during[42].Available {
		t.Fatalf("subscriber should be unavailable during the vacation interval: %+v", during[42])
	}
	after := ResolveFromIntervals(intervals, []int64{42}, until.Add(time.Hour))
	if !after[42].Available {
		t.Fatalf("subscriber should be available again after the interval ends: %+v", after[42])
	}
}

func TestResolveFromIntervalsInjectsHypotheticalCandidate(t *testing.T) {
	// coverage.Sweep's dry-run injects a not-yet-saved interval alongside
	// real ones — this is the exact mechanism it relies on.
	t0 := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	until := t0.Add(24 * time.Hour)
	real := Interval{ID: 1, SubscriberID: 7, Kind: "available", ValidFrom: t0.Add(-time.Hour), CreatedAt: t0.Add(-time.Hour)}
	hypothetical := Interval{ID: -1, SubscriberID: 7, Kind: "vacation", ValidFrom: t0, ValidUntil: &until, CreatedAt: t0}
	result := ResolveFromIntervals([]Interval{real, hypothetical}, []int64{7}, t0.Add(time.Hour))
	if result[7].Available {
		t.Fatalf("higher-priority hypothetical vacation should win over the real 'available' row: %+v", result[7])
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
