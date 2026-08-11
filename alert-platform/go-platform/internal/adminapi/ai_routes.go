package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeAI — POST /api/ai/chat (раздел «ADP AI» доп. ТЗ). Поток строго
// User → intent/tool selection (Ollama) → Allowed tool (ai_tools.go) →
// Permission check → Application Use Case → Repository → Journal +
// (для значимых действий) Global Audit. LLM никогда не трогает БД
// напрямую и не формирует итоговый текст с фактами — см. комментарий в
// ai_tools.go про защиту от галлюцинаций для MVP.
func (server *Server) routeAI(response http.ResponseWriter, request *http.Request, path string) bool {
	if server.routeAIJournal(response, request, path) {
		return true
	}
	if path == "/api/ai/chat" && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.AIUse, server.aiChat)
		return true
	}
	if path == "/api/ai/tools" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.AIUse, server.aiListTools)
		return true
	}
	return false
}

// aiListTools — раздел ADP AI UI: список быстрых команд/инструментов,
// доступных ЭТОМУ пользователю (permission уже отфильтрован) — фронтенд
// строит кнопки-подсказки из реального реестра, не хардкодит список.
func (server *Server) aiListTools(response http.ResponseWriter, request *http.Request, user map[string]any) {
	grant, _ := user["_grant"].(rbac.Grant)
	items := make([]map[string]any, 0, len(aiToolRegistry))
	for _, tool := range aiToolRegistry {
		if tool.Permission != "" && !grant.Has(tool.Permission) {
			continue
		}
		items = append(items, map[string]any{
			"name": tool.Name, "description": tool.Description, "action_type": tool.ActionType,
		})
	}
	writeJSON(response, http.StatusOK, items)
}

type aiChatRequest struct {
	Message   string         `json:"message"`
	SessionID string         `json:"session_id"`
	Context   map[string]any `json:"context"`
}

type aiChatResponse struct {
	Status      string        `json:"status"`
	Message     string        `json:"message"`
	ToolName    string        `json:"tool_name,omitempty"`
	Entities    []aiEntityRef `json:"entities,omitempty"`
	Navigate    *aiEntityRef  `json:"navigate,omitempty"`
	Data        any           `json:"data,omitempty"`
	Explanation string        `json:"explanation,omitempty"`
}

