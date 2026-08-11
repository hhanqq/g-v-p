//go:build integration

package adminapi

import (
	"context"
	"testing"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/testutil"
)

// TestResolveGrantBootstrapsThenPersistsOverrides — раздел «PostgreSQL
// integration тесты» доп. ТЗ + живая проверка того же механизма, что
// реально использовался для пользователя тестер1 этой итерации: первый
// LDAP-логин бутстрапит platform_users с ролью по умолчанию, повторный
// логин находит уже существующую строку (не дублирует, не падает на
// UNIQUE), а индивидуальный override (тот же путь, что и PUT
// /api/users/{id}/permissions) отражается в Grant.Effective() сразу же
// на следующем resolveGrant — реальная БД, реальные внешние ключи
// platform_users→user_permission_overrides, не мок.
func TestResolveGrantBootstrapsThenPersistsOverrides(t *testing.T) {
	ctx := context.Background()
	pool, _ := testutil.NewPostgres(t)
	server := &Server{pool: pool}

	grant, userID, err := server.resolveGrant(ctx, "интеграционный.тест", false, false)
	if err != nil {
		t.Fatalf("first resolveGrant (bootstrap): %v", err)
	}
	if userID == 0 {
		t.Fatal("expected a real platform_users.id after bootstrap")
	}
	if grant.Role != rbac.RoleEngineer {
		t.Fatalf("expected default role %q for a non-admin LDAP user, got %q", rbac.RoleEngineer, grant.Role)
	}
	if grant.Has(rbac.AIUse) {
		t.Fatal("engineer role must not have ai.use by default (matches the real starter-permission gap that тестер1 needed granted explicitly)")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_users WHERE username=$1`, "интеграционный.тест").Scan(&count); err != nil {
		t.Fatalf("count platform_users: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 platform_users row after bootstrap, got %d", count)
	}

	grantAgain, userIDAgain, err := server.resolveGrant(ctx, "интеграционный.тест", false, false)
	if err != nil {
		t.Fatalf("second resolveGrant (existing row): %v", err)
	}
	if userIDAgain != userID {
		t.Fatalf("second login must resolve to the same platform_users.id, got %d want %d", userIDAgain, userID)
	}
	if grantAgain.Role != grant.Role {
		t.Fatalf("role must be stable across logins, got %q want %q", grantAgain.Role, grant.Role)
	}

	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_permission_overrides(user_id,permission,effect,created_at) VALUES($1,$2,'grant',$3)`,
		userID, string(rbac.AIUse), now,
	); err != nil {
		t.Fatalf("insert override: %v", err)
	}

	grantWithOverride, _, err := server.resolveGrant(ctx, "интеграционный.тест", false, false)
	if err != nil {
		t.Fatalf("resolveGrant after override: %v", err)
	}
	if !grantWithOverride.Has(rbac.AIUse) {
		t.Fatal("expected ai.use to be granted after writing an individual override, same mechanism as PUT /api/users/{id}/permissions")
	}
}
