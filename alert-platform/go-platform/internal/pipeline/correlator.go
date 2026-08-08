package pipeline

import (
	"context"
	"database/sql"
	"strings"

	"github.com/jackc/pgx/v5"
)

type correlationRule struct {
	RuleID        string
	CauseSymptom  string
	MatchAxes     string
	WindowSeconds int
}

type cmdbObject struct {
	ID             string
	Subnet         *string
	ParentSwitchID *string
}

func tryCorrelate(ctx context.Context, tx pgx.Tx, trigger *problem) error {
	if trigger.IncidentID != nil {
		return nil
	}
	var rootID int64
	err := tx.QueryRow(ctx, `SELECT id FROM incidents WHERE root_problem_id=$1 LIMIT 1`, trigger.ID).Scan(&rootID)
	if err == nil {
		return nil
	}
	if err != pgx.ErrNoRows {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT rule_id,cause_symptom,match_axes,window_s
		FROM correlation_rules WHERE status='active' AND trigger_symptom=$1`, trigger.SymptomClass)
	if err != nil {
		return err
	}
	var rules []correlationRule
	for rows.Next() {
		var rule correlationRule
		if err := rows.Scan(&rule.RuleID, &rule.CauseSymptom, &rule.MatchAxes, &rule.WindowSeconds); err != nil {
			rows.Close()
			return err
		}
		rules = append(rules, rule)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rule := range rules {
		cause, err := findCause(ctx, tx, rule, trigger)
		if err != nil {
			return err
		}
		if cause == nil {
			continue
		}
		var incidentID int64
		if cause.IncidentID != nil {
			incidentID = *cause.IncidentID
		} else {
			err = tx.QueryRow(ctx, `
				INSERT INTO incidents(root_problem_id,priority,opened_at)
				VALUES($1,$2,$3) RETURNING id`, cause.ID, cause.Priority, cause.OpenedAt).Scan(&incidentID)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE problems SET incident_id=$1 WHERE id=$2`, incidentID, cause.ID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO incident_problems(incident_id,problem_id,role) VALUES($1,$2,'root')`, incidentID, cause.ID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO incident_problems(incident_id,problem_id,role,rule_id)
			VALUES($1,$2,'symptom',$3)`, incidentID, trigger.ID, rule.RuleID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE problems SET incident_id=$1 WHERE id=$2`, incidentID, trigger.ID); err != nil {
			return err
		}
		trigger.IncidentID = &incidentID
		return nil
	}
	return nil
}

func findCause(ctx context.Context, tx pgx.Tx, rule correlationRule, trigger *problem) (*problem, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,dedup_key,status,object_id,component,symptom_class,site,opened_at,resolved_at,last_seen_at,
		       repeat_count,toggle_count,priority,incident_id,duplicate_of_problem_id
		FROM problems
		WHERE symptom_class=$1 AND opened_at <= $2 AND id <> $3
		  AND (status IN ('OPEN','ACKNOWLEDGED','FLAPPING')
		       OR (status='RESOLVED' AND resolved_at >= $2::timestamp - make_interval(secs => $4)))
		ORDER BY opened_at DESC`, rule.CauseSymptom, trigger.OpenedAt, trigger.ID, rule.WindowSeconds)
	if err != nil {
		return nil, err
	}
	var candidates []*problem
	for rows.Next() {
		candidate, err := scanProblem(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		matches := true
		for _, axis := range strings.Split(rule.MatchAxes, ",") {
			axis = strings.TrimSpace(axis)
			if axis == "" {
				continue
			}
			matched, err := axisMatches(ctx, tx, axis, trigger, candidate)
			if err != nil {
				return nil, err
			}
			if !matched {
				matches = false
				break
			}
		}
		if matches {
			return candidate, nil
		}
	}
	return nil, nil
}

func axisMatches(ctx context.Context, tx pgx.Tx, axis string, trigger, cause *problem) (bool, error) {
	switch axis {
	case "site":
		return trigger.Site != nil && cause.Site != nil && *trigger.Site == *cause.Site, nil
	case "subnet":
		triggerObject, err := loadCMDBObject(ctx, tx, trigger.ObjectID)
		if err != nil {
			return false, err
		}
		causeObject, err := loadCMDBObject(ctx, tx, cause.ObjectID)
		if err != nil {
			return false, err
		}
		return triggerObject != nil && causeObject != nil && triggerObject.Subnet != nil && causeObject.Subnet != nil && *triggerObject.Subnet == *causeObject.Subnet, nil
	case "topology":
		triggerObject, err := loadCMDBObject(ctx, tx, trigger.ObjectID)
		if err != nil || triggerObject == nil || cause.ObjectID == nil {
			return false, err
		}
		if triggerObject.ParentSwitchID != nil && *triggerObject.ParentSwitchID == *cause.ObjectID {
			return true, nil
		}
		parent, err := loadCMDBObject(ctx, tx, triggerObject.ParentSwitchID)
		return parent != nil && parent.ParentSwitchID != nil && *parent.ParentSwitchID == *cause.ObjectID, err
	default:
		return false, nil
	}
}

func loadCMDBObject(ctx context.Context, tx pgx.Tx, objectID *string) (*cmdbObject, error) {
	if objectID == nil {
		return nil, nil
	}
	var object cmdbObject
	var subnet, parent sql.NullString
	err := tx.QueryRow(ctx, `SELECT id,subnet,parent_switch_id FROM cmdb_objects WHERE id=$1`, *objectID).Scan(&object.ID, &subnet, &parent)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	object.Subnet, object.ParentSwitchID = nullableString(subnet), nullableString(parent)
	return &object, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanProblem(row rowScanner) (*problem, error) {
	var item problem
	var objectID, component, site, priority sql.NullString
	var resolvedAt sql.NullTime
	var incidentID, duplicateID sql.NullInt64
	err := row.Scan(&item.ID, &item.DedupKey, &item.Status, &objectID, &component, &item.SymptomClass, &site,
		&item.OpenedAt, &resolvedAt, &item.LastSeenAt, &item.RepeatCount, &item.ToggleCount,
		&priority, &incidentID, &duplicateID)
	if err != nil {
		return nil, err
	}
	item.ObjectID, item.Component, item.Site = nullableString(objectID), nullableString(component), nullableString(site)
	item.Priority, item.ResolvedAt = nullableString(priority), nullableTime(resolvedAt)
	item.IncidentID, item.DuplicateOfProblemID = nullableInt64(incidentID), nullableInt64(duplicateID)
	return &item, nil
}
