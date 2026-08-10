package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

// routeGroups обрабатывает /api/groups и вложенные под-ресурсы
// (участники, зона ответственности по оборудованию). Разбор пути здесь,
// а не в общей цепочке server.go, потому что вложенность (group/{id}/
// members/{subscriber_id}) не укладывается в plain pathInt-хелпер.
func (server *Server) routeGroups(response http.ResponseWriter, request *http.Request, path string) bool {
	if path != "/api/groups" && !strings.HasPrefix(path, "/api/groups/") {
		return false
	}
	if path == "/api/groups" {
		switch request.Method {
		case http.MethodGet:
			server.withAuth(response, request, server.listGroups)
		case http.MethodPost:
			server.withAuth(response, request, server.createGroup)
		default:
			return false
		}
		return true
	}
	rest := strings.TrimPrefix(path, "/api/groups/")
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	groupID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid group id")
		return true
	}
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodGet:
			server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, _ map[string]any) { server.getGroup(w, r, groupID) })
		case http.MethodPut:
			server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, u map[string]any) { server.updateGroup(w, r, groupID, u) })
		case http.MethodDelete:
			server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, u map[string]any) { server.deleteGroup(w, r, groupID, u) })
		default:
			return false
		}
		return true
	}
	if len(segments) == 2 && segments[1] == "members" && request.Method == http.MethodPost {
		server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.addGroupMember(w, r, groupID, u)
		})
		return true
	}
	if len(segments) == 3 && segments[1] == "members" && request.Method == http.MethodDelete {
		subscriberID, err := strconv.ParseInt(segments[2], 10, 64)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid subscriber id")
			return true
		}
		server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.removeGroupMember(w, r, groupID, subscriberID, u)
		})
		return true
	}
	if len(segments) == 2 && segments[1] == "equipment" && request.Method == http.MethodPost {
		server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.addGroupEquipmentScope(w, r, groupID, u)
		})
		return true
	}
	if len(segments) == 3 && segments[1] == "equipment" && request.Method == http.MethodDelete {
		scopeID, err := strconv.ParseInt(segments[2], 10, 64)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid scope id")
			return true
		}
		server.withAuth(response, request, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.removeGroupEquipmentScope(w, r, groupID, scopeID, u)
		})
		return true
	}
	return false
}

func (server *Server) listGroups(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT g.id, g.name, g.description, g.created_at,
		       (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id),
		       (SELECT COUNT(*) FROM group_equipment_scope s WHERE s.group_id = g.id)
		FROM groups g ORDER BY g.id`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name string
		var description sql.NullString
		var createdAt time.Time
		var memberCount, scopeCount int64
		if err := rows.Scan(&id, &name, &description, &createdAt, &memberCount, &scopeCount); err != nil {
			writeError(response, http.StatusInternalServerError, "scan groups")
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "description": nullableString(description), "created_at": formatISO(createdAt),
			"member_count": memberCount, "equipment_scope_count": scopeCount,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load groups")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

type groupRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func decodeGroup(response http.ResponseWriter, request *http.Request) (groupRequest, bool) {
	var payload groupRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid group payload")
		return payload, false
	}
	return payload, true
}

func (server *Server) createGroup(response http.ResponseWriter, request *http.Request, user map[string]any) {
	payload, ok := decodeGroup(response, request)
	if !ok {
		return
	}
	if payload.Name == nil || strings.TrimSpace(*payload.Name) == "" {
		writeError(response, http.StatusUnprocessableEntity, "name is required")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	var id int64
	name := strings.TrimSpace(*payload.Name)
	err = tx.QueryRow(request.Context(), `INSERT INTO groups(name,description,created_at) VALUES($1,$2,$3) RETURNING id`, name, payload.Description, now).Scan(&id)
	if err == nil {
		_, err = tx.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'create_group',$2,$3)`, actor, name, now)
	}
	if err == nil {
		err = changelog.Record(request.Context(), tx, changelog.Event{
			OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.create",
			ResourceType: "group", ResourceID: strconv.FormatInt(id, 10),
			After: map[string]any{"name": name, "description": payload.Description},
		})
	}
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": id})
}

