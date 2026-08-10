package adminapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

// Раздел «История изменений»: карта объекта/сотрудника читает Postgres
// напрямую (быстро, доступно даже если Redpanda/ClickHouse отстают),
// кросс-сущностный low-code поиск — ClickHouse (changeHistorySearch).

type changeHistoryItem struct {
	ID         int64  `json:"id"`
	OccurredAt string `json:"occurred_at"`
	Actor      string `json:"actor"`
	ActorRole  string `json:"actor_role"`
	Action     string `json:"action"`
	Result     string `json:"result"`
	BeforeJSON string `json:"before_json"`
	AfterJSON  string `json:"after_json"`
}

func (server *Server) equipmentHistory(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	path := strings.TrimSuffix(normalizePath(request.URL.Path), "/history")
	rawID := strings.TrimPrefix(path, "/api/equipment/")
	objectID, err := url.PathUnescape(rawID)
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	items, err := server.queryChangeHistory(request, "cmdb_object", objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (server *Server) employeeHistory(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	path := strings.TrimSuffix(normalizePath(request.URL.Path), "/history")
	id, ok := pathInt(path, "/api/employees/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee id")
		return
	}
	items, err := server.queryChangeHistory(request, "subscriber", strconv.FormatInt(id, 10))
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (server *Server) queryChangeHistory(request *http.Request, resourceType, resourceID string) ([]changeHistoryItem, error) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT id,occurred_at,actor,actor_role,action,result,before_json,after_json
		FROM change_events WHERE resource_type=$1 AND resource_id=$2 ORDER BY id DESC LIMIT 50`,
		resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]changeHistoryItem, 0)
	for rows.Next() {
		var id int64
		var occurredAt time.Time
		var actor, actorRole, action, result string
		var before, after sql.NullString
		if err := rows.Scan(&id, &occurredAt, &actor, &actorRole, &action, &result, &before, &after); err != nil {
			return nil, err
		}
		items = append(items, changeHistoryItem{
			ID: id, OccurredAt: formatISO(occurredAt), Actor: actor, ActorRole: actorRole,
			Action: action, Result: result, BeforeJSON: before.String, AfterJSON: after.String,
		})
	}
	return items, rows.Err()
}

func (server *Server) changeHistoryFields(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	writeJSON(response, http.StatusOK, changelog.Fields)
}

func (server *Server) changeHistorySearch(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	if server.clickhouse == nil {
		writeError(response, http.StatusServiceUnavailable, "поиск по истории временно недоступен (ClickHouse не подключен)")
		return
	}
	var req changelog.SearchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid search payload")
		return
	}
	items, err := changelog.Search(request.Context(), server.clickhouse, req)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, items)
}
