package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeOrgUnits — раздел «Сотрудники» доп. ТЗ: дерево организации как
// основное представление вместо плоской карточной сетки. Глубина дерева
// произвольная (см. 0020_org_units.sql — только parent_id, никакого
// ограничения на число уровней или обязательных «слоёв»).
func (server *Server) routeOrgUnits(response http.ResponseWriter, request *http.Request, path string) bool {
	if path == "/api/org-units/tree" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.EmployeesRead, server.orgUnitsTree)
		return true
	}
	if path == "/api/org-units" && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.EmployeesManage, server.createOrgUnit)
		return true
	}
	return false
}

type orgUnitRow struct {
	id        int64
	parentID  *int64
	name      string
	kind      string
	sortOrder int
}

func loadOrgUnits(ctx context.Context, pool *pgxpool.Pool) ([]orgUnitRow, error) {
	rows, err := pool.Query(ctx, `SELECT id, parent_id, name, kind, sort_order FROM org_units ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	units := make([]orgUnitRow, 0)
	for rows.Next() {
		var u orgUnitRow
		var parentID sql.NullInt64
		if err := rows.Scan(&u.id, &parentID, &u.name, &u.kind, &u.sortOrder); err != nil {
			return nil, err
		}
		u.parentID = nullableInt64(parentID)
		units = append(units, u)
	}
	return units, rows.Err()
}

// loadActiveAlertCounts — раздел «Сотрудники»: «активный алерт-счётчик»
// в строке сотрудника. Считается по notifications.recipient (стабильный
// trueconf_username, тот же ключ, что и loadRecentAlerts), а не по
// group-принадлежности — сотрудник мог получить уведомление вне группы.
func loadActiveAlertCounts(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT notification.recipient, count(DISTINCT problem.id)
		FROM notifications notification
		JOIN problems problem ON problem.id = notification.problem_id
		WHERE problem.status IN ('OPEN','FLAPPING')
		GROUP BY notification.recipient`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var recipient string
		var count int64
		if err := rows.Scan(&recipient, &count); err != nil {
			return nil, err
		}
		counts[recipient] = count
	}
	return counts, rows.Err()
}

// availabilityBucket сворачивает availability.Status.Kind до пяти веток
// агрегата на карточке подразделения (раздел «Сотрудники»: available/
// on-duty/vacation/sick + прочее недоступен) — та же таблица приоритетов,
// что и internal/availability.Resolve, здесь только группировка для UI.
func availabilityBucket(status availability.Status) string {
	switch status.Kind {
	case "", "available", "override_available":
		return "available"
	case "shift", "on_call":
		return "on_duty"
	case "vacation":
		return "vacation"
	case "sick_leave":
		return "sick_leave"
	default: // unavailable, override_unavailable, delegation
		return "unavailable"
	}
}

type employeeSummary struct {
	ID               int64   `json:"id"`
	FullName         string  `json:"full_name"`
	TrueconfUsername string  `json:"trueconf_username"`
	Position         *string `json:"position"`
	Active           bool    `json:"active"`
	Available        bool    `json:"available"`
	AvailabilityKind string  `json:"availability_kind"`
	ActiveAlerts     int64   `json:"active_alerts"`
}

type orgTreeNode struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Headcount    int               `json:"headcount"`
	Availability map[string]int    `json:"availability"`
	Employees    []employeeSummary `json:"employees"`
	Children     []*orgTreeNode    `json:"children"`
}

type orgUnitEmployeeRow struct {
	id        int64
	username  string
	fullName  sql.NullString
	position  sql.NullString
	active    bool
	orgUnitID int64
}

func loadOrgUnitEmployeeRows(ctx context.Context, pool *pgxpool.Pool) ([]orgUnitEmployeeRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, trueconf_username, full_name, position, active, org_unit_id
		FROM subscribers WHERE org_unit_id IS NOT NULL ORDER BY full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	employeeRows := make([]orgUnitEmployeeRow, 0)
	for rows.Next() {
		var er orgUnitEmployeeRow
		if err := rows.Scan(&er.id, &er.username, &er.fullName, &er.position, &er.active, &er.orgUnitID); err != nil {
			return nil, err
		}
		employeeRows = append(employeeRows, er)
	}
	return employeeRows, rows.Err()
}