func (server *Server) getGroup(response http.ResponseWriter, request *http.Request, groupID int64) {
	var name string
	var description sql.NullString
	var createdAt time.Time
	err := server.pool.QueryRow(request.Context(), `SELECT name,description,created_at FROM groups WHERE id=$1`, groupID).Scan(&name, &description, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Группа не найдена")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	members, err := server.loadGroupMembers(request.Context(), groupID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	scope, err := server.loadGroupEquipmentScope(request.Context(), groupID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"id": groupID, "name": name, "description": nullableString(description), "created_at": formatISO(createdAt),
		"members": members, "equipment_scope": scope,
	})
}

func (server *Server) loadGroupMembers(ctx context.Context, groupID int64) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT subscriber.id, subscriber.trueconf_username, subscriber.full_name
		FROM group_members member
		JOIN subscribers subscriber ON subscriber.id = member.subscriber_id
		WHERE member.group_id = $1 ORDER BY subscriber.trueconf_username`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var username string
		var fullName sql.NullString
		if err := rows.Scan(&id, &username, &fullName); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"subscriber_id": id, "trueconf_username": username, "full_name": nullableString(fullName)})
	}
	return items, rows.Err()
}

func (server *Server) loadGroupEquipmentScope(ctx context.Context, groupID int64) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT scope.id, scope.object_id, object.name, scope.equipment_type, scope.site
		FROM group_equipment_scope scope
		LEFT JOIN cmdb_objects object ON object.id = scope.object_id
		WHERE scope.group_id = $1 ORDER BY scope.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var objectID, objectName, equipmentType, site sql.NullString
		if err := rows.Scan(&id, &objectID, &objectName, &equipmentType, &site); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "object_id": nullableString(objectID), "object_name": nullableString(objectName),
			"equipment_type": nullableString(equipmentType), "site": nullableString(site),
		})
	}
	return items, rows.Err()
}

func (server *Server) updateGroup(response http.ResponseWriter, request *http.Request, groupID int64, user map[string]any) {
	payload, ok := decodeGroup(response, request)
	if !ok {
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	var beforeName string
	var beforeDescription sql.NullString
	_ = server.pool.QueryRow(request.Context(), `SELECT name,description FROM groups WHERE id=$1`, groupID).Scan(&beforeName, &beforeDescription)
	var name string
	var description sql.NullString
	err := server.pool.QueryRow(request.Context(), `
		UPDATE groups SET name=COALESCE(NULLIF($2,''),name),description=COALESCE($3,description) WHERE id=$1 RETURNING name,description`,
		groupID, valueOrEmpty(payload.Name), payload.Description).Scan(&name, &description)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Группа не найдена")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = server.pool.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'update_group',$2,$3)`, actor, name, now)
	_ = changelog.Record(request.Context(), server.pool, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.update",
		ResourceType: "group", ResourceID: strconv.FormatInt(groupID, 10),
		Before: map[string]any{"name": beforeName, "description": nullableString(beforeDescription)},
		After:  map[string]any{"name": name, "description": nullableString(description)},
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (server *Server) deleteGroup(response http.ResponseWriter, request *http.Request, groupID int64, user map[string]any) {
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	var name string
	err := server.pool.QueryRow(request.Context(), `DELETE FROM groups WHERE id=$1 RETURNING name`, groupID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Группа не найдена")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = server.pool.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'delete_group',$2,$3)`, actor, name, now)
	_ = changelog.Record(request.Context(), server.pool, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.delete",
		ResourceType: "group", ResourceID: strconv.FormatInt(groupID, 10),
		Before: map[string]any{"name": name},
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

type groupMemberRequest struct {
	SubscriberID *int64 `json:"subscriber_id"`
}

func (server *Server) addGroupMember(response http.ResponseWriter, request *http.Request, groupID int64, user map[string]any) {
	var payload groupMemberRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.SubscriberID == nil {
		writeError(response, http.StatusUnprocessableEntity, "subscriber_id is required")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	_, err := server.pool.Exec(request.Context(), `
		INSERT INTO group_members(group_id,subscriber_id) VALUES($1,$2)
		ON CONFLICT (group_id,subscriber_id) DO NOTHING`, groupID, *payload.SubscriberID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = server.pool.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,detail,created_at) VALUES($1,'add_group_member',$2,$3,$4)`,
		actor, strconv.FormatInt(groupID, 10), strconv.FormatInt(*payload.SubscriberID, 10), now)
	_ = changelog.Record(request.Context(), server.pool, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.add_member",
		ResourceType: "group", ResourceID: strconv.FormatInt(groupID, 10),
		After: map[string]any{"subscriber_id": *payload.SubscriberID},
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (server *Server) removeGroupMember(response http.ResponseWriter, request *http.Request, groupID, subscriberID int64, user map[string]any) {
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	_, err := server.pool.Exec(request.Context(), `DELETE FROM group_members WHERE group_id=$1 AND subscriber_id=$2`, groupID, subscriberID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = server.pool.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,detail,created_at) VALUES($1,'remove_group_member',$2,$3,$4)`,
		actor, strconv.FormatInt(groupID, 10), strconv.FormatInt(subscriberID, 10), now)
	_ = changelog.Record(request.Context(), server.pool, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.remove_member",
		ResourceType: "group", ResourceID: strconv.FormatInt(groupID, 10),
		Before: map[string]any{"subscriber_id": subscriberID},
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

type groupEquipmentScopeRequest struct {
	ObjectID      *string `json:"object_id"`
	EquipmentType *string `json:"equipment_type"`
	Site          *string `json:"site"`
}

func (server *Server) addGroupEquipmentScope(response http.ResponseWriter, request *http.Request, groupID int64, user map[string]any) {
	var payload groupEquipmentScopeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid scope payload")
		return
	}
	if emptyOrNil(payload.ObjectID) && emptyOrNil(payload.EquipmentType) && emptyOrNil(payload.Site) {
		writeError(response, http.StatusUnprocessableEntity, "укажите оборудование, тип оборудования или площадку")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	var scopeID int64
	err := server.pool.QueryRow(request.Context(), `
		INSERT INTO group_equipment_scope(group_id,object_id,equipment_type,site) VALUES($1,$2,$3,$4) RETURNING id`,
		groupID, nilIfEmpty(payload.ObjectID), nilIfEmpty(payload.EquipmentType), nilIfEmpty(payload.Site)).Scan(&scopeID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = server.pool.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'add_group_equipment_scope',$2,$3)`,
		actor, strconv.FormatInt(groupID, 10), now)
	_ = changelog.Record(request.Context(), server.pool, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.add_equipment_scope",
		ResourceType: "group", ResourceID: strconv.FormatInt(groupID, 10),
		After: map[string]any{"scope_id": scopeID, "object_id": payload.ObjectID, "equipment_type": payload.EquipmentType, "site": payload.Site},
	})
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": scopeID})
}

func (server *Server) removeGroupEquipmentScope(response http.ResponseWriter, request *http.Request, groupID, scopeID int64, user map[string]any) {
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	_, err := server.pool.Exec(request.Context(), `DELETE FROM group_equipment_scope WHERE id=$1 AND group_id=$2`, scopeID, groupID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = server.pool.Exec(request.Context(), `INSERT INTO audit_log(actor,action,target,created_at) VALUES($1,'remove_group_equipment_scope',$2,$3)`,
		actor, strconv.FormatInt(groupID, 10), now)
	_ = changelog.Record(request.Context(), server.pool, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "group.remove_equipment_scope",
		ResourceType: "group", ResourceID: strconv.FormatInt(groupID, 10),
		Before: map[string]any{"scope_id": scopeID},
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func emptyOrNil(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

func nilIfEmpty(value *string) *string {
	if emptyOrNil(value) {
		return nil
	}
	return value
}
