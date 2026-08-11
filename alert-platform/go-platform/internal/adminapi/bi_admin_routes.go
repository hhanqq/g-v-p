package adminapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeBIAdmin — «Интеграции → BI / внешняя аналитика» (раздел 46 доп.
// ТЗ): администратор создаёт/отзывает service account здесь, под
// обычной LDAP-сессией и integrations.manage — отдельно от самих
// BI-эндпоинтов (routeBI), которые используют только токен.
func (server *Server) routeBIAdmin(response http.ResponseWriter, request *http.Request, path string) bool {
	if path == "/api/bi/service-accounts" {
		switch request.Method {
		case http.MethodGet:
			server.withPermission(response, request, rbac.IntegrationsRead, server.listBIServiceAccounts)
		case http.MethodPost:
			server.withPermission(response, request, rbac.IntegrationsManage, server.createBIServiceAccount)
		default:
			return false
		}
		return true
	}
	if strings.HasPrefix(path, "/api/bi/service-accounts/") {
		id, ok := pathInt(path, "/api/bi/service-accounts/")
		if !ok {
			writeError(response, http.StatusUnprocessableEntity, "invalid id")
			return true
		}
		if request.Method != http.MethodDelete {
			return false
		}
		server.withPermission(response, request, rbac.IntegrationsManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.revokeBIServiceAccount(w, r, id, u)
		})
		return true
	}
	return false
}

func (server *Server) listBIServiceAccounts(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT a.id, a.name, a.token_prefix, a.active, a.created_by, a.created_at, a.last_used_at,
		       (SELECT count(*) FROM bi_service_account_scopes s WHERE s.account_id = a.id)
		FROM bi_service_accounts a ORDER BY a.id DESC`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, prefix string
		var active bool
		var createdBy *string
		var createdAt time.Time
		var lastUsedAt *time.Time
		var scopeCount int64
		if err := rows.Scan(&id, &name, &prefix, &active, &createdBy, &createdAt, &lastUsedAt, &scopeCount); err != nil {
			writeError(response, http.StatusInternalServerError, "scan bi service accounts")
			return
		}
		item := map[string]any{
			"id": id, "name": name, "token_prefix": prefix, "active": active,
			"created_by": createdBy, "created_at": formatISO(createdAt), "scope_count": scopeCount,
		}
		if lastUsedAt != nil {
			item["last_used_at"] = formatISO(*lastUsedAt)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load bi service accounts")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

type createBIAccountRequest struct {
	Name   string `json:"name"`
	Scopes []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"scopes"`
}

func (server *Server) createBIServiceAccount(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload createBIAccountRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.Name) == "" {
		writeError(response, http.StatusUnprocessableEntity, "invalid payload")
		return
	}
	knownTypes := map[string]bool{
		string(rbac.ScopeSite): true, string(rbac.ScopeSubsidiary): true, string(rbac.ScopeService): true,
		string(rbac.ScopeEquipmentType): true, string(rbac.ScopeObject): true,
	}
	for _, s := range payload.Scopes {
		if !knownTypes[s.Type] || strings.TrimSpace(s.Value) == "" {
			writeError(response, http.StatusUnprocessableEntity, "некорректный scope")
			return
		}
	}
	token, err := generateBIToken()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "token generation failed")
		return
	}
	ctx := request.Context()
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO bi_service_accounts(name, token_hash, token_prefix, active, created_by, created_at)
		VALUES($1,$2,$3,TRUE,$4,$5) RETURNING id`,
		strings.TrimSpace(payload.Name), hashBIToken(token), token[:10], actor, now,
	).Scan(&id); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable (имя уже занято?)")
		return
	}
	for _, s := range payload.Scopes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO bi_service_account_scopes(account_id, scope_type, scope_value, created_at) VALUES($1,$2,$3,$4)`,
			id, s.Type, s.Value, now,
		); err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "bi_service_account.create",
		ResourceType: "bi_service_account", ResourceID: strconv.FormatInt(id, 10), After: map[string]any{"name": payload.Name, "scopes": payload.Scopes},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	// Единственный момент, когда сырой токен виден вообще кому-либо —
	// ни в БД (только hash), ни в последующих GET-ответах (только prefix).
	writeJSON(response, http.StatusCreated, map[string]any{"id": id, "token": token})
}

func (server *Server) revokeBIServiceAccount(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	ctx := request.Context()
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE bi_service_accounts SET active=FALSE WHERE id=$1`, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(response, http.StatusNotFound, "service account не найден")
		return
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "bi_service_account.revoke",
		ResourceType: "bi_service_account", ResourceID: strconv.FormatInt(id, 10),
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}
