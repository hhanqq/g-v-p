package adminapi

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// routeBI — раздел 40-42 доп. ТЗ: отдельный аналитический API,
// логически отделённый от /api/analytics/* (который обслуживает
// собственный React UI на LDAP-сессии). Формат ответа — {"data":[...],
// "meta":{"from","to","total"}}, минимум JSON+CSV (раздел 45). Не все
// шесть эндпоинтов из примера ТЗ реализованы — только те, где scope
// (раздел 44) реально проверяем через object_id (alerts/incidents/
// equipment); агрегаты SLA/delivery/scenarios пока не привязаны к
// object_id на уровне SQL нигде в проекте (даже в собственной
// Аналитике), честнее не выдавать их через BI с нерабочим scope, чем
// молча игнорировать ограничение токена.
func (server *Server) routeBI(response http.ResponseWriter, request *http.Request, path string) bool {
	if !strings.HasPrefix(path, "/api/v1/bi/") {
		return false
	}
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}
	switch path {
	case "/api/v1/bi/alerts":
		server.withBIAuth(response, request, server.biAlerts)
	case "/api/v1/bi/incidents":
		server.withBIAuth(response, request, server.biIncidents)
	case "/api/v1/bi/equipment":
		server.withBIAuth(response, request, server.biEquipment)
	default:
		writeError(response, http.StatusNotFound, "route not found")
	}
	return true
}

func writeBIResult(response http.ResponseWriter, request *http.Request, columns []string, rows [][]string, meta map[string]any) {
	if request.URL.Query().Get("format") == "csv" {
		response.Header().Set("Content-Type", "text/csv; charset=utf-8")
		response.Header().Set("Content-Disposition", "attachment; filename=export.csv")
		writer := csv.NewWriter(response)
		_ = writer.Write(columns)
		_ = writer.WriteAll(rows)
		writer.Flush()
		return
	}
	data := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		item := make(map[string]string, len(columns))
		for i, col := range columns {
			item[col] = row[i]
		}
		data = append(data, item)
	}
	writeJSON(response, http.StatusOK, map[string]any{"data": data, "meta": meta})
}

