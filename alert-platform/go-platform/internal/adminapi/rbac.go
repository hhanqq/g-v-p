package adminapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
	"github.com/jackc/pgx/v5"
)

var errAccountInactive = errors.New("учётная запись деактивирована в ADP")

// resolveGrant — единая точка вычисления эффективных прав для сессии.
// Guest — фиксированная read-only роль без обращения к platform_users
// (раздел 17-19 доп. ТЗ: гость — не запись в LDAP/БД пользователей, а
// собственный тип сессии). Обычный LDAP-логин бутстрапится в
// platform_users при первом входе — иначе включение RBAC молча отрезало
// бы доступ всем уже существующим пользователям в момент деплоя.
// LDAP-группа admins бутстрапит роль platform_admin, все остальные —
// engineer (самая скромная содержательная роль); дальше роль меняет
// администратор через «Пользователи и права», это не переоценивается на
// каждый вход.
func (server *Server) resolveGrant(ctx context.Context, username string, isAdmin bool, guest bool) (rbac.Grant, int64, error) {
	if guest {
		return rbac.Grant{Role: rbac.RoleGuest}, 0, nil
	}
	if server.pool == nil {
		// server.pool == nil только в модульных тестах (adminapi.New(nil, ...)
		// для маршрутов, не касающихся БД) — в проде cmd/admin-api/main.go
		// всегда падает при старте без реального пула, до вызова New(...).
		if isAdmin {
			return rbac.Grant{Role: rbac.RolePlatformAdmin}, 0, nil
		}
		return rbac.Grant{Role: rbac.RoleEngineer}, 0, nil
	}
	var userID int64
	var roleValue string
	var active bool
	err := server.pool.QueryRow(ctx, `SELECT id, role, active FROM platform_users WHERE username=$1`, username).Scan(&userID, &roleValue, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		defaultRole := rbac.RoleEngineer
		if isAdmin {
			defaultRole = rbac.RolePlatformAdmin
		}
		now := time.Now().UTC()
		insertErr := server.pool.QueryRow(ctx,
			`INSERT INTO platform_users(username,role,active,created_at,updated_at) VALUES($1,$2,TRUE,$3,$3) RETURNING id`,
			username, string(defaultRole), now,
		).Scan(&userID)
		if insertErr != nil {
			return rbac.Grant{}, 0, insertErr
		}
		roleValue, active = string(defaultRole), true
	} else if err != nil {
		return rbac.Grant{}, 0, err
	}
	if !active {
		return rbac.Grant{}, userID, errAccountInactive
	}
	overrides, err := server.loadPermissionOverrides(ctx, userID)
	if err != nil {
		return rbac.Grant{}, userID, err
	}
	scopes, err := server.loadUserScopes(ctx, userID)
	if err != nil {
		return rbac.Grant{}, userID, err
	}
	return rbac.Grant{Role: rbac.Role(roleValue), Overrides: overrides, Scopes: scopes}, userID, nil
}

func (server *Server) loadPermissionOverrides(ctx context.Context, userID int64) (map[rbac.Permission]bool, error) {
	rows, err := server.pool.Query(ctx, `SELECT permission, effect FROM user_permission_overrides WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[rbac.Permission]bool)
	for rows.Next() {
		var permission, effect string
		if err := rows.Scan(&permission, &effect); err != nil {
			return nil, err
		}
		out[rbac.Permission(permission)] = effect == "grant"
	}
	return out, rows.Err()
}

func (server *Server) loadUserScopes(ctx context.Context, userID int64) ([]rbac.Scope, error) {
	rows, err := server.pool.Query(ctx, `SELECT scope_type, scope_value FROM user_scopes WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]rbac.Scope, 0)
	for rows.Next() {
		var t, v string
		if err := rows.Scan(&t, &v); err != nil {
			return nil, err
		}
		out = append(out, rbac.Scope{Type: rbac.ScopeType(t), Value: v})
	}
	return out, rows.Err()
}

// currentUserWithGrant — общая точка входа и для withAuth/withPermission,
// и для /api/auth/me: ровно одна реализация «сессия → эффективные
// права», не две параллельных.
func (server *Server) currentUserWithGrant(request *http.Request) (map[string]any, error) {
	base, err := server.sessions.currentUser(request)
	if err != nil {
		return nil, err
	}
	username, _ := base["username"].(string)
	isAdmin, _ := base["is_admin"].(bool)
	guest, _ := base["guest"].(bool)
	grant, userID, err := server.resolveGrant(request.Context(), username, isAdmin, guest)
	if err != nil {
		return nil, err
	}
	effective := grant.Effective()
	permissionNames := make([]string, 0, len(effective))
	for permission, allowed := range effective {
		if allowed {
			permissionNames = append(permissionNames, string(permission))
		}
	}
	base["role"] = string(grant.Role)
	base["role_label"] = rbac.RoleLabels[grant.Role]
	base["permissions"] = permissionNames
	base["scopes"] = grant.Scopes
	base["platform_user_id"] = userID
	base["_grant"] = grant
	return base, nil
}

// withPermission — то же самое, что withAuth, плюс проверка конкретного
// permission на сервере (раздел 15 доп. ТЗ: "Нельзя реализовать
// permissions просто if (!permission) hideButton() — проверка должна
// быть в Go admin API"). Frontend прячет пункты меню/кнопки для
// удобства, но именно этот код — то, что реально не даёт обойти
// ограничение прямым запросом к API.
func (server *Server) withPermission(response http.ResponseWriter, request *http.Request, permission rbac.Permission, next authenticatedHandler) {
	server.withAuth(response, request, func(response http.ResponseWriter, request *http.Request, user map[string]any) {
		grant, _ := user["_grant"].(rbac.Grant)
		if !grant.Has(permission) {
			server.auditPermissionDenied(request.Context(), user, permission, request.URL.Path)
			writeError(response, http.StatusForbidden, "недостаточно прав: "+string(permission))
			return
		}
		next(response, request, user)
	})
}

// auditPermissionDenied — раздел «тестер1»/Демо-сценарий доп. ТЗ: отказ
// в праве обрывается ДО хендлера, который обычно и пишет audit_log —
// без этого отказ был бы вообще не виден в аудите, только в HTTP-логе.
// Пишет в тот же audit_log, что и обычные admin-мутации — не отдельный
// параллельный журнал отказов.
func (server *Server) auditPermissionDenied(ctx context.Context, user map[string]any, permission rbac.Permission, path string) {
	if server.pool == nil {
		return
	}
	actor, _ := user["username"].(string)
	if actor == "" {
		actor = "?"
	}
	_, _ = server.pool.Exec(ctx,
		`INSERT INTO audit_log(actor,action,target,detail,created_at) VALUES($1,'permission_denied',$2,$3,$4)`,
		actor, string(permission), path, time.Now().UTC())
}
