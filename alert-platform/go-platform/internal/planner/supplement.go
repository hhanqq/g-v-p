package planner

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

type supplementCandidate struct {
	IncidentID int64
	ProblemID  int64
	Target     NotificationTarget
}

func (planner *Planner) scheduleSupplements(ctx context.Context) error {
	rows, err := planner.pool.Query(ctx, `
		SELECT incident.id, incident.root_problem_id, notification.id,
		       notification.chat_id,
		       COALESCE(notification.recipient, 'chat:' || notification.chat_id)
		FROM incidents incident
		JOIN notifications notification
		  ON notification.problem_id = incident.root_problem_id
		 AND notification.type = 'NEW'
		 AND notification.status = 'sent'
		WHERE NOT EXISTS (
			SELECT 1 FROM notifications supplement
			WHERE supplement.problem_id = incident.root_problem_id
			  AND supplement.type = 'SUPPLEMENT'
			  AND supplement.recipient = COALESCE(notification.recipient, 'chat:' || notification.chat_id)
		)
		ORDER BY incident.id, notification.id`)
	if err != nil {
		return err
	}
	var candidates []supplementCandidate
	for rows.Next() {
		var candidate supplementCandidate
		if err := rows.Scan(
			&candidate.IncidentID,
			&candidate.ProblemID,
			&candidate.Target.ParentNotificationID,
			&candidate.Target.ChatID,
			&candidate.Target.Recipient,
		); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	for _, candidate := range candidates {
		key := fmt.Sprintf("%d:%s", candidate.ProblemID, candidate.Target.Recipient)
		if _, loaded := planner.inProgress.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		go func(item supplementCandidate) {
			defer planner.inProgress.Delete(key)
			if err := planner.planSupplement(ctx, item); err != nil {
				log.Printf("delivery-planner: supplement problem=%d recipient=%s: %v",
					item.ProblemID, item.Target.Recipient, err)
			}
		}(candidate)
	}
	return nil
}

func (planner *Planner) planSupplement(ctx context.Context, candidate supplementCandidate) error {
	root, err := planner.loadProblem(ctx, candidate.ProblemID)
	if err != nil {
		return err
	}
	rows, err := planner.pool.Query(ctx, `
		SELECT COALESCE(object.name, problem.object_id, 'неизвестный объект'),
		       problem.symptom_class, incident_problem.rule_id
		FROM incident_problems incident_problem
		JOIN problems problem ON problem.id = incident_problem.problem_id
		LEFT JOIN cmdb_objects object ON object.id = problem.object_id
		WHERE incident_problem.incident_id = $1
		  AND incident_problem.role = 'symptom'
		ORDER BY incident_problem.id`, candidate.IncidentID)
	if err != nil {
		return err
	}
	var symptoms []Symptom
	ruleSet := make(map[string]struct{})
	for rows.Next() {
		var symptom Symptom
		var rule sql.NullString
		if err := rows.Scan(&symptom.ObjectName, &symptom.Class, &rule); err != nil {
			rows.Close()
			return err
		}
		symptom.RuleID = stringPtr(rule)
		if symptom.RuleID != nil {
			ruleSet[*symptom.RuleID] = struct{}{}
		}
		symptoms = append(symptoms, symptom)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var serviceCount int
	err = planner.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT service.name)
		FROM incident_problems incident_problem
		JOIN problems problem ON problem.id = incident_problem.problem_id
		JOIN LATERAL (
			SELECT cmdb_services.name
			FROM cmdb_services
			JOIN cmdb_service_objects ON cmdb_service_objects.service_id = cmdb_services.id
			WHERE cmdb_service_objects.object_id = problem.object_id
			LIMIT 1
		) service ON TRUE
		WHERE incident_problem.incident_id = $1
		  AND incident_problem.role = 'symptom'`, candidate.IncidentID).Scan(&serviceCount)
	if err != nil {
		return err
	}

	rules := sortedSet(ruleSet)
	rootSite := valueOr(root.Site, "?")
	summaryPrompt := BuildSummaryPrompt(
		root.SymptomClass, root.ObjectName, rootSite, root.OpenedAt, symptoms, rules,
	)
	summary := planner.ollama.Ask(ctx, summaryPrompt, 300)
	checklist := planner.runbooks[root.SymptomClass]
	var recommendation *string
	if prompt := BuildRecommendationPrompt(
		root.SymptomClass, root.ObjectName, rootSite, len(symptoms), checklist,
	); prompt != nil {
		recommendation = planner.ollama.Ask(ctx, *prompt, 200)
	}
	renderData := SupplementData{
		ProblemID:        candidate.ProblemID,
		IncidentID:       candidate.IncidentID,
		RootObject:       root.ObjectName,
		RootSymptom:      root.SymptomClass,
		OpenedAt:         root.OpenedAt,
		SymptomsCount:    len(symptoms),
		ServicesCount:    serviceCount,
		RuleNames:        rules,
		AISummary:        summary,
		AIRecommendation: recommendation,
	}
	if recommendation == nil {
		renderData.Checklist = checklist
	}
	chatID := candidate.Target.ChatID
	parentID := candidate.Target.ParentNotificationID
	_, err = planner.createDelivery(ctx, delivery{
		ProblemID:             candidate.ProblemID,
		Type:                  "SUPPLEMENT",
		Recipient:             candidate.Target.Recipient,
		ChatID:                chatID,
		ProviderChatID:        &chatID,
		ReplyToNotificationID: &parentID,
		Text:                  RenderSupplement(renderData),
		AISummary:             summary,
		AIRecommendation:      recommendation,
	})
	return err
}
