package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type stateAction int

const (
	actionNoop stateAction = iota
	actionCreate
	actionRepeat
	actionReopen
	actionResolve
)

func applyEvent(ctx context.Context, tx pgx.Tx, dedupKey *string, event Event, resolution Resolution) (*problem, error) {
	if dedupKey == nil {
		return nil, nil
	}
	latest, err := loadLatestProblem(ctx, tx, *dedupKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	action := chooseStateAction(latest, event.State, event.OccurredAt, defaultFlapWindow)
	switch action {
	case actionRepeat:
		latest.RepeatCount++
		latest.LastSeenAt = event.OccurredAt
		_, err = tx.Exec(ctx, `UPDATE problems SET repeat_count=$1,last_seen_at=$2 WHERE id=$3`, latest.RepeatCount, latest.LastSeenAt, latest.ID)
		return latest, err
	case actionReopen:
		latest.ToggleCount++
		latest.RepeatCount++
		latest.Status = "OPEN"
		if latest.ToggleCount >= defaultFlapThreshold {
			latest.Status = "FLAPPING"
		}
		latest.ResolvedAt = nil
		latest.LastSeenAt = event.OccurredAt
		_, err = tx.Exec(ctx, `UPDATE problems SET toggle_count=$1,repeat_count=$2,status=$3,resolved_at=NULL,last_seen_at=$4 WHERE id=$5`, latest.ToggleCount, latest.RepeatCount, latest.Status, latest.LastSeenAt, latest.ID)
		if err != nil {
			return latest, err
		}
		if latest.IncidentID != nil {
			_, err = tx.Exec(ctx, `UPDATE incidents SET closed_at=NULL WHERE id=$1`, *latest.IncidentID)
		}
		return latest, err
	case actionCreate:
		created := &problem{
			DedupKey: *dedupKey, Status: "OPEN", ObjectID: resolution.ObjectID,
			Component: event.Component, SymptomClass: event.SymptomClass, Site: event.Site,
			OpenedAt: event.OccurredAt, LastSeenAt: event.OccurredAt, RepeatCount: 1,
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO problems(dedup_key,status,object_id,component,symptom_class,site,opened_at,last_seen_at,repeat_count,toggle_count,closed_by_reconciliation)
			VALUES($1,'OPEN',$2,$3,$4,$5,$6,$6,1,0,FALSE) RETURNING id`,
			*dedupKey, resolution.ObjectID, event.Component, event.SymptomClass, event.Site, event.OccurredAt,
		).Scan(&created.ID)
		return created, err
	case actionResolve:
		latest.Status = "RESOLVED"
		latest.ResolvedAt = &event.OccurredAt
		latest.LastSeenAt = event.OccurredAt
		_, err = tx.Exec(ctx, `UPDATE problems SET status='RESOLVED',resolved_at=$1,last_seen_at=$1 WHERE id=$2`, event.OccurredAt, latest.ID)
		if err != nil {
			return latest, err
		}
		if latest.IncidentID != nil {
			err = closeIncidentIfAllResolved(ctx, tx, *latest.IncidentID, event.OccurredAt)
		}
		return latest, err
	default:
		return nil, nil
	}
}

// closeIncidentIfAllResolved закрывает инцидент только когда ВСЕ его члены
// (root + symptoms из incident_problems) реально RESOLVED — не только тот
// Problem, который только что резолвнулся. Симметрично reopen-логике выше:
// incidents.closed_at раньше вообще никогда не писался (см. аудит 2026-08-11),
// UI трактовал «открыт/закрыт» только по статусу root problem.
func closeIncidentIfAllResolved(ctx context.Context, tx pgx.Tx, incidentID int64, at time.Time) error {
	var openCount int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM incident_problems member
		JOIN problems problem ON problem.id = member.problem_id
		WHERE member.incident_id=$1 AND problem.status <> 'RESOLVED'`, incidentID).Scan(&openCount)
	if err != nil {
		return err
	}
	if openCount > 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE incidents SET closed_at=$1 WHERE id=$2 AND closed_at IS NULL`, at, incidentID)
	return err
}

func chooseStateAction(latest *problem, state string, occurredAt time.Time, flapWindow time.Duration) stateAction {
	active := latest != nil && isActive(latest.Status)
	reopen := latest != nil && !active && latest.ResolvedAt != nil && occurredAt.Sub(*latest.ResolvedAt) <= flapWindow
	if state == "firing" {
		if active {
			return actionRepeat
		}
		if reopen {
			return actionReopen
		}
		return actionCreate
	}
	if active {
		return actionResolve
	}
	return actionNoop
}

func loadLatestProblem(ctx context.Context, tx pgx.Tx, dedupKey string) (*problem, error) {
	var item problem
	var objectID, component, site, priority sql.NullString
	var resolvedAt sql.NullTime
	var incidentID, duplicateID sql.NullInt64
	err := tx.QueryRow(ctx, `
		SELECT id,dedup_key,status,object_id,component,symptom_class,site,opened_at,resolved_at,last_seen_at,
		       repeat_count,toggle_count,priority,incident_id,duplicate_of_problem_id
		FROM problems WHERE dedup_key=$1 ORDER BY id DESC LIMIT 1 FOR UPDATE`, dedupKey,
	).Scan(&item.ID, &item.DedupKey, &item.Status, &objectID, &component, &item.SymptomClass, &site,
		&item.OpenedAt, &resolvedAt, &item.LastSeenAt, &item.RepeatCount, &item.ToggleCount,
		&priority, &incidentID, &duplicateID)
	if err != nil {
		return nil, err
	}
	item.ObjectID = nullableString(objectID)
	item.Component = nullableString(component)
	item.Site = nullableString(site)
	item.Priority = nullableString(priority)
	item.ResolvedAt = nullableTime(resolvedAt)
	item.IncidentID = nullableInt64(incidentID)
	item.DuplicateOfProblemID = nullableInt64(duplicateID)
	return &item, nil
}

func isActive(status string) bool {
	for _, candidate := range activeStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
