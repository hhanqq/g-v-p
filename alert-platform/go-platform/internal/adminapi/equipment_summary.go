package adminapi

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// equipmentIDFromPath — общий разбор /api/equipment/{id}/<suffix>, тот же
// url.PathUnescape, что уже использует getEquipment.
func equipmentIDFromPath(request *http.Request, suffix string) (string, error) {
	normalized := normalizePath(request.URL.Path)
	raw := strings.TrimSuffix(strings.TrimPrefix(normalized, "/api/equipment/"), suffix)
	return url.PathUnescape(raw)
}

// equipmentSummary — GET /api/equipment/{id}/summary. KPI-строка под
// паспортом карточки (раздел II.9 ТЗ): активные проблемы, открытые
// инциденты, алерты 24ч/30д, MTTR за 30 дней — реальные агрегаты, не
// пересчёт на клиенте.
func (server *Server) equipmentSummary(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	objectID, err := equipmentIDFromPath(request, "/summary")
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	stats, err := server.equipmentObjectStats(request.Context(), []string{objectID})
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var mttr sql.NullFloat64
	err = server.pool.QueryRow(request.Context(), `
		SELECT avg(EXTRACT(EPOCH FROM (resolved_at - opened_at))/60)
		FROM problems WHERE object_id=$1 AND status='RESOLVED' AND resolved_at >= now() - INTERVAL '30 days'
		  AND resolved_at >= opened_at`,
		objectID).Scan(&mttr)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	stat := stats[objectID]
	result := map[string]any{
		"active_problems": stat.active, "open_incidents": stat.openIncidents,
		"alerts_24h": stat.alerts24h, "alerts_30d": stat.alerts30d,
		"last_event_at": stat.lastEventAt, "worst_priority": stat.worstPriority,
	}
	if mttr.Valid {
		result["avg_mttr_minutes_30d"] = mttr.Float64
	} else {
		result["avg_mttr_minutes_30d"] = nil
	}
	writeJSON(response, http.StatusOK, result)
}

type equipmentIncident struct {
	ID           int64   `json:"id"`
	RootProblem  int64   `json:"root_problem_id"`
	Priority     *string `json:"priority"`
	OpenedAt     string  `json:"opened_at"`
	ClosedAt     *string `json:"closed_at"`
	MemberCount  int     `json:"member_count"`
	SymptomClass string  `json:"symptom_class"`
	ObjectName   string  `json:"object_name"`
}

// equipmentIncidentsList — GET /api/equipment/{id}/incidents. Инциденты,
// в чей кластер входит хотя бы один Problem этого объекта — открытые и
// закрытые вперемешку, статус берётся из closed_at (раздел III ТЗ: он
// теперь реально пишется, см. internal/pipeline/state.go).
func (server *Server) equipmentIncidentsList(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	objectID, err := equipmentIDFromPath(request, "/incidents")
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	rows, err := server.pool.Query(request.Context(), `
		SELECT incident.id, incident.root_problem_id, incident.priority, incident.opened_at, incident.closed_at,
			(SELECT count(*) FROM incident_problems ip2 WHERE ip2.incident_id = incident.id) AS member_count,
			root.symptom_class, coalesce(object.name, root.object_id, '') AS object_name
		FROM incidents incident
		JOIN problems root ON root.id = incident.root_problem_id
		LEFT JOIN cmdb_objects object ON object.id = root.object_id
		WHERE incident.id IN (
			SELECT ip.incident_id FROM incident_problems ip
			JOIN problems p ON p.id = ip.problem_id WHERE p.object_id = $1
		)
		ORDER BY incident.opened_at DESC LIMIT 100`, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]equipmentIncident, 0)
	for rows.Next() {
		var item equipmentIncident
		var priority sql.NullString
		var openedAt time.Time
		var closedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.RootProblem, &priority, &openedAt, &closedAt, &item.MemberCount, &item.SymptomClass, &item.ObjectName); err != nil {
			writeError(response, http.StatusInternalServerError, "scan incidents")
			return
		}
		item.Priority = nullableString(priority)
		item.OpenedAt = formatISO(openedAt)
		if closedAt.Valid {
			formatted := formatISO(closedAt.Time)
			item.ClosedAt = &formatted
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load incidents")
		return
	}
	writeJSON(response, http.StatusOK, items)
}
