package adminapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// scenarioStats — раздел «Аналитика исполнения сценариев»: сколько раз
// каждый узел/ветка реально сработали за все прогоны конкретной версии
// графа. version по умолчанию — текущая активная версия сценария;
// явный ?version= позволяет посмотреть счётчики исторической версии
// (актуально, если граф с тех пор отредактировали).
func (server *Server) scenarioStats(response http.ResponseWriter, request *http.Request, id int64) {
	ctx := request.Context()
	var version int64
	if raw := request.URL.Query().Get("version"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid version")
			return
		}
		version = parsed
	} else {
		if err := server.pool.QueryRow(ctx, `SELECT version FROM scenarios WHERE id=$1`, id).Scan(&version); errors.Is(err, pgx.ErrNoRows) {
			writeError(response, http.StatusNotFound, "Сценарий не найден")
			return
		} else if err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	rows, err := server.pool.Query(ctx, `
		SELECT node_id, node_type, COALESCE(branch,''), COUNT(*)
		FROM scenario_run_steps
		WHERE scenario_id=$1 AND scenario_version=$2
		GROUP BY node_id, node_type, branch
		ORDER BY node_id, branch`, id, version)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	counters := make([]map[string]any, 0)
	for rows.Next() {
		var nodeID, nodeType, branch string
		var count int64
		if err := rows.Scan(&nodeID, &nodeType, &branch, &count); err != nil {
			writeError(response, http.StatusInternalServerError, "scan scenario stats")
			return
		}
		counters = append(counters, map[string]any{"node_id": nodeID, "node_type": nodeType, "branch": branch, "count": count})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load scenario stats")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"scenario_id": id, "version": version, "counters": counters})
}

// scenarioRuns — «живой режим»: текущие прогоны сценария (по умолчанию
// все, ?status=running — только ещё не завершённые), с контекстом
// проблемы, на которую они отвечают.
func (server *Server) scenarioRuns(response http.ResponseWriter, request *http.Request, id int64) {
	status := request.URL.Query().Get("status")
	query := `
		SELECT r.id, r.problem_id, r.current_node_id, r.status, r.step_entered_at, r.notified_count, r.scenario_version,
		       p.symptom_class, p.object_id, p.priority, p.status
		FROM scenario_runs r JOIN problems p ON p.id = r.problem_id
		WHERE r.scenario_id=$1`
	args := []any{id}
	if status != "" {
		query += ` AND r.status=$2`
		args = append(args, status)
	}
	query += ` ORDER BY r.id DESC LIMIT 200`
	rows, err := server.pool.Query(request.Context(), query, args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var runID, problemID, notifiedCount, version int64
		var currentNodeID, runStatus, symptomClass, problemStatus string
		var objectID, priority sql.NullString
		var stepEnteredAt time.Time
		if err := rows.Scan(&runID, &problemID, &currentNodeID, &runStatus, &stepEnteredAt, &notifiedCount, &version,
			&symptomClass, &objectID, &priority, &problemStatus); err != nil {
			writeError(response, http.StatusInternalServerError, "scan scenario runs")
			return
		}
		items = append(items, map[string]any{
			"run_id": runID, "problem_id": problemID, "current_node_id": currentNodeID, "status": runStatus,
			"step_entered_at": formatISO(stepEnteredAt), "notified_count": notifiedCount, "version": version,
			"symptom_class": symptomClass, "object_id": nullableString(objectID), "priority": nullableString(priority),
			"problem_status": problemStatus,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load scenario runs")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

// scenarioRunTrace — полная упорядоченная трасса одного прогона плюс
// закреплённый за ним graph_json (Фаза 1: та версия, по которой прогон
// реально шёл, не обязательно текущая) — трасса рендерится поверх ТОГО
// графа, каким он был на момент исполнения, а не сегодняшнего.
func (server *Server) scenarioRunTrace(response http.ResponseWriter, request *http.Request, id, runID int64) {
	ctx := request.Context()
	var runScenarioID, version, problemID int64
	err := server.pool.QueryRow(ctx, `SELECT scenario_id, scenario_version, problem_id FROM scenario_runs WHERE id=$1`, runID).
		Scan(&runScenarioID, &version, &problemID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && runScenarioID != id) {
		writeError(response, http.StatusNotFound, "Прогон не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var graphJSON string
	if err := server.pool.QueryRow(ctx, `SELECT graph_json FROM scenario_versions WHERE scenario_id=$1 AND version=$2`, id, version).Scan(&graphJSON); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	rows, err := server.pool.Query(ctx, `
		SELECT node_id, node_type, COALESCE(branch,''), recipients_json, entered_at
		FROM scenario_run_steps WHERE run_id=$1 ORDER BY id`, runID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	steps := make([]map[string]any, 0)
	for rows.Next() {
		var nodeID, nodeType, branch string
		var recipientsJSON sql.NullString
		var enteredAt time.Time
		if err := rows.Scan(&nodeID, &nodeType, &branch, &recipientsJSON, &enteredAt); err != nil {
			writeError(response, http.StatusInternalServerError, "scan scenario run trace")
			return
		}
		steps = append(steps, map[string]any{
			"node_id": nodeID, "node_type": nodeType, "branch": branch,
			"recipients_json": nullableString(recipientsJSON), "entered_at": formatISO(enteredAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load scenario run trace")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"run_id": runID, "problem_id": problemID, "version": version, "graph_json": graphJSON, "steps": steps,
	})
}
