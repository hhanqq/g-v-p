package adminapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/coverage"
)

// equipmentCoverage — GET /api/equipment/{id}/coverage?from=&to= (раздел
// 47-56 доп. ТЗ): «кто будет доступен для конкретного оборудования в
// конкретный момент времени». Ответственная группа — реальный
// group_equipment_scope (тот же equipmentResponsibleGroups, что уже
// показывает карточка оборудования), политика покрытия — самая
// специфичная из coverage_policies, что целится в этот объект (по
// object_id, иначе по equipment_type, иначе по site — тот же принцип
// «специфичное правило побеждает», что и у SLA-правил в этом проекте).
// Источник доступности — тот же internal/availability.Resolve, которым
// пользуется маршрутизация уведомлений: если здесь показан доступный
// человек, именно ему уйдёт уведомление при реальном алерте (раздел 56).
func (server *Server) equipmentCoverage(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	objectID, err := equipmentIDFromPath(request, "/coverage")
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	ctx := request.Context()

	var exists bool
	if err := server.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cmdb_objects WHERE id=$1)`, objectID).Scan(&exists); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !exists {
		writeError(response, http.StatusNotFound, "оборудование не найдено")
		return
	}

	query := request.URL.Query()
	from := time.Now().UTC()
	if v := query.Get("from"); v != "" {
		parsed, err := parseISO(v)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid from")
			return
		}
		from = parsed
	}
	to := from.AddDate(0, 0, 7)
	if v := query.Get("to"); v != "" {
		parsed, err := parseISO(v)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid to")
			return
		}
		to = parsed
	}
	if !to.After(from) || to.Sub(from) > 90*24*time.Hour {
		writeError(response, http.StatusUnprocessableEntity, "range must be positive and at most 90 days")
		return
	}

	groups, err := server.equipmentResponsibleGroups(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	groupIDs := make([]int64, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g["id"].(int64))
	}

	members, err := server.groupMembers(ctx, groupIDs)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	memberIDs := make([]int64, 0, len(members))
	for _, m := range members {
		memberIDs = append(memberIDs, m["id"].(int64))
	}

	granularity := "day"
	step := 24 * time.Hour
	if to.Sub(from) <= 3*24*time.Hour {
		granularity, step = "hour", time.Hour
	}

	buckets := make([]string, 0)
	byMember := make(map[int64][]string, len(memberIDs))
	for _, id := range memberIDs {
		byMember[id] = make([]string, 0)
	}
	availableCount := make([]int, 0)
	for at := from; at.Before(to); at = at.Add(step) {
		buckets = append(buckets, formatISO(at))
		statuses, err := availability.Resolve(ctx, server.pool, memberIDs, at)
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		count := 0
		for _, id := range memberIDs {
			status := statuses[id]
			label := "unavailable"
			if status.Available {
				label = "available"
				count++
			} else if status.Kind != "" {
				label = status.Kind
			}
			byMember[id] = append(byMember[id], label)
		}
		availableCount = append(availableCount, count)
	}

	policy, err := server.matchingCoveragePolicy(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var gaps []coverage.Gap
	var coveragePct *float64
	if policy != nil {
		gaps, err = coverage.Sweep(ctx, server.pool, policy.GroupID, from, to, policy.MinAvailable, nil)
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		totalSeconds := to.Sub(from).Seconds()
		var gapSeconds float64
		for _, g := range gaps {
			gapSeconds += g.To.Sub(g.From).Seconds()
		}
		if totalSeconds > 0 {
			value := round((totalSeconds-gapSeconds)*100/totalSeconds, 1)
			coveragePct = &value
		}
	}

	gapItems := make([]map[string]any, 0, len(gaps))
	for _, g := range gaps {
		gapItems = append(gapItems, map[string]any{"from": formatISO(g.From), "to": formatISO(g.To), "min_available": g.MinAvailable})
	}
	byMemberJSON := make(map[string][]string, len(byMember))
	for id, series := range byMember {
		byMemberJSON[formatInt64(id)] = series
	}

	var policyJSON any
	if policy != nil {
		policyJSON = map[string]any{"id": policy.ID, "name": policy.Name, "min_available": policy.MinAvailable, "group_id": policy.GroupID}
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"responsible_groups": groups,
		"members":            members,
		"policy":             policyJSON,
		"granularity":        granularity,
		"timeline":           map[string]any{"buckets": buckets, "by_member": byMemberJSON, "available_count": availableCount},
		"gaps":               gapItems,
		"coverage_pct":       coveragePct,
	})
}

func formatInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

// groupMembers — объединённый (deduplicated) состав нескольких групп
// сразу, с реальным ФИО/логином для отображения в таймлайне.
func (server *Server) groupMembers(ctx context.Context, groupIDs []int64) ([]map[string]any, error) {
	if len(groupIDs) == 0 {
		return []map[string]any{}, nil
	}
	rows, err := server.pool.Query(ctx, `
		SELECT DISTINCT s.id, s.trueconf_username, s.full_name
		FROM group_members m
		JOIN subscribers s ON s.id = m.subscriber_id
		WHERE m.group_id = ANY($1) AND s.active = TRUE
		ORDER BY s.id`, groupIDs)
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
		displayName := username
		if fullName.Valid && fullName.String != "" {
			displayName = fullName.String
		}
		items = append(items, map[string]any{"id": id, "username": username, "display_name": displayName})
	}
	return items, rows.Err()
}

type matchedCoveragePolicy struct {
	ID           int64
	Name         string
	GroupID      int64
	MinAvailable int
}

func (server *Server) matchingCoveragePolicy(ctx context.Context, objectID string) (*matchedCoveragePolicy, error) {
	var policy matchedCoveragePolicy
	err := server.pool.QueryRow(ctx, `
		SELECT policy.id, policy.name, policy.group_id, policy.min_available
		FROM coverage_policies policy
		JOIN cmdb_objects object ON object.id = $1
		WHERE policy.active = TRUE AND (
			policy.object_id = $1
			OR (policy.equipment_type IS NOT NULL AND policy.equipment_type = object.equipment_type)
			OR (policy.site IS NOT NULL AND policy.site = object.site)
		)
		ORDER BY (policy.object_id IS NOT NULL) DESC, (policy.equipment_type IS NOT NULL) DESC, policy.id
		LIMIT 1`, objectID,
	).Scan(&policy.ID, &policy.Name, &policy.GroupID, &policy.MinAvailable)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}