func (server *Server) orgUnitsTree(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	units, err := loadOrgUnits(ctx, server.pool)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if len(units) == 0 {
		writeJSON(response, http.StatusOK, []orgTreeNode{})
		return
	}

	employeeRows, err := loadOrgUnitEmployeeRows(ctx, server.pool)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	subscriberIDs := make([]int64, 0, len(employeeRows))
	for _, er := range employeeRows {
		subscriberIDs = append(subscriberIDs, er.id)
	}

	statuses, err := availability.Resolve(ctx, server.pool, subscriberIDs, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	activeAlerts, err := loadActiveAlertCounts(ctx, server.pool)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	roots := buildOrgTree(units, employeeRows, statuses, activeAlerts)
	writeJSON(response, http.StatusOK, roots)
}

// buildOrgTree собирает parent_id-иерархию org_units в дерево, вешает на
// каждый узел его прямых сотрудников и сворачивает агрегаты (headcount +
// разбивка по доступности) снизу вверх по ВСЕМУ поддереву, не только
// прямым сотрудникам узла.
func buildOrgTree(
	units []orgUnitRow, employeeRows []orgUnitEmployeeRow,
	statuses map[int64]availability.Status, activeAlerts map[string]int64,
) []*orgTreeNode {
	nodes := make(map[int64]*orgTreeNode, len(units))
	for _, u := range units {
		nodes[u.id] = &orgTreeNode{
			ID: u.id, Name: u.name, Kind: u.kind,
			Availability: map[string]int{}, Employees: []employeeSummary{}, Children: []*orgTreeNode{},
		}
	}
	roots := make([]*orgTreeNode, 0)
	for _, u := range units {
		node := nodes[u.id]
		if u.parentID != nil {
			if parent, ok := nodes[*u.parentID]; ok {
				parent.Children = append(parent.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	for _, er := range employeeRows {
		node, ok := nodes[er.orgUnitID]
		if !ok {
			continue
		}
		status := statuses[er.id]
		node.Employees = append(node.Employees, employeeSummary{
			ID: er.id, TrueconfUsername: er.username, Active: er.active,
			FullName: nullableStringValue(er.fullName, er.username), Position: nullableString(er.position),
			Available: status.Available, AvailabilityKind: availabilityBucket(status),
			ActiveAlerts: activeAlerts[er.username],
		})
	}

	for _, root := range roots {
		rollupOrgTree(root)
	}
	return roots
}

func rollupOrgTree(node *orgTreeNode) (int, map[string]int) {
	total := len(node.Employees)
	agg := make(map[string]int)
	for _, e := range node.Employees {
		agg[e.AvailabilityKind]++
	}
	for _, child := range node.Children {
		childTotal, childAgg := rollupOrgTree(child)
		total += childTotal
		for kind, count := range childAgg {
			agg[kind] += count
		}
	}
	node.Headcount = total
	node.Availability = agg
	return total, agg
}

type orgUnitCreateRequest struct {
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
	Kind     string `json:"kind"`
}

func (server *Server) createOrgUnit(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload orgUnitCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid payload")
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Name == "" {
		writeError(response, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if strings.TrimSpace(payload.Kind) == "" {
		payload.Kind = "unit"
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO org_units(parent_id,name,kind,sort_order,created_at) VALUES($1,$2,$3,0,$4) RETURNING id`,
		payload.ParentID, payload.Name, payload.Kind, now,
	).Scan(&id)
	if err == nil {
		err = changelog.Record(ctx, tx, changelog.Event{
			OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "org_unit.create",
			ResourceType: "org_unit", ResourceID: strconv.FormatInt(id, 10),
			After: map[string]any{"name": payload.Name, "kind": payload.Kind, "parent_id": payload.ParentID},
		})
	}
	if err != nil || tx.Commit(ctx) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": id})
}
