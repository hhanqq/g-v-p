package adminapi

import (
	"context"
	"database/sql"
	"net/http"
)

// equipmentGroup — один уровень drill-down («Оборудование» → филиал →
// категория → объект). Один и тот же тип обслуживает оба промежуточных
// уровня: без параметра site — группировка по площадке (филиал), с site —
// группировка по equipment_type внутри площадки (категория). Статус
// категории наследуется от самой серьёзной активной проблемы среди
// потомков (worst_priority) — само по себе не создаёт Problem/Incident,
// только агрегирует уже существующие (раздел I.6 ТЗ).
type equipmentGroup struct {
	Key            string   `json:"key"`
	Label          string   `json:"label"`
	ObjectCount    int      `json:"object_count"`
	ActiveProblems int      `json:"active_problems"`
	P0Active       int      `json:"p0_active"`
	P1Active       int      `json:"p1_active"`
	OpenIncidents  int      `json:"open_incidents"`
	Alerts24h      int      `json:"alerts_24h"`
	Alerts30d      int      `json:"alerts_30d"`
	AvgMTTRMinutes *float64 `json:"avg_mttr_minutes"`
	WorstPriority  *string  `json:"worst_priority"`
}

var siteLabels = map[string]string{
	"brd-noyabrsk":   "Ноябрьский филиал",
	"brd-khantos":    "Ханты-Мансийский филиал",
	"brd-sakhalin":   "Сахалинский филиал",
	"brd-zapolyarye": "Заполярный филиал",
}

var equipmentTypeLabels = map[string]string{
	"plc":     "АСУ ТП / ПЛК",
	"server":  "Серверная инфраструктура",
	"network": "Сетевое оборудование",
}

func groupLabel(mapping map[string]string, key string) string {
	if label, ok := mapping[key]; ok {
		return label
	}
	if key == "" {
		return "Без категории"
	}
	return key
}

// listEquipmentGroups — GET /api/equipment/groups[?site=]. Без site отдаёт
// уровень «филиал», с site — уровень «категория» внутри этого филиала.
func (server *Server) listEquipmentGroups(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	site := request.URL.Query().Get("site")
	byCategory := site != ""

	counts, err := server.equipmentGroupCounts(ctx, byCategory, site)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	problemStats, err := server.equipmentGroupProblemStats(ctx, byCategory, site)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	alertStats, err := server.equipmentGroupAlertStats(ctx, byCategory, site)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	labels := siteLabels
	if byCategory {
		labels = equipmentTypeLabels
	}
	groups := make([]equipmentGroup, 0, len(counts))
	for key, count := range counts {
		group := equipmentGroup{Key: key, Label: groupLabel(labels, key), ObjectCount: count}
		if stats, ok := problemStats[key]; ok {
			group.ActiveProblems, group.P0Active, group.P1Active = stats.active, stats.p0, stats.p1
			group.OpenIncidents, group.WorstPriority, group.AvgMTTRMinutes = stats.openIncidents, stats.worstPriority, stats.avgMTTR
		}
		if stats, ok := alertStats[key]; ok {
			group.Alerts24h, group.Alerts30d = stats.last24h, stats.last30d
		}
		groups = append(groups, group)
	}
	writeJSON(response, http.StatusOK, groups)
}

func (server *Server) equipmentGroupCounts(ctx context.Context, byCategory bool, site string) (map[string]int, error) {
	query := `SELECT site, count(*) FROM cmdb_objects GROUP BY site`
	args := []any{}
	if byCategory {
		query = `SELECT coalesce(equipment_type,''), count(*) FROM cmdb_objects WHERE site=$1 GROUP BY equipment_type`
		args = append(args, site)
	}
	rows, err := server.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int)
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, rows.Err()
}

type equipmentProblemStats struct {
	active, p0, p1, openIncidents int
	worstPriority                 *string
	avgMTTR                       *float64
}

func (server *Server) equipmentGroupProblemStats(ctx context.Context, byCategory bool, site string) (map[string]equipmentProblemStats, error) {
	groupExpr := "object.site"
	where := ""
	args := []any{}
	if byCategory {
		groupExpr = "coalesce(object.equipment_type,'')"
		where = "WHERE object.site=$1"
		args = append(args, site)
	}
	query := `
		SELECT ` + groupExpr + ` AS key,
			count(*) FILTER (WHERE problem.status IN ('OPEN','FLAPPING')) AS active,
			count(*) FILTER (WHERE problem.status IN ('OPEN','FLAPPING') AND problem.priority='P0') AS p0,
			count(*) FILTER (WHERE problem.status IN ('OPEN','FLAPPING') AND problem.priority='P1') AS p1,
			count(DISTINCT problem.incident_id) FILTER (WHERE problem.incident_id IS NOT NULL AND problem.status IN ('OPEN','FLAPPING')) AS open_incidents,
			min(problem.priority) FILTER (WHERE problem.status IN ('OPEN','FLAPPING')) AS worst_priority,
			avg(EXTRACT(EPOCH FROM (problem.resolved_at - problem.opened_at))/60) FILTER (
				WHERE problem.status='RESOLVED' AND problem.resolved_at >= now() - INTERVAL '30 days'
				  AND problem.resolved_at >= problem.opened_at
			) AS avg_mttr_minutes
		FROM problems problem
		JOIN cmdb_objects object ON object.id = problem.object_id
		` + where + `
		GROUP BY key`
	rows, err := server.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]equipmentProblemStats)
	for rows.Next() {
		var key string
		var stats equipmentProblemStats
		var worstPriority sql.NullString
		var avgMTTR sql.NullFloat64
		if err := rows.Scan(&key, &stats.active, &stats.p0, &stats.p1, &stats.openIncidents, &worstPriority, &avgMTTR); err != nil {
			return nil, err
		}
		stats.worstPriority = nullableString(worstPriority)
		if avgMTTR.Valid {
			stats.avgMTTR = &avgMTTR.Float64
		}
		result[key] = stats
	}
	return result, rows.Err()
}

type equipmentAlertStats struct{ last24h, last30d int }

func (server *Server) equipmentGroupAlertStats(ctx context.Context, byCategory bool, site string) (map[string]equipmentAlertStats, error) {
	groupExpr := "object.site"
	where := ""
	args := []any{}
	if byCategory {
		groupExpr = "coalesce(object.equipment_type,'')"
		where = "WHERE object.site=$1"
		args = append(args, site)
	}
	query := `
		SELECT ` + groupExpr + ` AS key,
			count(*) FILTER (WHERE event.occurred_at >= now() - INTERVAL '24 hours') AS last24h,
			count(*) FILTER (WHERE event.occurred_at >= now() - INTERVAL '30 days') AS last30d
		FROM events event
		JOIN cmdb_objects object ON object.id = event.object_id
		` + where + `
		GROUP BY key`
	rows, err := server.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]equipmentAlertStats)
	for rows.Next() {
		var key string
		var stats equipmentAlertStats
		if err := rows.Scan(&key, &stats.last24h, &stats.last30d); err != nil {
			return nil, err
		}
		result[key] = stats
	}
	return result, rows.Err()
}
