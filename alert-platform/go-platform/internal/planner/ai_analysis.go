package planner

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

type aiAnalysisCandidate struct {
	RequestID int64
	ProblemID int64
	ReplyToID int64
}

// planAIAnalysisRequests — сотрудник попросил в чате разобрать алерт
// (services/delivery_trueconf/handlers.py пишет узкую заявку). Один
// вызов Ollama на заявку, асинхронно (тот же паттерн, что
// scheduleSupplements), чтобы медленный ответ модели не блокировал Tick.
func (planner *Planner) planAIAnalysisRequests(ctx context.Context) error {
	rows, err := planner.pool.Query(ctx, `
		SELECT id, problem_id, reply_to_notification_id
		FROM ai_analysis_requests WHERE status='pending' ORDER BY id`)
	if err != nil {
		return err
	}
	var candidates []aiAnalysisCandidate
	for rows.Next() {
		var candidate aiAnalysisCandidate
		if err := rows.Scan(&candidate.RequestID, &candidate.ProblemID, &candidate.ReplyToID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	for _, candidate := range candidates {
		key := fmt.Sprintf("ai-analysis:%d", candidate.RequestID)
		if _, loaded := planner.inProgress.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		go func(item aiAnalysisCandidate) {
			defer planner.inProgress.Delete(key)
			if err := planner.processAIAnalysisRequest(ctx, item); err != nil {
				log.Printf("delivery-planner: ai-analysis request=%d: %v", item.RequestID, err)
			}
		}(candidate)
	}
	return nil
}

func (planner *Planner) processAIAnalysisRequest(ctx context.Context, candidate aiAnalysisCandidate) error {
	problem, err := planner.loadProblem(ctx, candidate.ProblemID)
	if err != nil {
		return err
	}

	var symptoms []Symptom
	if problem.IncidentID != nil {
		rows, err := planner.pool.Query(ctx, `
			SELECT COALESCE(object.name, problem.object_id, 'неизвестный объект'), problem.symptom_class
			FROM incident_problems incident_problem
			JOIN problems problem ON problem.id = incident_problem.problem_id
			LEFT JOIN cmdb_objects object ON object.id = problem.object_id
			WHERE incident_problem.incident_id = $1 AND incident_problem.role = 'symptom'
			ORDER BY incident_problem.id`, *problem.IncidentID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var symptom Symptom
			if err := rows.Scan(&symptom.ObjectName, &symptom.Class); err != nil {
				rows.Close()
				return err
			}
			symptoms = append(symptoms, symptom)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	queryText := strings.TrimSpace(problem.SymptomClass + " " + problem.OriginalBody)
	kbChunks := planner.searchKnowledgeBase(ctx, queryText, 4)

	prompt := BuildOnDemandAnalysisPrompt(problem.ObjectName, valueOr(problem.Site, "?"), problem.SymptomClass, problem.OpenedAt, symptoms, kbChunks)
	analysis := planner.ollama.Ask(ctx, prompt, 500)

	text := RenderAIAnalysis(AIAnalysisData{
		ProblemID: problem.ID, IncidentID: problem.IncidentID, ObjectName: problem.ObjectName,
		SymptomClass: problem.SymptomClass, Site: problem.Site, Priority: problem.Priority,
		RelatedCount: len(symptoms), AIText: analysis,
	})

	var chatID, recipient string
	if err := planner.pool.QueryRow(ctx,
		`SELECT chat_id, COALESCE(recipient,'chat:'||chat_id) FROM notifications WHERE id=$1`,
		candidate.ReplyToID,
	).Scan(&chatID, &recipient); err != nil {
		return err
	}
	replyTo := candidate.ReplyToID
	if _, err := planner.createDelivery(ctx, delivery{
		ProblemID: candidate.ProblemID, Type: "AI_ANALYSIS", Recipient: recipient,
		ChatID: chatID, ProviderChatID: &chatID, ReplyToNotificationID: &replyTo,
		Text:              text,
		IdempotencySuffix: fmt.Sprintf("ai-analysis:%d", candidate.RequestID),
	}); err != nil {
		return err
	}

	_, err = planner.pool.Exec(ctx,
		`UPDATE ai_analysis_requests SET status='done', processed_at=$2 WHERE id=$1`,
		candidate.RequestID, time.Now().UTC())
	return err
}
