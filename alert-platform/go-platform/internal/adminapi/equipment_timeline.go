package adminapi

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"
)

// timelineEntry — один пункт полной ленты истории объекта (раздел V ТЗ):
// не только "алерты", а весь operational lifecycle — открытие/подтверждение/
// резолв проблемы, создание/закрытие инцидента, отправка уведомления,
// нарушение SLA, изменение паспорта оборудования. Источник — исключительно
// Postgres (operational state + change_events), без похода в ClickHouse:
// change_events синхронно пишется в той же транзакции, что и мутация
// (см. ARCHITECTURE.md §7), поэтому читать его напрямую для одной карточки
// не нарушает изоляцию «строго побочного контура» Redpanda/ClickHouse.
type timelineEntry struct {
	At     time.Time `json:"-"`
	AtISO  string    `json:"at"`
	Kind   string    `json:"kind"`
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
}

func (server *Server) equipmentTimeline(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	objectID, err := equipmentIDFromPath(request, "/timeline")
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	ctx := request.Context()
	entries := make([]timelineEntry, 0, 128)

	problemEvents, err := server.problemTimelineEntries(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	entries = append(entries, problemEvents...)

	incidentEvents, err := server.incidentTimelineEntries(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	entries = append(entries, incidentEvents...)

	notificationEvents, err := server.notificationTimelineEntries(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	entries = append(entries, notificationEvents...)

	slaEvents, err := server.slaTimelineEntries(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	entries = append(entries, slaEvents...)

	changeEvents, err := server.changeTimelineEntries(ctx, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	entries = append(entries, changeEvents...)

	sort.Slice(entries, func(i, j int) bool { return entries[i].At.After(entries[j].At) })
	if len(entries) > 300 {
		entries = entries[:300]
	}
	writeJSON(response, http.StatusOK, entries)
}

func (server *Server) problemTimelineEntries(ctx context.Context, objectID string) ([]timelineEntry, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT id, symptom_class, priority, opened_at, acknowledged_at, resolved_at
		FROM problems WHERE object_id=$1 ORDER BY opened_at DESC LIMIT 100`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]timelineEntry, 0)
	for rows.Next() {
		var id int64
		var symptomClass string
		var priority sql.NullString
		var openedAt time.Time
		var acknowledgedAt, resolvedAt sql.NullTime
		if err := rows.Scan(&id, &symptomClass, &priority, &openedAt, &acknowledgedAt, &resolvedAt); err != nil {
			return nil, err
		}
		prio := nullableStringValue(priority, "—")
		entries = append(entries, timelineEntry{
			At: openedAt, AtISO: formatISO(openedAt), Kind: "problem_opened",
			Title: "Открыта проблема", Detail: prio + " · " + symptomClass,
		})
		if acknowledgedAt.Valid {
			entries = append(entries, timelineEntry{
				At: acknowledgedAt.Time, AtISO: formatISO(acknowledgedAt.Time), Kind: "problem_acknowledged",
				Title: "Проблема подтверждена", Detail: symptomClass,
			})
		}
		if resolvedAt.Valid {
			entries = append(entries, timelineEntry{
				At: resolvedAt.Time, AtISO: formatISO(resolvedAt.Time), Kind: "problem_resolved",
				Title: "Проблема устранена", Detail: symptomClass,
			})
		}
	}
	return entries, rows.Err()
}

func (server *Server) incidentTimelineEntries(ctx context.Context, objectID string) ([]timelineEntry, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT DISTINCT incident.id, incident.opened_at, incident.closed_at, root.symptom_class
		FROM incidents incident
		JOIN problems root ON root.id = incident.root_problem_id
		WHERE incident.id IN (
			SELECT ip.incident_id FROM incident_problems ip
			JOIN problems p ON p.id = ip.problem_id WHERE p.object_id = $1
		) ORDER BY incident.opened_at DESC LIMIT 50`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]timelineEntry, 0)
	for rows.Next() {
		var id int64
		var openedAt time.Time
		var closedAt sql.NullTime
		var symptomClass string
		if err := rows.Scan(&id, &openedAt, &closedAt, &symptomClass); err != nil {
			return nil, err
		}
		entries = append(entries, timelineEntry{
			At: openedAt, AtISO: formatISO(openedAt), Kind: "incident_created",
			Title: "Создан инцидент", Detail: symptomClass,
		})
		if closedAt.Valid {
			entries = append(entries, timelineEntry{
				At: closedAt.Time, AtISO: formatISO(closedAt.Time), Kind: "incident_closed",
				Title: "Инцидент закрыт", Detail: symptomClass,
			})
		}
	}
	return entries, rows.Err()
}

func (server *Server) notificationTimelineEntries(ctx context.Context, objectID string) ([]timelineEntry, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT n.type, n.status, n.recipient, n.created_at
		FROM notifications n JOIN problems p ON p.id = n.problem_id
		WHERE p.object_id=$1 ORDER BY n.created_at DESC LIMIT 100`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]timelineEntry, 0)
	for rows.Next() {
		var notifyType, status string
		var recipient sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&notifyType, &status, &recipient, &createdAt); err != nil {
			return nil, err
		}
		entries = append(entries, timelineEntry{
			At: createdAt, AtISO: formatISO(createdAt), Kind: "notification_" + status,
			Title: "Уведомление (" + notifyType + ")", Detail: nullableStringValue(recipient, "—") + " · " + status,
		})
	}
	return entries, rows.Err()
}

func (server *Server) slaTimelineEntries(ctx context.Context, objectID string) ([]timelineEntry, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT breach.created_at, rule.name
		FROM sla_breach_notices breach
		JOIN problems p ON p.id = breach.problem_id
		JOIN sla_rules rule ON rule.id = breach.sla_rule_id
		WHERE p.object_id=$1 ORDER BY breach.created_at DESC LIMIT 50`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]timelineEntry, 0)
	for rows.Next() {
		var createdAt time.Time
		var ruleName string
		if err := rows.Scan(&createdAt, &ruleName); err != nil {
			return nil, err
		}
		entries = append(entries, timelineEntry{
			At: createdAt, AtISO: formatISO(createdAt), Kind: "sla_breach",
			Title: "Нарушение SLA", Detail: ruleName,
		})
	}
	return entries, rows.Err()
}

func (server *Server) changeTimelineEntries(ctx context.Context, objectID string) ([]timelineEntry, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT occurred_at, actor, action, detail
		FROM change_events WHERE resource_type='cmdb_object' AND resource_id=$1
		ORDER BY occurred_at DESC LIMIT 50`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]timelineEntry, 0)
	for rows.Next() {
		var occurredAt time.Time
		var actor, action string
		var detail sql.NullString
		if err := rows.Scan(&occurredAt, &actor, &action, &detail); err != nil {
			return nil, err
		}
		entries = append(entries, timelineEntry{
			At: occurredAt, AtISO: formatISO(occurredAt), Kind: "change_" + action,
			Title: "Изменение оборудования", Detail: actor + " · " + nullableStringValue(detail, action),
		})
	}
	return entries, rows.Err()
}
