package adminapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/coverage"
)

// homeOverview — GET /api/home/overview (раздел 20-29 доп. ТЗ). Главная
// показывает «сейчас», не историю (Аналитика) и не техническую
// диагностику ADP (Состояние системы, platformHealth). Существующий
// /api/home/summary (гомогенный набор бизнес-метрик пайплайна) не
// удалён — он оставлен нетронутым как самостоятельный рабочий эндпоинт,
// просто больше не единственный потребитель Главной.
func (server *Server) homeOverview(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var openIncidents, criticalActive, noReaction, slaBreachesToday int64
	if err := server.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM incidents WHERE closed_at IS NULL),
			(SELECT count(*) FROM problems WHERE status IN ('OPEN','FLAPPING') AND priority IN ('P0','P1')),
			(SELECT count(*) FROM problems WHERE status IN ('OPEN','FLAPPING') AND acknowledged_at IS NULL),
			(SELECT count(*) FROM sla_breach_notices WHERE created_at >= $1)`,
		todayStart,
	).Scan(&openIncidents, &criticalActive, &noReaction, &slaBreachesToday); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	periodFrom, periodTo := parseHomePeriod(request, now)
	alertsSeries, granularity, err := server.alertsSeriesByPriority(ctx, periodFrom, periodTo)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	needsAttention, err := server.needsAttention(ctx, now)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	scenarios, err := server.homeScenariosSummary(ctx, todayStart)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	coverageSummary, err := server.homeCoverageSummary(ctx, now)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	components := []componentStatus{
		server.checkPostgres(ctx), server.checkGateway(ctx), server.checkPipeline(ctx),
		server.checkDeliveryChannel(ctx, "trueconf", "TrueConf"), server.checkDeliveryChannel(ctx, "email", "Email"),
		server.checkOllama(ctx),
	}
	samples := server.resources.recent()
	resources := map[string]any{}
	cpuSeries := make([]float64, 0, len(samples))
	ramSeries := make([]float64, 0, len(samples))
	for _, s := range samples {
		cpuSeries = append(cpuSeries, round(s.CPUPct, 1))
		if s.RAMTotal > 0 {
			ramSeries = append(ramSeries, round(float64(s.RAMUsed)*100/float64(s.RAMTotal), 1))
		}
	}
	resources["cpu_series"], resources["ram_series"] = cpuSeries, ramSeries
	if len(samples) > 0 {
		latest := samples[len(samples)-1]
		resources["cpu_pct"] = round(latest.CPUPct, 1)
		if latest.RAMTotal > 0 {
			resources["ram_pct"] = round(float64(latest.RAMUsed)*100/float64(latest.RAMTotal), 1)
		}
		if latest.DiskTot > 0 {
			resources["disk_pct"] = round(float64(latest.DiskUsed)*100/float64(latest.DiskTot), 1)
		}
	}
	resources["ai"] = server.aiTelemetry(ctx)

	writeJSON(response, http.StatusOK, map[string]any{
		"kpis": map[string]any{
			"open_incidents": openIncidents, "critical_active": criticalActive,
			"no_reaction": noReaction, "sla_breaches_today": slaBreachesToday,
		},
		"alerts_series": alertsSeries,
		"alerts_period": map[string]any{
			"from": formatISO(periodFrom), "to": formatISO(periodTo), "granularity": granularity,
		},
		"needs_attention": needsAttention,
		"adp_health":      components,
		"resources":       resources,
		"scenarios":       scenarios,
		"coverage":        coverageSummary,
	})
}

// parseHomePeriod — раздел «Главная» доп. ТЗ: селектор периода
// (24ч/7д/30д/произвольный), период отражается в URL самим фронтендом
// (?period=7d или ?period=custom&from=...&to=...). Отсутствие/нераспознанный
// period — прежнее поведение по умолчанию (последние 24 часа).
func parseHomePeriod(request *http.Request, now time.Time) (from, to time.Time) {
	to = now
	switch request.URL.Query().Get("period") {
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	case "custom":
		from = now.Add(-24 * time.Hour)
		if parsed, err := time.Parse(time.RFC3339, request.URL.Query().Get("from")); err == nil {
			from = parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, request.URL.Query().Get("to")); err == nil {
			to = parsed.UTC()
		}
	default:
		from = now.Add(-24 * time.Hour)
	}
	if !to.After(from) {
		return now.Add(-24 * time.Hour), now
	}
	return from, to
}

// bucketPlan — раздел «Главная» доп. ТЗ: гранулярность бакетов считает
// бэкенд по фактической длительности периода, не фронтенд по фиксированным
// вариантам — работает и для произвольного custom-диапазона, а не только
// для 24ч/7д/30д. Цель явно сформулирована в ТЗ: браузер не должен получать
// тысячи сырых точек.
func bucketPlan(duration time.Duration) (interval, format, granularity string) {
	switch {
	case duration <= 48*time.Hour:
		return "1 hour", "HH24:MI", "hour"
	case duration <= 14*24*time.Hour:
		return "6 hours", "DD.MM HH24:MI", "6h"
	case duration <= 45*24*time.Hour:
		return "1 day", "DD.MM", "day"
	default:
		return "7 days", "DD.MM", "week"
	}
}

func (server *Server) alertsSeriesByPriority(ctx context.Context, from, to time.Time) ([]map[string]any, string, error) {
	interval, format, granularity := bucketPlan(to.Sub(from))
	rows, err := server.pool.Query(ctx, `
		SELECT TO_CHAR(bucket, $4), COALESCE(problem.priority,'—'), count(*)
		FROM GENERATE_SERIES($1::timestamp, $2::timestamp, $3::interval) bucket
		JOIN events event ON event.occurred_at >= bucket AND event.occurred_at < bucket + $3::interval
		LEFT JOIN problems problem ON problem.id = event.problem_id
		GROUP BY bucket, problem.priority ORDER BY bucket`,
		from, to, interval, format,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var bucket, priority string
		var count int64
		if err := rows.Scan(&bucket, &priority, &count); err != nil {
			return nil, "", err
		}
		out = append(out, map[string]any{"bucket": bucket, "priority": priority, "count": count})
	}
	return out, granularity, rows.Err()
}

// needsAttention — раздел 23: самый практически полезный блок Главной.
// Три реальных источника, не выдуманный список: дольше всего ждущая
// реакции критическая проблема, оборудование без доступного дежурного
// прямо сейчас (тот же coverage.Sweep, что и раздел «Покрытие» — один
// источник истины) и растущий backlog доставки.
func (server *Server) needsAttention(ctx context.Context, now time.Time) ([]map[string]any, error) {
	items := make([]map[string]any, 0)

	rows, err := server.pool.Query(ctx, `
		SELECT problem.id, problem.priority, problem.object_id, problem.incident_id, problem.opened_at
		FROM problems problem
		WHERE problem.status IN ('OPEN','FLAPPING') AND problem.acknowledged_at IS NULL AND problem.priority IN ('P0','P1')
		ORDER BY problem.opened_at ASC LIMIT 3`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var priority, objectID *string
		var incidentID *int64
		var openedAt time.Time
		if err := rows.Scan(&id, &priority, &objectID, &incidentID, &openedAt); err != nil {
			rows.Close()
			return nil, err
		}
		minutes := int(now.Sub(openedAt).Minutes())
		label := "проблема"
		if objectID != nil {
			label = *objectID
		}
		item := map[string]any{
			"kind": "no_reaction", "priority": priority, "object_id": objectID, "problem_id": id,
			"text": label, "detail": itoaMinutes(minutes) + " без реакции",
		}
		if incidentID != nil {
			item["incident_id"] = *incidentID
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	policyRows, err := server.pool.Query(ctx, `SELECT id, name, group_id, min_available FROM coverage_policies WHERE active=TRUE ORDER BY id LIMIT 20`)
	if err != nil {
		return nil, err
	}
	type policyRow struct {
		id, groupID  int64
		name         string
		minAvailable int
	}
	var policies []policyRow
	for policyRows.Next() {
		var p policyRow
		if err := policyRows.Scan(&p.id, &p.name, &p.groupID, &p.minAvailable); err != nil {
			policyRows.Close()
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := policyRows.Err(); err != nil {
		policyRows.Close()
		return nil, err
	}
	policyRows.Close()
	for _, p := range policies {
		if len(items) >= 6 {
			break
		}
		gaps, err := coverage.Sweep(ctx, server.pool, p.groupID, now, now.Add(time.Minute), p.minAvailable, nil)
		if err != nil {
			return nil, err
		}
		if len(gaps) > 0 {
			items = append(items, map[string]any{
				"kind": "coverage_gap", "text": p.name, "detail": "нет доступного ответственного прямо сейчас",
			})
		}
	}

	var pendingOld int64
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_outbox WHERE status='pending' AND created_at < $1`,
		now.Add(-5*time.Minute),
	).Scan(&pendingOld); err != nil {
		return nil, err
	}
	if pendingOld > 0 {
		items = append(items, map[string]any{
			"kind": "delivery_backlog", "text": "Доставка",
			"detail": fmt.Sprintf("backlog растёт: %d сообщений старше 5 минут", pendingOld),
		})
	}

	return items, nil
}

