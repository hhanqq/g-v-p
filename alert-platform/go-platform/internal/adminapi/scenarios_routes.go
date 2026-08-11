package adminapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeScenarios обрабатывает /api/scenarios и вложенные под-ресурсы
// (версии графа). Вынесено в отдельный диспетчер по образцу routeGroups
// (groups.go) — specific-suffix маршруты вроде .../versions должны
// разбираться раньше общего GET /api/scenarios/{id}, иначе тонут в нём;
// тот же урок уже дважды наступал на плоской if-цепочке server.go
// (.../history, .../subscription-suggestion). activate/deactivate
// гейтятся отдельным scenarios.activate — раздел 11 доп. ТЗ явно
// разводит его с scenarios.manage (правка графа).
func (server *Server) routeScenarios(response http.ResponseWriter, request *http.Request, path string) bool {
	if path != "/api/scenarios" && !strings.HasPrefix(path, "/api/scenarios/") {
		return false
	}
	if path == "/api/scenarios" {
		switch request.Method {
		case http.MethodGet:
			server.withPermission(response, request, rbac.ScenariosRead, server.listScenarios)
		case http.MethodPost:
			server.withPermission(response, request, rbac.ScenariosManage, server.createScenario)
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
			server.withPermission(response, request, rbac.ScenariosRead, server.getScenario)
		case http.MethodPut:
			server.withPermission(response, request, rbac.ScenariosManage, server.updateScenario)
		default:
			return false
		}
		return true
	}
	if len(segments) == 2 && segments[1] == "activate" && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.ScenariosActivate, server.activateScenario)
		return true
	}
	if len(segments) == 2 && segments[1] == "deactivate" && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.ScenariosActivate, server.deactivateScenario)
		return true
	}
	if len(segments) == 2 && segments[1] == "versions" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.ScenariosRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
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
		server.withPermission(response, request, rbac.ScenariosRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.getScenarioVersion(w, r, scenarioID, version)
		})
		return true
	}
	if len(segments) == 2 && segments[1] == "stats" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.ScenariosRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.scenarioStats(w, r, scenarioID)
		})
		return true
	}
	if len(segments) == 2 && segments[1] == "runs" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.ScenariosRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.scenarioRuns(w, r, scenarioID)
		})
		return true
	}
	if len(segments) == 4 && segments[1] == "runs" && segments[3] == "trace" && request.Method == http.MethodGet {
		runID, err := strconv.ParseInt(segments[2], 10, 64)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid run id")
			return true
		}
		server.withPermission(response, request, rbac.ScenariosRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.scenarioRunTrace(w, r, scenarioID, runID)
		})
		return true
	}
	return false
}
