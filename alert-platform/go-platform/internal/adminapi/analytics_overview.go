package adminapi

import (
	"context"
	"database/sql"
	"net/http"
)

// analyticsOverview — GET /api/analytics/overview[?period=&site=&priority=].
// Верхний уровень дашборда (раздел III.13 ТЗ): KPI + воронка снижения
// шума + распределения по приоритету/источнику. Реальные агрегаты по
// уже существующим Signal/Event/Problem/Incident/Notification —
// ничего не подавляется и не удаляется, раздел «Снижение шума» это
// прямо описывает словами "объединено", не "потеряно" (раздел VIII.36 ТЗ).
func (server *Server) analyticsOverview(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)
	site := siteFilter(request)

	siteWhere, siteArgs := "", []any{}
	if site != "" {
		siteWhere = " AND object.site = $3"
		siteArgs = []any{site}
	}

	var rawEvents, problemsTotal, problemsDeduped, incidentsTotal, notificationsSent int
	err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM events event
		LEFT JOIN cmdb_objects object ON object.id = event.object_id
		WHERE event.occurred_at >= $1 AND event.occurred_at < $2`+siteWhere,
		append([]any{rng.From, rng.To}, siteArgs...)...,
	).Scan(&rawEvents)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	err = server.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE problem.duplicate_of_problem_id IS NULL)
		FROM problems problem LEFT JOIN cmdb_objects object ON object.id = problem.object_id
		WHERE problem.opened_at >= $1 AND problem.opened_at < $2`+siteWhere,
		append([]any{rng.From, rng.To}, siteArgs...)...,
	).Scan(&problemsTotal, &problemsDeduped)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	err = server.pool.QueryRow(ctx, `
		SELECT count(*) FROM incidents incident
		JOIN problems root ON root.id = incident.root_problem_id
		LEFT JOIN cmdb_objects object ON object.id = root.object_id
		WHERE incident.opened_at >= $1 AND incident.opened_at < $2`+siteWhere,
		append([]any{rng.From, rng.To}, siteArgs...)...,
	).Scan(&incidentsTotal)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	err = server.pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications n
		JOIN problems problem ON problem.id = n.problem_id
		LEFT JOIN cmdb_objects object ON object.id = problem.object_id
		WHERE n.status = 'sent' AND n.created_at >= $1 AND n.created_at < $2`+siteWhere,
		append([]any{rng.From, rng.To}, siteArgs...)...,
	).Scan(&notificationsSent)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	mtta, err := queryNullableFloat(ctx, server.pool, `
		SELECT AVG(EXTRACT(EPOCH FROM (acknowledged_at-opened_at))) FROM problems
		WHERE acknowledged_at IS NOT NULL AND acknowledged_at>=opened_at
		  AND opened_at >= $1 AND opened_at < $2`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	mttr, err := queryNullableFloat(ctx, server.pool, `
		SELECT AVG(EXTRACT(EPOCH FROM (resolved_at-opened_at))) FROM problems
		WHERE status='RESOLVED' AND resolved_at IS NOT NULL AND resolved_at>=opened_at
		  AND opened_at >= $1 AND opened_at < $2`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var requiringAck, acknowledged int
	err = server.pool.QueryRow(ctx, `
		SELECT count(DISTINCT problem.id), count(DISTINCT problem.id) FILTER (WHERE problem.acknowledged_at IS NOT NULL)
		FROM notifications n JOIN problems problem ON problem.id = n.problem_id
		WHERE n.type='NEW' AND n.created_at >= $1 AND n.created_at < $2`,
		rng.From, rng.To,
	).Scan(&requiringAck, &acknowledged)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var ackRate *float64
	if requiringAck > 0 {
		value := round(float64(acknowledged)*100/float64(requiringAck), 1)
		ackRate = &value
	}

	var noiseReduction *float64
	if rawEvents > 0 {
		value := round((1-float64(problemsDeduped)/float64(rawEvents))*100, 1)
		noiseReduction = &value
	}

	priorities := make(map[string]int64)
	rows, err := server.pool.Query(ctx, `
		SELECT priority, count(*) FROM problems
		WHERE priority IS NOT NULL AND opened_at >= $1 AND opened_at < $2
		GROUP BY priority`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var priority string
		var count int64
		_ = rows.Scan(&priority, &count)
		priorities[priority] = count
	}
	rows.Close()

	sources := make([]map[string]any, 0)
	rows, err = server.pool.Query(ctx, `
		SELECT s.source_system, count(*) FROM events e JOIN signals s ON s.id = e.signal_id
		WHERE e.occurred_at >= $1 AND e.occurred_at < $2
		GROUP BY s.source_system ORDER BY count(*) DESC`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var system string
		var count int64
		_ = rows.Scan(&system, &count)
		sources = append(sources, map[string]any{"source_system": system, "count": count})
	}
	rows.Close()

	writeJSON(response, http.StatusOK, map[string]any{
		"alerts_total": rawEvents, "incidents_total": incidentsTotal,
		"mtta_seconds": mtta, "mttr_seconds": mttr, "ack_rate_pct": ackRate,
		"noise_reduction_pct": noiseReduction,
		"noise_funnel": map[string]any{
			"raw_events": rawEvents, "deduplicated": problemsDeduped, "problems_total": problemsTotal,
			"incidents": incidentsTotal, "notifications_sent": notificationsSent,
			"folded_into_existing": maxInt(0, rawEvents-problemsTotal),
		},
		"priority_distribution": priorities, "source_distribution": sources,
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// queryNullableFloat — как nullableFloat в server.go, но с параметрами
// запроса (nullableFloat принимает только готовую строку без плейсхолдеров,
// периодные фильтры аналитики без параметров писать небезопасно).
func queryNullableFloat(ctx context.Context, querier queryRower, query string, args ...any) (*float64, error) {
	var value sql.NullFloat64
	if err := querier.QueryRow(ctx, query, args...).Scan(&value); err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	rounded := round(value.Float64, 1)
	return &rounded, nil
}
