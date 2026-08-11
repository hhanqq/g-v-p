package adminapi

import (
	"net/http"
)

// analyticsScenarios — GET /api/analytics/scenarios?period=. Раздел
// VIII.39 ТЗ: запуски/завершения/эскалации сценариев на реальных
// scenario_runs/scenario_run_steps (та же трасса, что уже строит
// ScenarioStats.tsx для одного сценария, здесь — сводка по всем).
func (server *Server) analyticsScenarios(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)

	var totalRuns, doneRuns, noRecipientRuns int
	err := server.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status='done'), count(*) FILTER (WHERE status='no_recipient')
		FROM scenario_runs WHERE created_at >= $1 AND created_at < $2`, rng.From, rng.To,
	).Scan(&totalRuns, &doneRuns, &noRecipientRuns)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var escalatedRuns int
	err = server.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT run_id FROM scenario_run_steps step
			JOIN scenario_runs run ON run.id = step.run_id
			WHERE step.node_type='notify' AND run.created_at >= $1 AND run.created_at < $2
			GROUP BY run_id HAVING count(*) > 1
		) escalated`, rng.From, rng.To,
	).Scan(&escalatedRuns)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	avgSteps, err := queryNullableFloat(ctx, server.pool, `
		SELECT AVG(step_count) FROM (
			SELECT run_id, count(*) AS step_count FROM scenario_run_steps step
			JOIN scenario_runs run ON run.id = step.run_id
			WHERE run.created_at >= $1 AND run.created_at < $2
			GROUP BY run_id
		) counted`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	topScenarios := make([]map[string]any, 0)
	rows, err := server.pool.Query(ctx, `
		SELECT scenario.name, count(*) FROM scenario_runs run
		JOIN scenarios scenario ON scenario.id = run.scenario_id
		WHERE run.created_at >= $1 AND run.created_at < $2
		GROUP BY scenario.name ORDER BY count(*) DESC LIMIT 8`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var count int64
		if err := rows.Scan(&name, &count); err != nil {
			writeError(response, http.StatusInternalServerError, "scan top scenarios")
			return
		}
		topScenarios = append(topScenarios, map[string]any{"name": name, "runs": count})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load top scenarios")
		return
	}

	var resolvedWithoutEscalationPct *float64
	if totalRuns > 0 {
		value := round(float64(totalRuns-escalatedRuns)*100/float64(totalRuns), 1)
		resolvedWithoutEscalationPct = &value
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"total_runs": totalRuns, "done_runs": doneRuns, "no_recipient_runs": noRecipientRuns,
		"escalated_runs": escalatedRuns, "avg_steps": avgSteps,
		"resolved_without_escalation_pct": resolvedWithoutEscalationPct,
		"top_scenarios":                   topScenarios,
	})
}
