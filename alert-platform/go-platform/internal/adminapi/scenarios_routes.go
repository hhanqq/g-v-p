package adminapi

import (
	"net/http"
	"strconv"
	"strings"
)

// routeScenarios обрабатывает /api/scenarios и вложенные под-ресурсы
// (версии графа). Вынесено в отдельный диспетчер по образцу routeGroups
// (groups.go) — specific-suffix маршруты вроде .../versions должны
// разбираться раньше общего GET /api/scenarios/{id}, иначе тонут в нём;
// тот же урок уже дважды наступал на плоской if-цепочке server.go
// (.../history, .../subscription-suggestion).
func (server *Server) routeScenarios(response http.ResponseWriter, request *http.Request, path string) bool {
	if path != "/api/scenarios" && !strings.HasPrefix(path, "/api/scenarios/") {
		return false
	}
	if path == "/api/scenarios" {
		switch request.Method {
		case http.MethodGet:
			server.withAuth(response, request, server.listScenarios)
		case http.MethodPost:
			server.withAuth(response, request, server.createScenario)
		default:
			return false
		}
		return true
	}
	rest := strings.TrimPrefix(path, "/api/scenarios/")
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	scenarioID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid scenario id")
		return true
	}
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodGet:
			server.withAuth(response, request, server.getScenario)
		case http.MethodPut:
			server.withAuth(response, request, server.updateScenario)
		default:
			return false
		}
		return true
	}
	if len(segments) == 2 && segments[1] == "activate" && request.Method == http.MethodPost {
		server.withAuth(response, request, server.activateScenario)
		return true
	}
	if len(segments) == 2 && segments[1] == "deactivate" && request.Method == http.MethodPost {
		server.withAuth(response, request, server.deactivateScenario)
		return true
	}
	if len(segments) == 2 && segments[1] == "versions" && request.Method == http.MethodGet {
		server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.listScenarioVersions(w, r, scenarioID)
		})
		return true
	}
	if len(segments) == 3 && segments[1] == "versions" && request.Method == http.MethodGet {
		version, err := strconv.ParseInt(segments[2], 10, 64)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid scenario version")
			return true
		}
		server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.getScenarioVersion(w, r, scenarioID, version)
		})
		return true
	}
	return false
}
