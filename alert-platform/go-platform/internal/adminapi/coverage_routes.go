package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/coverage"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeCoverage обрабатывает /api/coverage/policies (CRUD) и
// /api/coverage/.../gaps (вычисление по требованию через
// internal/coverage.Sweep — не materialized view, см. пакет).
func (server *Server) routeCoverage(response http.ResponseWriter, request *http.Request, path string) bool {
	if path == "/api/coverage/gaps" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.CoverageRead, server.allCoverageGaps)
		return true
	}
	if path != "/api/coverage/policies" && !strings.HasPrefix(path, "/api/coverage/policies/") {
		return false
	}
	if path == "/api/coverage/policies" {
		switch request.Method {
		case http.MethodGet:
			server.withPermission(response, request, rbac.CoverageRead, server.listCoveragePolicies)
		case http.MethodPost:
			server.withPermission(response, request, rbac.CoverageManage, server.createCoveragePolicy)
		default:
			return false
		}
		return true
	}
	rest := strings.TrimPrefix(path, "/api/coverage/policies/")
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	policyID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid policy id")
		return true
	}
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodPut:
			server.withPermission(response, request, rbac.CoverageManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
				server.updateCoveragePolicy(w, r, policyID, u)
			})
		case http.MethodDelete:
			server.withPermission(response, request, rbac.CoverageManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
				server.deleteCoveragePolicy(w, r, policyID, u)
			})
		default:
			return false
		}
		return true
	}
	if len(segments) == 2 && segments[1] == "gaps" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.CoverageRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.coveragePolicyGaps(w, r, policyID)
		})
		return true
	}
	return false
}

type coveragePolicyRequest struct {
	Name          *string `json:"name"`
	GroupID       *int64  `json:"group_id"`
	MinAvailable  *int    `json:"min_available"`
	ObjectID      *string `json:"object_id"`
	EquipmentType *string `json:"equipment_type"`
	Site          *string `json:"site"`
	Active        *bool   `json:"active"`
}

func (server *Server) listCoveragePolicies(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT policy.id, policy.name, policy.group_id, grp.name, policy.min_available,
		       policy.object_id, policy.equipment_type, policy.site, policy.active
		FROM coverage_policies policy JOIN groups grp ON grp.id = policy.group_id
		ORDER BY policy.id`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, groupID int64
		var name, groupName string
		var minAvailable int
		var objectID, equipmentType, site sql.NullString
		var active bool
		if err := rows.Scan(&id, &name, &groupID, &groupName, &minAvailable, &objectID, &equipmentType, &site, &active); err != nil {
			writeError(response, http.StatusInternalServerError, "scan coverage policies")
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "group_id": groupID, "group_name": groupName, "min_available": minAvailable,
			"object_id": nullableString(objectID), "equipment_type": nullableString(equipmentType),
			"site": nullableString(site), "active": active,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load coverage policies")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (server *Server) createCoveragePolicy(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload coveragePolicyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Name == nil || strings.TrimSpace(*payload.Name) == "" ||
		payload.GroupID == nil || payload.MinAvailable == nil || *payload.MinAvailable < 1 {
		writeError(response, http.StatusUnprocessableEntity, "name, group_id and min_available (>=1) are required")
		return
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
	var groupExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1)`, *payload.GroupID).Scan(&groupExists); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !groupExists {
		writeError(response, http.StatusUnprocessableEntity, "unknown group_id")
		return
	}
	name := strings.TrimSpace(*payload.Name)
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO coverage_policies(name,group_id,min_available,object_id,equipment_type,site,active,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,TRUE,$7,$8,$8) RETURNING id`,
		name, *payload.GroupID, *payload.MinAvailable, payload.ObjectID, payload.EquipmentType, payload.Site, actor, now,
	).Scan(&id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "coverage_policy.create",
		ResourceType: "coverage_policy", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{"name": name, "group_id": *payload.GroupID, "min_available": *payload.MinAvailable},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": id})
}

func (server *Server) updateCoveragePolicy(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	var payload coveragePolicyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid coverage policy payload")
		return
	}
	if payload.MinAvailable != nil && *payload.MinAvailable < 1 {
		writeError(response, http.StatusUnprocessableEntity, "min_available must be >= 1")
		return
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
	var name string
	err = tx.QueryRow(ctx, `
		UPDATE coverage_policies SET
			name=COALESCE($2,name), group_id=COALESCE($3,group_id), min_available=COALESCE($4,min_available),
			object_id=COALESCE($5,object_id), equipment_type=COALESCE($6,equipment_type), site=COALESCE($7,site),
			active=COALESCE($8,active), updated_at=$9
		WHERE id=$1 RETURNING name`,
		id, payload.Name, payload.GroupID, payload.MinAvailable, payload.ObjectID, payload.EquipmentType, payload.Site, payload.Active, now,
	).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Политика покрытия не найдена")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "coverage_policy.update",
		ResourceType: "coverage_policy", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{"name": name},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (server *Server) deleteCoveragePolicy(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	ctx := request.Context()
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name string
	err = tx.QueryRow(ctx, `DELETE FROM coverage_policies WHERE id=$1 RETURNING name`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Политика покрытия не найдена")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "coverage_policy.delete",
		ResourceType: "coverage_policy", ResourceID: strconv.FormatInt(id, 10),
		Before: map[string]any{"name": name},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func parseGapRange(query map[string][]string) (time.Time, time.Time, bool) {
	fromRaw, toRaw := first(query["from"]), first(query["to"])
	from, err := parseISO(fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	to, err := parseISO(toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if !to.After(from) || to.Sub(from) > 100*24*time.Hour {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (server *Server) coveragePolicyGaps(response http.ResponseWriter, request *http.Request, id int64) {
	ctx := request.Context()
	from, to, ok := parseGapRange(request.URL.Query())
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid from/to")
		return
	}
	var groupID int64
	var minAvailable int
	if err := server.pool.QueryRow(ctx, `SELECT group_id, min_available FROM coverage_policies WHERE id=$1 AND active=TRUE`, id).Scan(&groupID, &minAvailable); errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Активная политика покрытия не найдена")
		return
	} else if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	gaps, err := coverage.Sweep(ctx, server.pool, groupID, from, to, minAvailable, nil)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, gapsJSON(gaps))
}

func (server *Server) allCoverageGaps(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	from, to, ok := parseGapRange(request.URL.Query())
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid from/to")
		return
	}
	rows, err := server.pool.Query(ctx, `SELECT id, name, group_id, min_available FROM coverage_policies WHERE active=TRUE ORDER BY id`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	type policyRow struct {
		id, groupID  int64
		name         string
		minAvailable int
	}
	var policies []policyRow
	for rows.Next() {
		var p policyRow
		if err := rows.Scan(&p.id, &p.name, &p.groupID, &p.minAvailable); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan coverage policies")
			return
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load coverage policies")
		return
	}
	rows.Close()

	items := make([]map[string]any, 0)
	for _, p := range policies {
		gaps, err := coverage.Sweep(ctx, server.pool, p.groupID, from, to, p.minAvailable, nil)
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		items = append(items, map[string]any{"policy_id": p.id, "policy_name": p.name, "gaps": gapsJSON(gaps)})
	}
	writeJSON(response, http.StatusOK, items)
}

func gapsJSON(gaps []coverage.Gap) []map[string]any {
	items := make([]map[string]any, 0, len(gaps))
	for _, g := range gaps {
		items = append(items, map[string]any{"from": formatISO(g.From), "to": formatISO(g.To), "min_available": g.MinAvailable})
	}
	return items
}
