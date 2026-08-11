package adminapi

import (
	"net/http"
)

// analyticsAlertsTimeseries — GET /api/analytics/alerts-timeseries?period=&groupby=priority|source.
// Раздел IV.15 ТЗ: основной график должен показывать объём, динамику и
// состав по критичности/источнику — один stacked-ready ответ на оба
// переключателя вместо двух отдельных ручек.
func (server *Server) analyticsAlertsTimeseries(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)
	groupBy := request.URL.Query().Get("groupby")

	var query string
	if groupBy == "source" {
		query = `
			SELECT TO_CHAR(day,'YYYY-MM-DD'), COALESCE(s.source_system,'—'), COUNT(e.id)
			FROM GENERATE_SERIES(date_trunc('day',$1::timestamp), date_trunc('day',$2::timestamp), INTERVAL '1 day') day
			LEFT JOIN events e ON e.occurred_at>=day AND e.occurred_at<day+INTERVAL '1 day'
			LEFT JOIN signals s ON s.id = e.signal_id
			GROUP BY day, s.source_system ORDER BY day`
	} else {
		query = `
			SELECT TO_CHAR(day,'YYYY-MM-DD'), COALESCE(p.priority,'unknown'), COUNT(DISTINCT e.id)
			FROM GENERATE_SERIES(date_trunc('day',$1::timestamp), date_trunc('day',$2::timestamp), INTERVAL '1 day') day
			LEFT JOIN events e ON e.occurred_at>=day AND e.occurred_at<day+INTERVAL '1 day'
			LEFT JOIN problems p ON p.id = e.problem_id
			GROUP BY day, p.priority ORDER BY day`
	}
	rows, err := server.pool.Query(ctx, query, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	series := make([]map[string]any, 0)
	for rows.Next() {
		var day, key string
		var count int64
		if err := rows.Scan(&day, &key, &count); err != nil {
			writeError(response, http.StatusInternalServerError, "scan timeseries")
			return
		}
		series = append(series, map[string]any{"day": day, "key": key, "count": count})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load timeseries")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"series": series, "groupby": groupByOrDefault(groupBy)})
}

func groupByOrDefault(value string) string {
	if value == "source" {
		return "source"
	}
	return "priority"
}

// analyticsIncidentsTimeseries — GET /api/analytics/incidents-timeseries?period=.
// Раздел VIII.30 ТЗ: создано / закрыто / открыто на конец периода —
// реальные incidents.opened_at/closed_at, не производная от алертов.
func (server *Server) analyticsIncidentsTimeseries(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)
	rows, err := server.pool.Query(ctx, `
		SELECT TO_CHAR(day,'YYYY-MM-DD'),
			COUNT(*) FILTER (WHERE i.opened_at>=day AND i.opened_at<day+INTERVAL '1 day') AS created,
			COUNT(*) FILTER (WHERE i.closed_at>=day AND i.closed_at<day+INTERVAL '1 day') AS closed
		FROM GENERATE_SERIES(date_trunc('day',$1::timestamp), date_trunc('day',$2::timestamp), INTERVAL '1 day') day
		LEFT JOIN incidents i ON (i.opened_at>=day AND i.opened_at<day+INTERVAL '1 day')
			OR (i.closed_at>=day AND i.closed_at<day+INTERVAL '1 day')
		GROUP BY day ORDER BY day`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	series := make([]map[string]any, 0)
	for rows.Next() {
		var day string
		var created, closed int64
		if err := rows.Scan(&day, &created, &closed); err != nil {
			writeError(response, http.StatusInternalServerError, "scan incidents timeseries")
			return
		}
		series = append(series, map[string]any{"day": day, "created": created, "closed": closed})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load incidents timeseries")
		return
	}
	// "Открыты" — ещё никто не подтвердил root problem; "В работе" —
	// подтверждена, но кластер не весь резолвлен; "Закрыты" — все
	// участники резолвлены (incidents.closed_at реально выставлен,
	// см. internal/pipeline/state.go). Снимок на текущий момент, не
	// период — донат показывает состояние "прямо сейчас", а не историю.
	var openNow, inProgress, closedTotal int
	if err := server.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE i.closed_at IS NULL AND root.acknowledged_at IS NULL),
			count(*) FILTER (WHERE i.closed_at IS NULL AND root.acknowledged_at IS NOT NULL),
			count(*) FILTER (WHERE i.closed_at IS NOT NULL)
		FROM incidents i JOIN problems root ON root.id = i.root_problem_id`,
	).Scan(&openNow, &inProgress, &closedTotal); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"series":         series,
		"open_vs_closed": map[string]any{"open": openNow, "in_progress": inProgress, "closed": closedTotal},
	})
}
