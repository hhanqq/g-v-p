package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/scenario"
	"github.com/jackc/pgx/v5"
)

func (server *Server) analyticsSummary(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	series := make([][]any, 0, 14)
	rows, err := server.pool.Query(ctx, `
		SELECT TO_CHAR(day,'YYYY-MM-DD'),COUNT(event.id)
		FROM GENERATE_SERIES(CURRENT_DATE-INTERVAL '13 days',CURRENT_DATE,INTERVAL '1 day') day
		LEFT JOIN events event ON event.occurred_at>=day AND event.occurred_at<day+INTERVAL '1 day'
		GROUP BY day ORDER BY day`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var day string
		var count int64
		if err := rows.Scan(&day, &count); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan analytics")
			return
		}
		series = append(series, []any{day, count})
	}
	rows.Close()
	topObjects := make([]map[string]any, 0)
	rows, err = server.pool.Query(ctx, `
		SELECT problem.object_id,COUNT(*),COALESCE(object.name,problem.object_id),object.site,object.equipment_type
		FROM problems problem LEFT JOIN cmdb_objects object ON object.id=problem.object_id
		WHERE problem.object_id IS NOT NULL
		GROUP BY problem.object_id,object.name,object.site,object.equipment_type
		ORDER BY COUNT(*) DESC LIMIT 8`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var objectID, name string
		var count int64
		var site, equipmentType sql.NullString
		if err := rows.Scan(&objectID, &count, &name, &site, &equipmentType); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan top objects")
			return
		}
		topObjects = append(topObjects, map[string]any{"object_id": objectID, "count": count, "name": name, "site": nullableString(site), "equipment_type": nullableString(equipmentType)})
	}
	rows.Close()
	topSymptoms := make([][]any, 0)
	rows, err = server.pool.Query(ctx, `SELECT symptom_class,COUNT(*) FROM problems GROUP BY symptom_class ORDER BY COUNT(*) DESC LIMIT 5`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var symptom string
		var count int64
		_ = rows.Scan(&symptom, &count)
		topSymptoms = append(topSymptoms, []any{symptom, count})
	}
	rows.Close()
	priorities := make(map[string]int64)
	rows, err = server.pool.Query(ctx, `SELECT priority,COUNT(*) FROM problems WHERE priority IS NOT NULL GROUP BY priority`)
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
	breaches := make(map[string]int64)
	var breachTotal int64
	rows, err = server.pool.Query(ctx, `
		SELECT COALESCE(problem.priority,'?'),COUNT(*)
		FROM sla_breach_notices notice JOIN problems problem ON problem.id=notice.problem_id
		GROUP BY problem.priority`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var priority string
		var count int64
		_ = rows.Scan(&priority, &count)
		breaches[priority], breachTotal = count, breachTotal+count
	}
	rows.Close()
	mttr, _ := nullableFloat(ctx, server.pool, `SELECT AVG(EXTRACT(EPOCH FROM (resolved_at-opened_at))) FROM problems WHERE status='RESOLVED' AND resolved_at IS NOT NULL AND resolved_at>=opened_at`, 1)
	var events, resolvedEvents, openProblems, incidents int64
	if err := server.pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM events),(SELECT COUNT(*) FROM events WHERE resolved=TRUE),(SELECT COUNT(*) FROM problems WHERE status IN ('OPEN','FLAPPING')),(SELECT COUNT(*) FROM incidents)`).Scan(&events, &resolvedEvents, &openProblems, &incidents); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var coverage *float64
	if events > 0 {
		value := round(float64(resolvedEvents)*100/float64(events), 1)
		coverage = &value
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"alerts_over_time": series, "top_problem_objects": topObjects, "top_symptoms": topSymptoms,
		"priority_distribution": priorities, "sla_breach": map[string]any{"total": breachTotal, "by_priority": breaches},
		"avg_mttr_seconds": mttr, "resolution_coverage_pct": coverage,
		"incidents": map[string]any{"open_problems": openProblems, "incidents": incidents},
	})
}

type interactionKey struct{ objectID, name, symptom string }

func (server *Server) equipmentInteractions(ctx context.Context, objectID string) (map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		WITH mine AS (
			SELECT member.incident_id,member.role FROM incident_problems member
			JOIN problems problem ON problem.id=member.problem_id WHERE problem.object_id=$1
		)
		SELECT mine.role,member.role,problem.object_id,COALESCE(object.name,problem.object_id),problem.symptom_class
		FROM mine JOIN incident_problems member ON member.incident_id=mine.incident_id
		JOIN problems problem ON problem.id=member.problem_id
		LEFT JOIN cmdb_objects object ON object.id=problem.object_id
		WHERE problem.object_id IS NOT NULL AND problem.object_id<>$1`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	caused, causedBy := make(map[interactionKey]int64), make(map[interactionKey]int64)
	for rows.Next() {
		var myRole, role, otherID, name, symptom string
		if err := rows.Scan(&myRole, &role, &otherID, &name, &symptom); err != nil {
			return nil, err
		}
		key := interactionKey{otherID, name, symptom}
		if myRole == "root" {
			caused[key]++
		} else if myRole == "symptom" && role == "root" {
			causedBy[key]++
		}
	}
	convert := func(values map[interactionKey]int64) []map[string]any {
		keys := make([]interactionKey, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return values[keys[i]] > values[keys[j]] })
		if len(keys) > 8 {
			keys = keys[:8]
		}
		result := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]any{"object_id": key.objectID, "name": key.name, "symptom_class": key.symptom, "count": values[key]})
		}
		return result
	}
	return map[string]any{"caused": convert(caused), "caused_by": convert(causedBy)}, rows.Err()
}

type scenarioRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	GraphJSON   *string `json:"graph_json"`
}

func decodeScenario(response http.ResponseWriter, request *http.Request) (scenarioRequest, bool) {
	var payload scenarioRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid scenario payload")
		return payload, false
	}
	return payload, true
}

func (server *Server) getScenario(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	id, ok := scenarioID(normalizePath(request.URL.Path))
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid scenario id")
		return
	}
	var name, graphJSON, status string
	var description, createdBy sql.NullString
	var updatedAt time.Time
	err := server.pool.QueryRow(request.Context(), `SELECT name,description,graph_json,status,created_by,updated_at FROM scenarios WHERE id=$1`, id).Scan(&name, &description, &graphJSON, &status, &createdBy, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Сценарий не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"id": id, "name": name, "description": nullableString(description), "graph_json": graphJSON, "status": status, "created_by": nullableString(createdBy), "updated_at": formatISO(updatedAt)})
}

func (server *Server) createScenario(response http.ResponseWriter, request *http.Request, user map[string]any) {
	payload, ok := decodeScenario(response, request)
	if !ok || payload.Name == nil || strings.TrimSpace(*payload.Name) == "" || payload.GraphJSON == nil {
		if ok {
			writeError(response, http.StatusUnprocessableEntity, "name and graph_json are required")
		}
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	var id int64
	err = tx.QueryRow(request.Context(), `INSERT INTO scenarios(name,description,graph_json,status,created_by,created_at,updated_at) VALUES($1,$2,$3,'draft',$4,$5,$5) RETURNING id`, strings.TrimSpace(*payload.Name), payload.Description, *payload.GraphJSON, actor, now).Scan(&id)
	if err == nil {
		_, err = tx.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'create_scenario',$2,$3)`, actor, strings.TrimSpace(*payload.Name), now)
	}
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": id})
}

func (server *Server) updateScenario(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	id, ok := scenarioID(normalizePath(request.URL.Path))
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid scenario id")
		return
	}
	payload, ok := decodeScenario(response, request)
	if !ok {
		return
	}
	var status string
	err := server.pool.QueryRow(request.Context(), `
		UPDATE scenarios SET name=COALESCE($2,name),description=COALESCE($3,description),
		graph_json=COALESCE($4,graph_json),status=CASE WHEN $4::text IS NOT NULL THEN 'draft' ELSE status END,updated_at=$5
		WHERE id=$1 RETURNING status`, id, payload.Name, payload.Description, payload.GraphJSON, time.Now().UTC()).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Сценарий не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (server *Server) activateScenario(response http.ResponseWriter, request *http.Request, user map[string]any) {
	server.setScenarioStatus(response, request, user, "active")
}

func (server *Server) deactivateScenario(response http.ResponseWriter, request *http.Request, user map[string]any) {
	server.setScenarioStatus(response, request, user, "draft")
}

func (server *Server) setScenarioStatus(response http.ResponseWriter, request *http.Request, user map[string]any, status string) {
	id, ok := scenarioID(normalizePath(request.URL.Path))
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid scenario id")
		return
	}
	var name, graphJSON string
	if err := server.pool.QueryRow(request.Context(), `SELECT name,graph_json FROM scenarios WHERE id=$1`, id).Scan(&name, &graphJSON); errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Сценарий не найден")
		return
	} else if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if status == "active" {
		if _, valid := scenario.Parse(graphJSON); !valid {
			writeError(response, http.StatusBadRequest, "Граф должен быть связным DAG с одним корневым узлом «Условие» и корректными ветками Да/Нет")
			return
		}
	}
	actor, _ := user["username"].(string)
	action := "deactivate_scenario"
	if status == "active" {
		action = "activate_scenario"
	}
	now := time.Now().UTC()
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	_, err = tx.Exec(request.Context(), `UPDATE scenarios SET status=$2,updated_at=$3 WHERE id=$1`, id, status, now)
	if err == nil {
		_, err = tx.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,$2,$3,$4)`, actor, action, name, now)
	}
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func scenarioID(path string) (int64, bool) {
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/activate"), "/deactivate")
	return pathInt(path, "/api/scenarios/")
}

type slaRequest struct {
	Name              string  `json:"name"`
	Priority          string  `json:"priority"`
	Subsidiary        *string `json:"subsidiary"`
	ServiceID         *string `json:"service_id"`
	ResponseMinutes   int     `json:"response_minutes"`
	ResolutionMinutes int     `json:"resolution_minutes"`
}

func (server *Server) createSLARule(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload slaRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.Name) == "" || payload.ResponseMinutes < 1 || payload.ResolutionMinutes < 1 || !validPriority(payload.Priority) {
		writeError(response, http.StatusUnprocessableEntity, "invalid SLA rule")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	var id int64
	err = tx.QueryRow(request.Context(), `INSERT INTO sla_rules(name,priority,subsidiary,service_id,response_minutes,resolution_minutes,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, strings.TrimSpace(payload.Name), payload.Priority, payload.Subsidiary, payload.ServiceID, payload.ResponseMinutes, payload.ResolutionMinutes, now).Scan(&id)
	if err == nil {
		_, err = tx.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'create_sla_rule',$2,$3)`, actor, strings.TrimSpace(payload.Name), now)
	}
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": id})
}

func validPriority(value string) bool {
	return value == "P0" || value == "P1" || value == "P2" || value == "P3"
}
