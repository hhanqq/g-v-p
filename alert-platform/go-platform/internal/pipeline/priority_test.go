package pipeline

import (
	"testing"
	"time"
)

func TestPriorityModifiersAndClamping(t *testing.T) {
	cfg, err := LoadPriorityConfig(projectPath(t, "packages", "rules", "priority_matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	night := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	priority, breakdown := computePriority(cfg, 99, -5, 1, day, false)
	if breakdown.TechnicalSeverity != 5 || breakdown.BusinessImpact != 0 || priority != "P2" {
		t.Fatalf("unexpected clamped priority: %s %#v", priority, breakdown)
	}
	dayPriority, _ := computePriority(cfg, 2, 2, 1, day, false)
	nightPriority, nightBreakdown := computePriority(cfg, 2, 2, 50, night, false)
	if dayPriority == nightPriority || len(nightBreakdown.ModifiersApplied) != 2 {
		t.Fatalf("modifiers were not applied: day=%s night=%s %#v", dayPriority, nightPriority, nightBreakdown)
	}
}

func TestSimilarityRatioSupportsFuzzyResolver(t *testing.T) {
	ratio := similarityRatio("app-01x", "app-01")
	if ratio < fuzzyCutoff || ratio >= 1 {
		t.Fatalf("unexpected similarity ratio: %f", ratio)
	}
}