func (server *Server) biAlerts(response http.ResponseWriter, request *http.Request, account *biAccount) {
	ctx := request.Context()
	from, to, err := parseBIRange(request)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	limit, offset := clampLimit(queryInt(request, "limit", 1000)), queryInt(request, "offset", 0)

	where := []string{"event.occurred_at >= $1", "event.occurred_at < $2"}
	args := []any{from, to}
	query := request.URL.Query()
	if site := query.Get("site"); site != "" {
		args = append(args, site)
		where = append(where, fmt.Sprintf("event.site = $%d", len(args)))
	}
	if service := query.Get("service"); service != "" {
		args = append(args, service)
		where = append(where, fmt.Sprintf("event.object_id IN (SELECT object_id FROM cmdb_service_objects WHERE service_id=$%d)", len(args)))
	}
	if priority := query.Get("priority"); priority != "" {
		args = append(args, strings.Split(priority, ","))
		where = append(where, fmt.Sprintf("problem.priority = ANY($%d)", len(args)))
	}
	if status := query.Get("status"); status != "" {
		cond, err := compileEnumSetCondition(strings.Split(status, ","), alertStatusValues)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if cond != "" {
			where = append(where, cond)
		}
	}
	if cond := biScopeCondition(account.Scopes, "event.object_id", &args); cond != "" {
		where = append(where, cond)
	}
	condition := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := server.pool.QueryRow(ctx, `SELECT count(*)`+alertFilterFrom+condition, args...).Scan(&total); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	args = append(args, limit, offset)
	rows, err := server.pool.Query(ctx, `
		SELECT event.id, event.occurred_at, signal.source_system, event.object_id, event.site,
		       event.symptom_class, problem.priority, problem.status, problem.incident_id`+
		alertFilterFrom+condition+
		fmt.Sprintf(" ORDER BY event.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	records := make([][]string, 0)
	for rows.Next() {
		var id int64
		var occurredAt time.Time
		var sourceSystem, symptomClass string
		var objectID, site, priority, status sql.NullString
		var incidentID sql.NullInt64
		if err := rows.Scan(&id, &occurredAt, &sourceSystem, &objectID, &site, &symptomClass, &priority, &status, &incidentID); err != nil {
			writeError(response, http.StatusInternalServerError, "scan bi alerts")
			return
		}
		records = append(records, []string{
			strconv.FormatInt(id, 10), formatISO(occurredAt), sourceSystem, objectID.String, site.String,
			symptomClass, priority.String, status.String, formatNullableIDString(incidentID),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load bi alerts")
		return
	}
	writeBIResult(response, request,
		[]string{"id", "occurred_at", "source_system", "object_id", "site", "symptom_class", "priority", "status", "incident_id"},
		records, map[string]any{"from": formatISO(from), "to": formatISO(to), "total": total})
}

func (server *Server) biIncidents(response http.ResponseWriter, request *http.Request, account *biAccount) {
	ctx := request.Context()
	from, to, err := parseBIRange(request)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	limit, offset := clampLimit(queryInt(request, "limit", 1000)), queryInt(request, "offset", 0)

	where := []string{"incident.opened_at >= $1", "incident.opened_at < $2"}
	args := []any{from, to}
	query := request.URL.Query()
	if site := query.Get("site"); site != "" {
		args = append(args, site)
		where = append(where, fmt.Sprintf("root.site = $%d", len(args)))
	}
	if priority := query.Get("priority"); priority != "" {
		args = append(args, strings.Split(priority, ","))
		where = append(where, fmt.Sprintf("incident.priority = ANY($%d)", len(args)))
	}
	if status := query.Get("status"); status != "" {
		if status == "open" {
			where = append(where, "incident.closed_at IS NULL")
		} else if status == "closed" {
			where = append(where, "incident.closed_at IS NOT NULL")
		}
	}
	if cond := biScopeCondition(account.Scopes, "root.object_id", &args); cond != "" {
		where = append(where, cond)
	}
	condition := " WHERE " + strings.Join(where, " AND ")
	from_ := `FROM incidents incident LEFT JOIN problems root ON root.id = incident.root_problem_id`

	var total int64
	if err := server.pool.QueryRow(ctx, `SELECT count(*) `+from_+condition, args...).Scan(&total); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	args = append(args, limit, offset)
	rows, err := server.pool.Query(ctx, `
		SELECT incident.id, incident.priority, incident.opened_at, incident.closed_at, root.object_id, root.site, root.symptom_class
		`+from_+condition+fmt.Sprintf(" ORDER BY incident.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	records := make([][]string, 0)
	for rows.Next() {
		var id int64
		var priority, objectID, site, symptomClass sql.NullString
		var openedAt time.Time
		var closedAt sql.NullTime
		if err := rows.Scan(&id, &priority, &openedAt, &closedAt, &objectID, &site, &symptomClass); err != nil {
			writeError(response, http.StatusInternalServerError, "scan bi incidents")
			return
		}
		closed := ""
		if closedAt.Valid {
			closed = formatISO(closedAt.Time)
		}
		records = append(records, []string{
			strconv.FormatInt(id, 10), priority.String, formatISO(openedAt), closed, objectID.String, site.String, symptomClass.String,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load bi incidents")
		return
	}
	writeBIResult(response, request,
		[]string{"id", "priority", "opened_at", "closed_at", "root_object_id", "site", "root_symptom_class"},
		records, map[string]any{"from": formatISO(from), "to": formatISO(to), "total": total})
}

func (server *Server) biEquipment(response http.ResponseWriter, request *http.Request, account *biAccount) {
	ctx := request.Context()
	limit, offset := clampLimit(queryInt(request, "limit", 1000)), queryInt(request, "offset", 0)
	where := []string{}
	args := []any{}
	query := request.URL.Query()
	if site := query.Get("site"); site != "" {
		args = append(args, site)
		where = append(where, fmt.Sprintf("cmdb.site = $%d", len(args)))
	}
	if cond := biScopeCondition(account.Scopes, "cmdb.id", &args); cond != "" {
		where = append(where, cond)
	}
	condition := ""
	if len(where) > 0 {
		condition = " WHERE " + strings.Join(where, " AND ")
	}
	from := `FROM cmdb_objects cmdb`

	var total int64
	if err := server.pool.QueryRow(ctx, `SELECT count(*) `+from+condition, args...).Scan(&total); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	args = append(args, limit, offset)
	rows, err := server.pool.Query(ctx, `
		SELECT cmdb.id, cmdb.name, cmdb.site, cmdb.equipment_type, cmdb.kind,
		       (SELECT count(*) FROM problems p WHERE p.object_id=cmdb.id AND p.status IN ('OPEN','FLAPPING'))
		`+from+condition+fmt.Sprintf(" ORDER BY cmdb.id LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	records := make([][]string, 0)
	for rows.Next() {
		var id, name, site string
		var equipmentType sql.NullString
		var kind string
		var activeProblems int64
		if err := rows.Scan(&id, &name, &site, &equipmentType, &kind, &activeProblems); err != nil {
			writeError(response, http.StatusInternalServerError, "scan bi equipment")
			return
		}
		records = append(records, []string{id, name, site, equipmentType.String, kind, strconv.FormatInt(activeProblems, 10)})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load bi equipment")
		return
	}
	writeBIResult(response, request,
		[]string{"id", "name", "site", "equipment_type", "kind", "active_problems"},
		records, map[string]any{"total": total})
}

func formatNullableIDString(v sql.NullInt64) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatInt(v.Int64, 10)
}
