package adminapi

import (
	"net/http"
)

// analyticsSLA — GET /api/analytics/sla?period=. Раздел VIII.38 ТЗ:
// доля соблюдения, не голое число нарушений. "Применимо SLA" честно
// реплицирует правило выбора sla_rules из internal/planner/automation.go
// (planSLABreaches) — приоритет + опционально subsidiary/service_id
// по владению объектом, самое специфичное правило побеждает — иначе
// знаменатель был бы "все проблемы", часть которых вообще не подпадает
// ни под одно правило.
func (server *Server) analyticsSLA(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)

	var applicable, breached int
	err := server.pool.QueryRow(ctx, `
		WITH applicable_rules AS (
			SELECT problem.id,
				(SELECT rule.id FROM sla_rules rule
				 WHERE rule.priority = problem.priority
				   AND (rule.subsidiary IS NULL OR EXISTS(
						SELECT 1 FROM cmdb_ownership ownership WHERE ownership.object_id=problem.object_id AND ownership.subsidiary=rule.subsidiary
						UNION ALL
						SELECT 1 FROM cmdb_ownership ownership JOIN cmdb_service_objects so ON so.service_id=ownership.service_id
						WHERE so.object_id=problem.object_id AND ownership.subsidiary=rule.subsidiary))
				   AND (rule.service_id IS NULL OR EXISTS(SELECT 1 FROM cmdb_service_objects WHERE object_id=problem.object_id AND service_id=rule.service_id))
				 ORDER BY ((rule.subsidiary IS NOT NULL)::int + (rule.service_id IS NOT NULL)::int) DESC, rule.id LIMIT 1
				) AS rule_id
			FROM problems problem
			WHERE problem.priority IS NOT NULL AND problem.opened_at >= $1 AND problem.opened_at < $2
		)
		SELECT count(*) FILTER (WHERE rule_id IS NOT NULL),
			count(*) FILTER (WHERE rule_id IS NOT NULL AND id IN (SELECT problem_id FROM sla_breach_notices))
		FROM applicable_rules`, rng.From, rng.To,
	).Scan(&applicable, &breached)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var compliancePct *float64
	if applicable > 0 {
		value := round(float64(applicable-breached)*100/float64(applicable), 1)
		compliancePct = &value
	}

	rows, err := server.pool.Query(ctx, `
		SELECT TO_CHAR(day,'YYYY-MM-DD'), count(*) FROM GENERATE_SERIES(
			date_trunc('day',$1::timestamp), date_trunc('day',$2::timestamp), INTERVAL '1 day') day
		LEFT JOIN sla_breach_notices notice ON notice.created_at>=day AND notice.created_at<day+INTERVAL '1 day'
		GROUP BY day ORDER BY day`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	series := make([]map[string]any, 0)
	for rows.Next() {
		var day string
		var count int64
		if err := rows.Scan(&day, &count); err != nil {
			writeError(response, http.StatusInternalServerError, "scan sla series")
			return
		}
		series = append(series, map[string]any{"day": day, "breaches": count})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load sla series")
		return
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"applicable": applicable, "breached": breached, "compliance_pct": compliancePct, "breach_series": series,
	})
}
