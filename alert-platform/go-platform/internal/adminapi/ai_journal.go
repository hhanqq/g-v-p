package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeAIJournal — GET /api/ai/journal (раздел «ADP AI» доп. ТЗ): экран
// «Журнал», отдельная запись на КАЖДЫЙ запрос к ассистенту (успешный и
// нет), не только значимые доменные действия — те дополнительно попадают
// в Global Audit (change_events, см. aiChat в ai_routes.go).
func (server *Server) routeAIJournal(response http.ResponseWriter, request *http.Request, path string) bool {
	if path == "/api/ai/journal" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.AIAuditRead, server.listAIJournal)
		return true
	}
	return false
}

type aiJournalEntry struct {
	Username        string
	SessionID       string
	RequestText     string
	ActionType      string
	ToolName        string
	ResourceType    string
	ResourceID      string
	InputParameters any
	ResultSummary   string
	Status          string
	DurationMs      int
	Model           string
	ModelVersion    string
	Explanation     string
	ErrorCode       string
	ErrorMessage    string
}

func (server *Server) recordAIJournal(ctx context.Context, entry aiJournalEntry) {
	if server.pool == nil {
		return
	}
	var inputJSON *string
	if entry.InputParameters != nil {
		if encoded, err := json.Marshal(entry.InputParameters); err == nil {
			text := string(encoded)
			inputJSON = &text
		}
	}
	// best-effort: раздел «ИИ работает best-effort», журналирование самого
	// журнала не должно ронять запрос — ошибка записи здесь молча
	// игнорируется, как и остальные необязательные наблюдаемость-записи
	// в проекте (changelog.Record best-effort вызовы).
	_, _ = server.pool.Exec(ctx, `
		INSERT INTO ai_journal(
			created_at, username, session_id, request_text, action_type, tool_name,
			resource_type, resource_id, input_parameters, result_summary, status,
			duration_ms, model, model_version, explanation, error_code, error_message
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		time.Now().UTC(), entry.Username, nullIfEmptyString(entry.SessionID), entry.RequestText,
		entry.ActionType, nullIfEmptyString(entry.ToolName), nullIfEmptyString(entry.ResourceType),
		nullIfEmptyString(entry.ResourceID), inputJSON, nullIfEmptyString(entry.ResultSummary), entry.Status,
		nullableIntValue(entry.DurationMs), nullIfEmptyString(entry.Model), nullIfEmptyString(entry.ModelVersion),
		nullIfEmptyString(entry.Explanation), nullIfEmptyString(entry.ErrorCode), nullIfEmptyString(entry.ErrorMessage),
	)
}

func nullIfEmptyString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func nullableIntValue(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

// listAIJournal — фильтры Период/Пользователь/Tool/Статус/Тип объекта
// (раздел «ADP AI» доп. ТЗ). period здесь — query-параметры from/to
// (RFC3339), не общий parseAnalyticsRange: журнал ИИ не входит в
// аналитику пайплайна и не должен зависеть от её пресетов.
func (server *Server) listAIJournal(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	query := request.URL.Query()
	where := make([]string, 0, 5)
	args := make([]any, 0, 5)
	if username := strings.TrimSpace(query.Get("username")); username != "" {
		args = append(args, username)
		where = append(where, "username = $"+strconv.Itoa(len(args)))
	}
	if tool := strings.TrimSpace(query.Get("tool")); tool != "" {
		args = append(args, tool)
		where = append(where, "tool_name = $"+strconv.Itoa(len(args)))
	}
	if status := strings.TrimSpace(query.Get("status")); status != "" {
		args = append(args, status)
		where = append(where, "status = $"+strconv.Itoa(len(args)))
	}
	if resourceType := strings.TrimSpace(query.Get("resource_type")); resourceType != "" {
		args = append(args, resourceType)
		where = append(where, "resource_type = $"+strconv.Itoa(len(args)))
	}
	if from, err := time.Parse(time.RFC3339, query.Get("from")); err == nil {
		args = append(args, from)
		where = append(where, "created_at >= $"+strconv.Itoa(len(args)))
	}
	if to, err := time.Parse(time.RFC3339, query.Get("to")); err == nil {
		args = append(args, to)
		where = append(where, "created_at <= $"+strconv.Itoa(len(args)))
	}
	condition := ""
	if len(where) > 0 {
		condition = " WHERE " + strings.Join(where, " AND ")
	}
	limit := queryInt(request, "limit", 200)
	if limit < 1 || limit > 1000 {
		limit = 200
	}
	args = append(args, limit)
	rows, err := server.pool.Query(request.Context(), `
		SELECT id, created_at, username, session_id, request_text, action_type, tool_name,
		       resource_type, resource_id, result_summary, status, duration_ms, model,
		       explanation, error_code, error_message
		FROM ai_journal`+condition+` ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var createdAt time.Time
		var username, requestText, actionType, status string
		var sessionID, toolName, resourceType, resourceID, resultSummary, model, explanation, errorCode, errorMessage sql.NullString
		var durationMs sql.NullInt32
		if err := rows.Scan(&id, &createdAt, &username, &sessionID, &requestText, &actionType, &toolName,
			&resourceType, &resourceID, &resultSummary, &status, &durationMs, &model,
			&explanation, &errorCode, &errorMessage); err != nil {
			writeError(response, http.StatusInternalServerError, "scan ai_journal")
			return
		}
		items = append(items, map[string]any{
			"id": id, "created_at": formatISO(createdAt), "username": username, "session_id": nullableString(sessionID),
			"request_text": requestText, "action_type": actionType, "tool_name": nullableString(toolName),
			"resource_type": nullableString(resourceType), "resource_id": nullableString(resourceID),
			"result_summary": nullableString(resultSummary), "status": status,
			"duration_ms": nullableInt32(durationMs), "model": nullableString(model),
			"explanation": nullableString(explanation), "error_code": nullableString(errorCode),
			"error_message": nullableString(errorMessage),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load ai_journal")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func nullableInt32(value sql.NullInt32) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}