func itoaMinutes(n int) string {
	return strconv.Itoa(n) + " мин"
}

func (server *Server) homeScenariosSummary(ctx context.Context, todayStart time.Time) (map[string]any, error) {
	var activeScenarios, runsToday, escalationsToday, awaitingReaction int64
	if err := server.pool.QueryRow(ctx, `SELECT count(*) FROM scenarios WHERE status='active'`).Scan(&activeScenarios); err != nil {
		return nil, err
	}
	if err := server.pool.QueryRow(ctx, `SELECT count(*) FROM scenario_runs WHERE created_at >= $1`, todayStart).Scan(&runsToday); err != nil {
		return nil, err
	}
	if err := server.pool.QueryRow(ctx, `SELECT count(*) FROM scenario_runs WHERE status='running'`).Scan(&awaitingReaction); err != nil {
		return nil, err
	}
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT run_id FROM scenario_run_steps step
			JOIN scenario_runs run ON run.id = step.run_id
			WHERE step.node_type='notify' AND run.created_at >= $1
			GROUP BY run_id HAVING count(*) > 1
		) escalated`, todayStart,
	).Scan(&escalationsToday); err != nil {
		return nil, err
	}
	return map[string]any{
		"active_scenarios": activeScenarios, "runs_today": runsToday,
		"awaiting_reaction": awaitingReaction, "escalations_today": escalationsToday,
	}, nil
}

// homeCoverageSummary — раздел 28: покрытие критических объектов
// (P0-объекты с активной политикой) прямо сейчас + ближайшие 7 дней.
func (server *Server) homeCoverageSummary(ctx context.Context, now time.Time) (map[string]any, error) {
	rows, err := server.pool.Query(ctx, `SELECT id, group_id, min_available FROM coverage_policies WHERE active=TRUE`)
	if err != nil {
		return nil, err
	}
	type policyRow struct {
		id, groupID  int64
		minAvailable int
	}
	var policies []policyRow
	for rows.Next() {
		var p policyRow
		if err := rows.Scan(&p.id, &p.groupID, &p.minAvailable); err != nil {
			rows.Close()
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	fullyCovered, gapsNext7d := 0, 0
	for _, p := range policies {
		gapsNow, err := coverage.Sweep(ctx, server.pool, p.groupID, now, now.Add(time.Minute), p.minAvailable, nil)
		if err != nil {
			return nil, err
		}
		if len(gapsNow) == 0 {
			fullyCovered++
		}
		gapsWeek, err := coverage.Sweep(ctx, server.pool, p.groupID, now, now.AddDate(0, 0, 7), p.minAvailable, nil)
		if err != nil {
			return nil, err
		}
		if len(gapsWeek) > 0 {
			gapsNext7d++
		}
	}
	return map[string]any{
		"critical_total": len(policies), "critical_fully_covered": fullyCovered, "gaps_next_7d": gapsNext7d,
	}, nil
}
