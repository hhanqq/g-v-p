package adminapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

func (server *Server) handleLegacyRedirect(response http.ResponseWriter, request *http.Request, path string) bool {
	if request.Method != http.MethodGet {
		return false
	}
	targets := map[string]string{
		"/":           "/console/app/",
		"/dashboard":  "/console/app/",
		"/sources":    "/console/app/sources",
		"/sources/":   "/console/app/sources",
		"/audit":      "/console/app/audit",
		"/audit/":     "/console/app/audit",
		"/demo":       "/console/app/demo",
		"/compliance": "/console/app/compliance",
	}
	target, ok := targets[path]
	if !ok {
		return false
	}
	http.Redirect(response, request, target, http.StatusPermanentRedirect)
	return true
}

func (server *Server) isDemoRoute(method, path string) bool {
	if server.demo == nil {
		return false
	}
	return method == http.MethodGet && (path == "/api/demo/scenarios" || path == "/api/ai-selftest" || path == "/api/system-stats" || strings.HasPrefix(path, "/api/ai-demo/")) ||
		method == http.MethodPost && strings.HasPrefix(path, "/api/trigger/")
}

func (server *Server) listSources(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `SELECT id,instance,system,site,api_token,created_at FROM source_instances ORDER BY instance`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var instance, system, site string
		var token sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&id, &instance, &system, &site, &token, &createdAt); err != nil {
			writeError(response, http.StatusInternalServerError, "scan sources")
			return
		}
		items = append(items, map[string]any{
			"id": id, "instance": instance, "system": system, "site": site,
			"api_token": nullableString(token), "created_at": formatISO(createdAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load sources")
		return
	}
	sites := make([]string, 0)
	rows, err = server.pool.Query(request.Context(), `SELECT DISTINCT site FROM cmdb_objects WHERE site<>'' ORDER BY site`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var site string
		if err := rows.Scan(&site); err != nil {
			writeError(response, http.StatusInternalServerError, "scan sites")
			return
		}
		sites = append(sites, site)
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"items": items, "systems": []string{"solarwinds", "zabbix"}, "sites": sites,
	})
}

type sourceRequest struct {
	Instance string `json:"instance"`
	System   string `json:"system"`
	Site     string `json:"site"`
}

func (server *Server) addSource(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload sourceRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid source payload")
		return
	}
	payload.Instance, payload.System, payload.Site = strings.TrimSpace(payload.Instance), strings.TrimSpace(payload.System), strings.TrimSpace(payload.Site)
	if payload.Instance == "" || payload.Site == "" || (payload.System != "zabbix" && payload.System != "solarwinds") {
		writeError(response, http.StatusUnprocessableEntity, "instance, supported system and site are required")
		return
	}
	actor, _ := user["username"].(string)
	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	token, err := generateSourceToken()
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "token generation failed")
		return
	}
	now := time.Now().UTC()
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO source_instances(instance,system,site,api_token,created_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, payload.Instance, payload.System, payload.Site, token, now).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(response, http.StatusConflict, "инстанс уже зарегистрирован")
			return
		}
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_log(actor,action,target,detail,created_at) VALUES($1,'source_add',$2,$3,$4)`, actor, payload.Instance, "system="+payload.System+" site="+payload.Site, now)
	if err == nil {
		err = changelog.Record(ctx, tx, changelog.Event{
			OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "source_instance.create",
			ResourceType: "source_instance", ResourceID: strconv.FormatInt(id, 10),
			After: map[string]any{"instance": payload.Instance, "system": payload.System, "site": payload.Site},
		})
	}
	if err != nil || tx.Commit(ctx) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"id": id, "instance": payload.Instance, "system": payload.System, "site": payload.Site,
		"api_token": token, "created_at": formatISO(now),
	})
}

// generateSourceToken — X-Source-Token для POST /api/v1/ingest/raw
// (go-platform/internal/gateway/http.go). Каждый новый источник получает
// его автоматически при регистрации — раздел «Информационная
// безопасность», закрывает принятый риск неаутентифицированного шлюза
// без изменения обратной совместимости уже существующих источников.
func generateSourceToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (server *Server) deleteSource(response http.ResponseWriter, request *http.Request, user map[string]any) {
	rawID := strings.Trim(strings.TrimPrefix(normalizePath(request.URL.Path), "/api/sources/"), "/")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid source id")
		return
	}
	actor, _ := user["username"].(string)
	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var instance string
	err = tx.QueryRow(ctx, `DELETE FROM source_instances WHERE id=$1 RETURNING instance`, id).Scan(&instance)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "источник не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'source_delete',$2,$3)`, actor, instance, now); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	err = changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "source_instance.delete",
		ResourceType: "source_instance", ResourceID: strconv.FormatInt(id, 10),
		Before: map[string]any{"instance": instance},
	})
	if err != nil || tx.Commit(ctx) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) listAudit(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	limit := queryInt(request, "limit", 200)
	if limit < 1 || limit > 1000 {
		limit = 200
	}
	rows, err := server.pool.Query(request.Context(), `SELECT id,actor,action,target,detail,created_at FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var actor, action string
		var target, detail sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&id, &actor, &action, &target, &detail, &createdAt); err != nil {
			writeError(response, http.StatusInternalServerError, "scan audit")
			return
		}
		items = append(items, map[string]any{"id": id, "actor": actor, "action": action, "target": nullableString(target), "detail": nullableString(detail), "created_at": formatISO(createdAt)})
	}
	writeJSON(response, http.StatusOK, items)
}
