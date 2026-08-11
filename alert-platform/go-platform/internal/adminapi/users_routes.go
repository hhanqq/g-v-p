package adminapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeUsers — «Администрирование → Пользователи и права» (раздел 8-16
// доп. ТЗ). Список ограничен теми, кто хотя бы раз входил в ADP: нет
// синхронизации с полным LDAP-каталогом, platform_users заполняется
// лениво при первом логине (см. rbac.go resolveGrant) — это честное
// отражение реальности, не притворство, что здесь полный список
// сотрудников компании.
func (server *Server) routeUsers(response http.ResponseWriter, request *http.Request, path string) bool {
	if path == "/api/users/meta" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.UsersRead, server.usersMeta)
		return true
	}
	if path == "/api/users" {
		if request.Method != http.MethodGet {
			return false
		}
		server.withPermission(response, request, rbac.UsersRead, server.listPlatformUsers)
		return true
	}
	if !strings.HasPrefix(path, "/api/users/") {
		return false
	}
	rest := strings.TrimPrefix(path, "/api/users/")
	switch {
	case strings.HasSuffix(rest, "/permissions"):
		idPart := strings.TrimSuffix(rest, "/permissions")
		id, ok := pathInt("/"+idPart, "/")
		if !ok {
			writeError(response, http.StatusUnprocessableEntity, "invalid user id")
			return true
		}
		if request.Method != http.MethodPut {
			return false
		}
		server.withPermission(response, request, rbac.UsersManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.updateUserPermissions(w, r, id, u)
		})
		return true
	case strings.HasSuffix(rest, "/scopes"):
		idPart := strings.TrimSuffix(rest, "/scopes")
		id, ok := pathInt("/"+idPart, "/")
		if !ok {
			writeError(response, http.StatusUnprocessableEntity, "invalid user id")
			return true
		}
		if request.Method != http.MethodPut {
			return false
		}
		server.withPermission(response, request, rbac.UsersManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.updateUserScopes(w, r, id, u)
		})
		return true
	default:
		id, ok := pathInt("/"+rest, "/")
		if !ok {
			writeError(response, http.StatusUnprocessableEntity, "invalid user id")
			return true
		}
		switch request.Method {
		case http.MethodGet:
			server.withPermission(response, request, rbac.UsersRead, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
				server.getPlatformUser(w, r, id, u)
			})
		case http.MethodPut:
			server.withPermission(response, request, rbac.UsersManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
				server.updatePlatformUser(w, r, id, u)
			})
		default:
			return false
		}
		return true
	}
}

func (server *Server) usersMeta(response http.ResponseWriter, _ *http.Request, _ map[string]any) {
	roles := make([]map[string]string, 0, len(rbac.AllRoles))
	for _, role := range rbac.AllRoles {
		if role == rbac.RoleGuest {
			continue // гость — не назначаемая роль, отдельный тип сессии (раздел 17-18)
		}
		roles = append(roles, map[string]string{"value": string(role), "label": rbac.RoleLabels[role]})
	}
	permissions := make([]map[string]string, 0, len(rbac.AllPermissions))
	for _, permission := range rbac.AllPermissions {
		permissions = append(permissions, map[string]string{"value": string(permission), "label": rbac.PermissionLabels[permission]})
	}
	// rolePermissions — permission-код по умолчанию для каждой роли, чтобы
	// карточка пользователя (раздел 12 доп. ТЗ) могла показать один
	// чекбокс на permission («эффективно да/нет»), а не отдельно роль и
	// override: несовпадение с дефолтом роли и есть override.
	rolePermissions := make(map[string][]string, len(rbac.AllRoles))
	for _, role := range rbac.AllRoles {
		if role == rbac.RoleGuest {
			continue
		}
		granted := make([]string, 0)
		for permission, allowed := range rbac.RolePermissions[role] {
			if allowed {
				granted = append(granted, string(permission))
			}
		}
		rolePermissions[string(role)] = granted
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"roles":            roles,
		"permissions":      permissions,
		"role_permissions": rolePermissions,
		"scope_types": []map[string]string{
			{"value": string(rbac.ScopeSite), "label": "Филиал"},
			{"value": string(rbac.ScopeSubsidiary), "label": "Подразделение"},
			{"value": string(rbac.ScopeService), "label": "Сервис"},
			{"value": string(rbac.ScopeEquipmentType), "label": "Категория оборудования"},
			{"value": string(rbac.ScopeObject), "label": "Конкретное оборудование"},
		},
	})
}

