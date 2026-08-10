package adminapi

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// equipmentGraph — GET /api/equipment/{id}/graph?scope=incident&incident_id=N
// или ?scope=historical&window=24h|7d|30d|90d (по умолчанию 30d). Строит
// граф на РЕАЛЬНЫХ данных: incident_problems (role+rule_id — сохранённое
// решение коррелятора, раздел II.18 ТЗ), не на совпадении имён/визуальной
// имитации. "incident" — только кластер одного инцидента (текущий, если
// не закрыт); "historical" — все кластеры, в которых объект участвовал
// за окно, с накоплением.
type graphNode struct {
	ID           string  `json:"id"`
	ProblemID    int64   `json:"problem_id"`
	IncidentID   int64   `json:"incident_id"`
	ObjectID     *string `json:"object_id"`
	ObjectName   string  `json:"object_name"`
	SymptomClass string  `json:"symptom_class"`
	Priority     *string `json:"priority"`
	Status       string  `json:"status"`
	OpenedAt     string  `json:"opened_at"`
	Role         string  `json:"role"`
}

type graphEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	RuleID *string `json:"rule_id"`
}

func (server *Server) equipmentGraph(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	objectID, err := equipmentIDFromPath(request, "/graph")
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	scope := request.URL.Query().Get("scope")
	if scope == "" {
		scope = "historical"
	}

	var query string
	var args []any
	if scope == "incident" {
		incidentID, err := strconv.ParseInt(request.URL.Query().Get("incident_id"), 10, 64)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid incident_id")
			return
		}
		query = `
			SELECT ip.incident_id, ip.problem_id, ip.role, ip.rule_id,
				problem.object_id, coalesce(object.name, problem.object_id) AS object_name,
				problem.symptom_class, problem.priority, problem.status, problem.opened_at
			FROM incident_problems ip
			JOIN problems problem ON problem.id = ip.problem_id
			LEFT JOIN cmdb_objects object ON object.id = problem.object_id
			WHERE ip.incident_id = $1
			ORDER BY ip.role ASC, problem.opened_at`
		args = []any{incidentID}
	} else {
		window := parseGraphWindow(request.URL.Query().Get("window"))
		since := time.Now().Add(-window)
		query = `
			WITH mine AS (
				SELECT DISTINCT member.incident_id
				FROM incident_problems member
				JOIN problems problem ON problem.id = member.problem_id
				JOIN incidents incident ON incident.id = member.incident_id
				WHERE problem.object_id = $1 AND incident.opened_at >= $2
			)
			SELECT ip.incident_id, ip.problem_id, ip.role, ip.rule_id,
				problem.object_id, coalesce(object.name, problem.object_id) AS object_name,
				problem.symptom_class, problem.priority, problem.status, problem.opened_at
			FROM mine
			JOIN incident_problems ip ON ip.incident_id = mine.incident_id
			JOIN problems problem ON problem.id = ip.problem_id
			LEFT JOIN cmdb_objects object ON object.id = problem.object_id
			ORDER BY ip.incident_id, ip.role ASC, problem.opened_at`
		args = []any{objectID, since}
	}

	rows, err := server.pool.Query(request.Context(), query, args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()

	nodes := make([]graphNode, 0)
	edges := make([]graphEdge, 0)
	rootOfIncident := make(map[int64]string)
	for rows.Next() {
		var incidentID, problemID int64
		var role string
		var ruleID sql.NullString
		var objectID sql.NullString
		var objectName, symptomClass, status string
		var priority sql.NullString
		var openedAt time.Time
		if err := rows.Scan(&incidentID, &problemID, &role, &ruleID, &objectID, &objectName, &symptomClass, &priority, &status, &openedAt); err != nil {
			writeError(response, http.StatusInternalServerError, "scan graph")
			return
		}
		nodeID := "p" + strconv.FormatInt(problemID, 10)
		nodes = append(nodes, graphNode{
			ID: nodeID, ProblemID: problemID, IncidentID: incidentID, ObjectID: nullableString(objectID),
			ObjectName: objectName, SymptomClass: symptomClass, Priority: nullableString(priority),
			Status: status, OpenedAt: formatISO(openedAt), Role: role,
		})
		if role == "root" {
			rootOfIncident[incidentID] = nodeID
		} else if root, ok := rootOfIncident[incidentID]; ok {
			edges = append(edges, graphEdge{From: root, To: nodeID, RuleID: nullableString(ruleID)})
		}
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load graph")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
}

func parseGraphWindow(raw string) time.Duration {
	switch raw {
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "90d":
		return 90 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}
