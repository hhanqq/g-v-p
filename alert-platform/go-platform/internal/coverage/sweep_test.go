package coverage

import (
	"testing"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
)

func TestSweepBreakpointsIncludesRangeAndInteriorBoundaries(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	midEnd := time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)
	intervals := []availability.Interval{
		{SubscriberID: 1, Kind: "vacation", ValidFrom: mid, ValidUntil: &midEnd},
	}
	points := sweepBreakpoints(intervals, from, to)
	want := []time.Time{from, mid, midEnd, to}
	if len(points) != len(want) {
		t.Fatalf("got %d breakpoints, want %d: %v", len(points), len(want), points)
	}
	for i, w := range want {
		if !points[i].Equal(w) {
			t.Fatalf("breakpoint[%d] = %v, want %v", i, points[i], w)
		}
	}
}

func TestSweepBreakpointsIgnoresBoundariesOutsideRange(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	before := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	intervals := []availability.Interval{
		{SubscriberID: 1, Kind: "vacation", ValidFrom: before, ValidUntil: &after},
	}
	points := sweepBreakpoints(intervals, from, to)
	if len(points) != 2 {
		t.Fatalf("expected only [from, to] since the interval's own boundaries fall outside the range: %v", points)
	}
}
