package adminapi

import (
	"strings"
	"testing"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

func TestBIScopeConditionEmpty(t *testing.T) {
	args := make([]any, 0)
	if cond := biScopeCondition(nil, "event.object_id", &args); cond != "" {
		t.Fatalf("expected empty condition for no scopes, got %q", cond)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args appended, got %d", len(args))
	}
}

func TestBIScopeConditionSite(t *testing.T) {
	args := make([]any, 0)
	cond := biScopeCondition([]rbac.Scope{{Type: rbac.ScopeSite, Value: "brd-noyabrsk"}}, "event.object_id", &args)
	if !strings.Contains(cond, "event.object_id IN (SELECT id FROM cmdb_objects WHERE site = ANY($1))") {
		t.Fatalf("unexpected condition: %s", cond)
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
}

func TestBIScopeConditionCombinesMultipleTypesWithAnd(t *testing.T) {
	args := make([]any, 0)
	cond := biScopeCondition([]rbac.Scope{
		{Type: rbac.ScopeSite, Value: "brd-noyabrsk"},
		{Type: rbac.ScopeEquipmentType, Value: "network"},
	}, "cmdb.id", &args)
	if !strings.Contains(cond, " AND ") {
		t.Fatalf("expected multiple scope types ANDed together, got: %s", cond)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
}

func TestBITokenRoundTrip(t *testing.T) {
	token, err := generateBIToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(token, "bi_") {
		t.Fatalf("expected bi_ prefix, got %s", token)
	}
	hash1 := hashBIToken(token)
	hash2 := hashBIToken(token)
	if hash1 != hash2 {
		t.Fatal("hashing must be deterministic")
	}
	if hashBIToken(token+"x") == hash1 {
		t.Fatal("different tokens must not collide")
	}
}