func (server *Server) aiChat(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload aiChatRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<16))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.Message) == "" {
		writeError(response, http.StatusUnprocessableEntity, "message is required")
		return
	}
	payload.Message = strings.TrimSpace(payload.Message)
	ctx := request.Context()
	username, _ := user["username"].(string)
	grant, _ := user["_grant"].(rbac.Grant)
	started := time.Now()

	// Раздел «ADP AI UI»: Ollama недоступна → ADP AI сообщает об этом,
	// остальная платформа (все прочие admin API endpoints) работает как
	// обычно — это не общий health-gate, а локальная деградация одного
	// запроса.
	if server.ollama == nil {
		server.recordAIJournal(ctx, aiJournalEntry{
			Username: username, SessionID: payload.SessionID, RequestText: payload.Message,
			ActionType: "read", Status: "FAILED", ErrorCode: "ollama_not_configured",
			DurationMs: int(time.Since(started).Milliseconds()),
		})
		writeJSON(response, http.StatusOK, aiChatResponse{
			Status: "FAILED", Message: "ADP AI временно недоступен. Основные функции ADP продолжают работать.",
		})
		return
	}

	toolName, params, reachable := server.aiSelectTool(ctx, payload.Message, payload.Context)
	if !reachable {
		server.recordAIJournal(ctx, aiJournalEntry{
			Username: username, SessionID: payload.SessionID, RequestText: payload.Message,
			ActionType: "read", Status: "FAILED", ErrorCode: "ollama_unreachable",
			DurationMs: int(time.Since(started).Milliseconds()), Model: server.ollama.Model(),
		})
		writeJSON(response, http.StatusOK, aiChatResponse{
			Status: "FAILED", Message: "ADP AI временно недоступен. Основные функции ADP продолжают работать.",
		})
		return
	}
	if toolName == "" || toolName == "none" {
		server.recordAIJournal(ctx, aiJournalEntry{
			Username: username, SessionID: payload.SessionID, RequestText: payload.Message,
			ActionType: "read", Status: "FAILED", ErrorCode: "no_tool_selected",
			DurationMs: int(time.Since(started).Milliseconds()), Model: server.ollama.Model(),
		})
		writeJSON(response, http.StatusOK, aiChatResponse{
			Status:  "FAILED",
			Message: "Не понял запрос. Попробуйте, например: «покажи активные P0», «кто сейчас дежурит», «покажи разрывы покрытия».",
		})
		return
	}

	tool, known := findAITool(toolName)
	if !known {
		server.recordAIJournal(ctx, aiJournalEntry{
			Username: username, SessionID: payload.SessionID, RequestText: payload.Message, ToolName: toolName,
			ActionType: "read", Status: "FAILED", ErrorCode: "unknown_tool",
			DurationMs: int(time.Since(started).Milliseconds()), Model: server.ollama.Model(),
		})
		writeJSON(response, http.StatusOK, aiChatResponse{Status: "FAILED", Message: "Не понял запрос — попробуйте переформулировать."})
		return
	}

	if tool.Permission != "" && !grant.Has(tool.Permission) {
		server.recordAIJournal(ctx, aiJournalEntry{
			Username: username, SessionID: payload.SessionID, RequestText: payload.Message,
			ActionType: tool.ActionType, ToolName: tool.Name, ResourceType: tool.ResourceType,
			Status: "DENIED", ErrorCode: "permission_denied", ErrorMessage: string(tool.Permission),
			DurationMs: int(time.Since(started).Milliseconds()), Model: server.ollama.Model(),
		})
		writeJSON(response, http.StatusOK, aiChatResponse{
			Status: "DENIED", Message: "Недостаточно прав для этого запроса: " + string(tool.Permission),
		})
		return
	}

	result, err := tool.Execute(ctx, server, grant, params)
	durationMs := int(time.Since(started).Milliseconds())
	if err != nil {
		status, errorCode, message := "FAILED", "execution_error", "Не удалось выполнить запрос."
		if errors.Is(err, errAIPermissionDenied) {
			status, errorCode, message = "DENIED", "permission_denied", "Недостаточно прав для этого запроса."
		}
		server.recordAIJournal(ctx, aiJournalEntry{
			Username: username, SessionID: payload.SessionID, RequestText: payload.Message,
			ActionType: tool.ActionType, ToolName: tool.Name, ResourceType: tool.ResourceType,
			Status: status, ErrorCode: errorCode, ErrorMessage: err.Error(),
			DurationMs: durationMs, Model: server.ollama.Model(),
		})
		writeJSON(response, http.StatusOK, aiChatResponse{Status: status, Message: message})
		return
	}

	inputJSON, _ := json.Marshal(params)
	server.recordAIJournal(ctx, aiJournalEntry{
		Username: username, SessionID: payload.SessionID, RequestText: payload.Message,
		ActionType: tool.ActionType, ToolName: tool.Name, ResourceType: tool.ResourceType,
		ResourceID: firstEntityID(result), InputParameters: json.RawMessage(inputJSON),
		ResultSummary: result.Summary, Status: "SUCCESS", DurationMs: durationMs, Model: server.ollama.Model(),
	})
	// Значимое действие (не просто навигация) — также в общий Global Audit,
	// раздел ТЗ: цепочка user→AI-запрос→tool→доменное действие→результат
	// видна в одном месте, не только в ai_journal.
	if tool.ActionType != "navigate" && server.pool != nil {
		_ = changelog.Record(ctx, server.pool, changelog.Event{
			OccurredAt: time.Now().UTC(), Actor: "ADP AI", ActorRole: "ai",
			ActorType: "ai", InitiatedBy: username,
			Action: "ai." + tool.Name, ResourceType: tool.ResourceType, ResourceID: firstEntityID(result),
			After: result.Data, Result: "success",
		})
	}
	writeJSON(response, http.StatusOK, aiChatResponse{
		Status: "SUCCESS", Message: result.Summary, ToolName: tool.Name,
		Entities: result.Entities, Navigate: result.Navigate, Data: result.Data,
	})
}

