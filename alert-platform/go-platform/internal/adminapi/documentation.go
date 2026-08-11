package adminapi

import (
	"net/http"
)

func (server *Server) openapi(response http.ResponseWriter) {
	writeJSON(response, http.StatusOK, OpenAPISpec())
}

// OpenAPISpec returns the same OpenAPI document that is served at
// /openapi.json. Documentation tooling uses it without starting the API or a
// database.
func OpenAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "ADP — Admin API (Go)",
			"version": "1.0.0",
		},
		"paths": map[string]any{
			"/api/auth/login":                          map[string]any{"post": operation("LDAP login")},
			"/api/auth/logout":                         map[string]any{"post": operation("Logout")},
			"/api/auth/me":                             map[string]any{"get": operation("Current user")},
			"/api/home/summary":                        map[string]any{"get": operation("Operational summary")},
			"/api/incidents":                           map[string]any{"get": operation("List incidents")},
			"/api/incidents/{id}":                      map[string]any{"get": operation("Incident detail")},
			"/api/alerts":                              map[string]any{"get": operation("List parsed alerts")},
			"/api/equipment":                           map[string]any{"get": operation("List equipment")},
			"/api/equipment/{id}":                      map[string]any{"get": operation("Equipment detail")},
			"/api/employees":                           map[string]any{"get": operation("List employees")},
			"/api/employees/{id}":                      map[string]any{"get": operation("Employee detail")},
			"/api/sources":                             map[string]any{"get": operation("List sources"), "post": operation("Register source")},
			"/api/sources/{id}":                        map[string]any{"delete": operation("Delete source")},
			"/api/audit":                               map[string]any{"get": operation("Read audit log")},
			"/api/scenarios":                           map[string]any{"get": operation("List no-code scenarios")},
			"/api/groups":                              map[string]any{"get": operation("List groups"), "post": operation("Create group")},
			"/api/groups/{id}":                         map[string]any{"get": operation("Group detail"), "put": operation("Update group"), "delete": operation("Delete group")},
			"/api/groups/{id}/members":                 map[string]any{"post": operation("Add group member")},
			"/api/groups/{id}/members/{subscriber_id}": map[string]any{"delete": operation("Remove group member")},
			"/api/groups/{id}/equipment":               map[string]any{"post": operation("Add equipment scope")},
			"/api/groups/{id}/equipment/{scope_id}":    map[string]any{"delete": operation("Remove equipment scope")},
			"/api/sla-rules":                           map[string]any{"get": operation("List SLA rules")},
			"/api/integrations/status":                 map[string]any{"get": operation("Integration status")},
			"/api/demo/scenarios":                      map[string]any{"get": operation("List synthetic scenarios")},
			"/api/trigger/{id}":                        map[string]any{"post": operation("Run synthetic scenario")},
		},
	}
}

func operation(summary string) map[string]any {
	return map[string]any{
		"summary": summary,
		"responses": map[string]any{
			"200": map[string]any{"description": "Success"},
		},
	}
}

func (server *Server) docs(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ADP — Admin API</title><style>body{font-family:system-ui,sans-serif;max-width:760px;margin:40px auto;padding:0 16px}code{background:#eee;padding:2px 5px;border-radius:4px}li{margin:7px 0}</style></head><body><h1>ADP — Admin API (Go)</h1><p><a href="openapi.json">OpenAPI 3.1 JSON</a> · <a href="app/">React-консоль</a></p><p>Все admin endpoints используют подписанную HttpOnly cookie, полученную через <code>POST /api/auth/login</code>. Управление источниками и аудит дополнительно требуют роль <code>admins</code>.</p><ul><li><code>/api/home/summary</code> — операционные метрики</li><li><code>/api/incidents</code>, <code>/api/alerts</code> — события и инциденты</li><li><code>/api/equipment</code>, <code>/api/employees</code> — справочники</li><li><code>/api/sources</code>, <code>/api/audit</code> — admin-only</li><li><code>/api/demo/scenarios</code>, <code>/api/trigger/{id}</code> — синтетический demo-runner</li></ul></body></html>`))
}
