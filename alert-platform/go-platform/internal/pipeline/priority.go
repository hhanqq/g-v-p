package pipeline

import (
	"fmt"
	"slices"
	"time"
)

var priorityOrder = []string{"P0", "P1", "P2", "P3"}

func computePriority(cfg priorityConfig, technicalSeverity, businessImpact, repeatCount int, occurredAt time.Time, maintenanceActive bool) (string, PriorityBreakdown) {
	ts := clamp(technicalSeverity, 0, 5)
	bi := clamp(businessImpact, 0, 5)
	base := cfg.Matrix[ts][bi]
	index := slices.Index(priorityOrder, base)
	if index < 0 {
		index = len(priorityOrder) - 1
	}
	applied := make([]string, 0)
	if repeatCount > cfg.Modifiers.RepeatThreshold {
		index = clamp(index-cfg.Modifiers.RepeatBump, 0, len(priorityOrder)-1)
		applied = append(applied, fmt.Sprintf("повторяемость > %d (+%d)", cfg.Modifiers.RepeatThreshold, cfg.Modifiers.RepeatBump))
	}
	if slices.Contains(cfg.Modifiers.NightHours, occurredAt.Hour()) {
		index = clamp(index-cfg.Modifiers.NightBump, 0, len(priorityOrder)-1)
		applied = append(applied, "ночное время")
	}
	if maintenanceActive {
		index = clamp(index+cfg.Modifiers.MaintenanceActiveDebump, 0, len(priorityOrder)-1)
		applied = append(applied, "активное окно плановых работ")
	}
	priority := priorityOrder[index]
	return priority, PriorityBreakdown{
		TechnicalSeverity: ts, BusinessImpact: bi, MatrixCell: base,
		ModifiersApplied: applied, FinalPriority: priority,
	}
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