func firstEntityID(result *aiToolResult) string {
	if result == nil {
		return ""
	}
	if result.Navigate != nil {
		return result.Navigate.ID
	}
	if len(result.Entities) > 0 {
		return result.Entities[0].ID
	}
	return ""
}

// aiSelectTool — единственное место, где Ollama решает, ЧТО спросить.
// Ответ модели проверяется строго: неизвестный tool/невалидный JSON →
// ok=false, вызывающий код никогда не выполняет что-то по интерпретации,
// не совпавшей с реестром буквально.
// aiSelectTool различает два разных отказа (раздел «ADP AI UI» доп. ТЗ:
// «Ollama недоступна» — отдельное сообщение, не то же самое, что «не
// понял запрос»): reachable=false — модель не ответила вовсе (недоступна/
// таймаут, тот же nil-контракт, что у OllamaClient.Ask везде в проекте);
// reachable=true, tool=="" — модель ответила, но не выбрала инструмент
// или ответ не распарсился как валидный JSON из реестра.
func (server *Server) aiSelectTool(ctx context.Context, message string, pageContext map[string]any) (tool string, params map[string]any, reachable bool) {
	prompt := buildToolSelectionPrompt(message, pageContext)
	raw := server.ollama.Ask(ctx, prompt, 300)
	if raw == nil {
		return "", nil, false
	}
	parsed, err := parseToolSelection(*raw)
	if err != nil {
		return "", nil, true
	}
	return parsed.Tool, parsed.Params, true
}

func buildToolSelectionPrompt(message string, pageContext map[string]any) string {
	var b strings.Builder
	b.WriteString("Ты — модуль выбора инструмента ADP AI, ассистента диспетчерской платформы мониторинга инфраструктуры. ")
	b.WriteString("У тебя НЕТ прямого доступа к базе данных: ты только выбираешь ОДИН инструмент из списка ниже и его параметры. ")
	b.WriteString("Ответь СТРОГО одной строкой JSON, без пояснений и без markdown: ")
	b.WriteString(`{"tool":"<имя из списка>","params":{...}}`)
	b.WriteString(` или {"tool":"none"}, если ни один инструмент не подходит.` + "\n\n")
	b.WriteString("Доступные инструменты:\n")
	for _, tool := range aiToolRegistry {
		fmt.Fprintf(&b, "- %s: %s. Параметры: %s\n", tool.Name, tool.Description, tool.ParamsHint)
	}
	if entityType, ok := pageContext["entity_type"].(string); ok && entityType != "" {
		fmt.Fprintf(&b, "\nПользователь сейчас смотрит карточку: %s %v\n", entityType, pageContext["entity_id"])
	}
	fmt.Fprintf(&b, "\nЗапрос пользователя: %s\n\nОтвет (только JSON, одна строка):", message)
	return b.String()
}

type toolSelection struct {
	Tool   string         `json:"tool"`
	Params map[string]any `json:"params"`
}

// parseToolSelection вырезает первый JSON-объект из ответа модели —
// локальные модели нередко оборачивают JSON в ```json...``` или добавляют
// вступление, даже когда прямо попросили не делать этого.
func parseToolSelection(raw string) (toolSelection, error) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return toolSelection{}, fmt.Errorf("no JSON object in model output")
	}
	var parsed toolSelection
	if err := json.Unmarshal([]byte(raw[start:end+1]), &parsed); err != nil {
		return toolSelection{}, err
	}
	if strings.TrimSpace(parsed.Tool) == "" {
		return toolSelection{}, fmt.Errorf("empty tool")
	}
	return parsed, nil
}
