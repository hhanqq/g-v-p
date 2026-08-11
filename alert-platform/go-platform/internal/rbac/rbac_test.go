package rbac

import "testing"

func TestEffectivePermissionsRoleBaseline(t *testing.T) {
	grant := Grant{Role: RoleEngineer}
	eff := grant.Effective()
	if !eff[AlertsRead] {
		t.Fatal("engineer should have alerts.read by default")
	}
	if eff[UsersManage] {
		t.Fatal("engineer should not have users.manage by default")
	}
}

func TestEffectivePermissionsIndividualDeny(t *testing.T) {
	grant := Grant{Role: RoleDispatcher, Overrides: map[Permission]bool{SLAManage: false}}
	eff := grant.Effective()
	if eff[SLAManage] {
		t.Fatal("individual deny must override role grant for sla.manage")
	}
	if !eff[SLARead] {
		t.Fatal("sla.read should remain granted, deny is scoped to sla.manage only")
	}
}

func TestEffectivePermissionsIndividualGrant(t *testing.T) {
	grant := Grant{Role: RoleEngineer, Overrides: map[Permission]bool{AnalyticsRead: true}}
	if !grant.Has(AnalyticsRead) {
		t.Fatal("individual grant must add a permission the role doesn't have by default")
	}
}

func TestPlatformAdminHasEverything(t *testing.T) {
	grant := Grant{Role: RolePlatformAdmin}
	for _, p := range AllPermissions {
		if !grant.Has(p) {
			t.Fatalf("platform_admin missing permission %s", p)
		}
	}
}

func TestAllowsSiteWithoutScope(t *testing.T) {
	grant := Grant{Role: RoleEngineer}
	if !grant.AllowsSite("brd-noyabrsk") {
		t.Fatal("no scope rows should mean unrestricted")
	}
}

func TestAllowsSiteWithScope(t *testing.T) {
	grant := Grant{Role: RoleEngineer, Scopes: []Scope{{Type: ScopeSite, Value: "brd-noyabrsk"}}}
	if !grant.AllowsSite("brd-noyabrsk") {
		t.Fatal("scoped site should be allowed")
	}
	if grant.AllowsSite("brd-khantos") {
		t.Fatal("non-scoped site should be denied once any site scope exists")
	}
}
