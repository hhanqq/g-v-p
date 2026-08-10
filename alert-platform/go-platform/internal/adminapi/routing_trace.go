package adminapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// problemRoutingTrace — раздел «Маршрутизация с учётом доступности»:
// объединяет два независимых источника decision trace для одной
// проблемы в одну хронологическую ленту. Простой подписочный путь
// (planNew/planSLABreaches) пишет trace прямо в
// notifications.routing_trace_json; сценарный путь пишет его в
// scenario_run_steps.recipients_json (см. internal/planner/automation.go)
// — узел notify там же решает default/no_recipient. Оба читаются
// read-only, ничего не пересчитывается на лету.
func (server *Server) problemRoutingTrace(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	path := strings.TrimSuffix(normalizePath(request.URL.Path), "/routing-trace")
	id, ok := pathInt(path, "/api/problems/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid problem id")
		return
	}
	items := make([]map[string]any, 0)

	rows, err := server.pool.Query(request.Context(), `
		SELECT type, recipient, routing_trace_json, created_at
		FROM notifications
		WHERE problem_id=$1 AND routing_trace_json IS NOT NULL
		ORDER BY id`, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var notificationType, recipient, traceJSON string
		var createdAt time.Time
		if err := rows.Scan(&notificationType, &recipient, &traceJSON, &createdAt); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan routing trace")
			return
		}
		var trace map[string]any
		_ = json.Unmarshal([]byte(traceJSON), &trace)
		items = append(items, map[string]any{
			"source": "notification", "notification_type": notificationType, "recipient": recipient,
			"trace": trace, "at": formatISO(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load routing trace")
		return
	}
	rows.Close()

	rows, err = server.pool.Query(request.Context(), `
		SELECT s.scenario_id, sc.name, s.node_id, s.branch, s.recipients_json, s.created_at
		FROM scenario_run_steps s
		JOIN scenarios sc ON sc.id = s.scenario_id
		WHERE s.problem_id=$1 AND s.node_type='notify' AND s.recipients_json IS NOT NULL
		ORDER BY s.id`, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var scenarioID int64
		var scenarioName, nodeID, traceJSON string
		var branch sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&scenarioID, &scenarioName, &nodeID, &branch, &traceJSON, &createdAt); err != nil {
			writeError(response, http.StatusInternalServerError, "scan routing trace")
			return
		}
		var trace map[string]any
		_ = json.Unmarshal([]byte(traceJSON), &trace)
		items = append(items, map[string]any{
			"source": "scenario", "scenario_id": scenarioID, "scenario_name": scenarioName,
			"node_id": nodeID, "branch": nullableString(branch), "trace": trace, "at": formatISO(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load routing trace")
		return
	}
	writeJSON(response, http.StatusOK, items)
}