func (server *Server) listPlatformUsers(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT pu.id, pu.username, pu.role, pu.active,
		       (SELECT count(*) FROM user_permission_overrides o WHERE o.user_id=pu.id),
		       (SELECT count(*) FROM user_scopes s WHERE s.user_id=pu.id)
		FROM platform_users pu ORDER BY pu.username`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var username, role string
		var active bool
		var overrideCount, scopeCount int64
		if err := rows.Scan(&id, &username, &role, &active, &overrideCount, &scopeCount); err != nil {
			writeError(response, http.StatusInternalServerError, "scan users")
			return
		}
		items = append(items, map[string]any{
			"id": id, "username": username, "role": role, "role_label": rbac.RoleLabels[rbac.Role(role)],
			"active": active, "override_count": overrideCount, "scope_count": scopeCount,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load users")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (server *Server) getPlatformUser(response http.ResponseWriter, request *http.Request, id int64, _ map[string]any) {
	ctx := request.Context()
	var username, role string
	var active bool
	if err := server.pool.QueryRow(ctx, `SELECT username, role, active FROM platform_users WHERE id=$1`, id).Scan(&username, &role, &active); err != nil {
		writeError(response, http.StatusNotFound, "пользователь не найден")
		return
	}
	overrides, err := server.loadPermissionOverrides(ctx, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	scopes, err := server.loadUserScopes(ctx, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	grant := rbac.Grant{Role: rbac.Role(role), Overrides: overrides, Scopes: scopes}
	effective := grant.Effective()

	overrideList := make([]map[string]string, 0, len(overrides))
	for permission, allow := range overrides {
		effect := "deny"
		if allow {
			effect = "grant"
		}
		overrideList = append(overrideList, map[string]string{"permission": string(permission), "effect": effect})
	}
	scopeList := make([]map[string]string, 0, len(scopes))
	for _, scope := range scopes {
		scopeList = append(scopeList, map[string]string{"type": string(scope.Type), "value": scope.Value})
	}
	effectiveList := make([]string, 0, len(effective))
	for permission, allowed := range effective {
		if allowed {
			effectiveList = append(effectiveList, string(permission))
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"id": id, "username": username, "role": role, "role_label": rbac.RoleLabels[rbac.Role(role)],
		"active": active, "overrides": overrideList, "scopes": scopeList, "effective_permissions": effectiveList,
	})
}

type updateUserRequest struct {
	Role   *string `json:"role"`
	Active *bool   `json:"active"`
}

func (server *Server) updatePlatformUser(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	var payload updateUserRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid payload")
		return
	}
	if payload.Role != nil {
		valid := false
		for _, role := range rbac.AllRoles {
			if role != rbac.RoleGuest && string(role) == *payload.Role {
				valid = true
				break
			}
		}
		if !valid {
			writeError(response, http.StatusUnprocessableEntity, "неизвестная роль")
			return
		}
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
	var targetUsername string
	if err := tx.QueryRow(ctx, `SELECT username FROM platform_users WHERE id=$1`, id).Scan(&targetUsername); err != nil {
		writeError(response, http.StatusNotFound, "пользователь не найден")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE platform_users SET
			role = COALESCE($2, role),
			active = COALESCE($3, active),
			updated_at = $4
		WHERE id=$1`, id, payload.Role, payload.Active, now); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "platform_user.update",
		ResourceType: "platform_user", ResourceID: targetUsername,
		After: map[string]any{"role": payload.Role, "active": payload.Active},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

type permissionOverrideRequest struct {
	Overrides []struct {
		Permission string `json:"permission"`
		Effect     string `json:"effect"`
	} `json:"overrides"`
}

// updateUserPermissions — «Индивидуальные права» (раздел 12 доп. ТЗ):
// администратор присылает полный желаемый набор исключений поверх роли,
// не инкремент — таблица перезаписывается целиком в одной транзакции.
func (server *Server) updateUserPermissions(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	var payload permissionOverrideRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid payload")
		return
	}
	knownPermissions := make(map[string]bool, len(rbac.AllPermissions))
	for _, permission := range rbac.AllPermissions {
		knownPermissions[string(permission)] = true
	}
	for _, override := range payload.Overrides {
		if !knownPermissions[override.Permission] {
			writeError(response, http.StatusUnprocessableEntity, "неизвестное право: "+override.Permission)
			return
		}
		if override.Effect != "grant" && override.Effect != "deny" {
			writeError(response, http.StatusUnprocessableEntity, "effect должен быть grant или deny")
			return
		}
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
	var targetUsername string
	if err := tx.QueryRow(ctx, `SELECT username FROM platform_users WHERE id=$1`, id).Scan(&targetUsername); err != nil {
		writeError(response, http.StatusNotFound, "пользователь не найден")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_permission_overrides WHERE user_id=$1`, id); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for _, override := range payload.Overrides {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_permission_overrides(user_id,permission,effect,created_at) VALUES($1,$2,$3,$4)`,
			id, override.Permission, override.Effect, now,
		); err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "platform_user.set_permissions",
		ResourceType: "platform_user", ResourceID: targetUsername, After: payload.Overrides,
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

type scopeRequest struct {
	Scopes []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"scopes"`
}

func (server *Server) updateUserScopes(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	var payload scopeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid payload")
		return
	}
	knownTypes := map[string]bool{
		string(rbac.ScopeSite): true, string(rbac.ScopeSubsidiary): true, string(rbac.ScopeService): true,
		string(rbac.ScopeEquipmentType): true, string(rbac.ScopeObject): true,
	}
	for _, scope := range payload.Scopes {
		if !knownTypes[scope.Type] || strings.TrimSpace(scope.Value) == "" {
			writeError(response, http.StatusUnprocessableEntity, "некорректный scope")
			return
		}
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
	var targetUsername string
	if err := tx.QueryRow(ctx, `SELECT username FROM platform_users WHERE id=$1`, id).Scan(&targetUsername); err != nil {
		writeError(response, http.StatusNotFound, "пользователь не найден")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_scopes WHERE user_id=$1`, id); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for _, scope := range payload.Scopes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_scopes(user_id,scope_type,scope_value,created_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			id, scope.Type, scope.Value, now,
		); err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "platform_user.set_scopes",
		ResourceType: "platform_user", ResourceID: targetUsername, After: payload.Scopes,
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
