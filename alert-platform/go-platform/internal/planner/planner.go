package planner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Planner struct {
	pool       *pgxpool.Pool
	ollama     *OllamaClient
	runbooks   map[string][]string
	publicURL  string
	inProgress sync.Map
}

type Symptom struct {
	ObjectName string
	Class      string
	RuleID     *string
}

type delivery struct {
	ProblemID             int64
	Type                  string
	Recipient             string
	ChatID                string
	ProviderChatID        *string
	ReplyToNotificationID *int64
	Text                  string
	AISummary             *string
	AIRecommendation      *string
	IdempotencySuffix     string
}

func New(ctx context.Context, databaseURL string, ollama *OllamaClient, runbooks map[string][]string) (*Planner, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Planner{pool: pool, ollama: ollama, runbooks: runbooks}, nil
}

func (planner *Planner) Close() { planner.pool.Close() }

func (planner *Planner) SetPublicConsoleURL(value string) {
	planner.publicURL = strings.TrimRight(value, "/")
}

func (planner *Planner) EnsureFallbackSubscriber(ctx context.Context, username string) error {
	if username == "" {
		return nil
	}
	tx, err := planner.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM subscribers`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return tx.Commit(ctx)
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	var subscriberID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO subscribers(trueconf_username, display_name, access_token, active, created_at)
		VALUES ($1, 'Тестовый получатель (по умолчанию)', $2, TRUE, $3)
		RETURNING id`, username, token, time.Now().UTC()).Scan(&subscriberID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO subscriptions(subscriber_id, subsidiary, service_id, priority_threshold, created_at)
		VALUES ($1, NULL, NULL, NULL, $2)`, subscriberID, time.Now().UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (planner *Planner) Tick(ctx context.Context) error {
	rows, err := planner.pool.Query(ctx, `
		SELECT id, duplicate_of_problem_id
		FROM problems
		WHERE status IN ('OPEN', 'FLAPPING')
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("load open problems: %w", err)
	}
	type openProblem struct {
		id        int64
		duplicate sql.NullInt64
	}
	var openProblems []openProblem
	for rows.Next() {
		var item openProblem
		if err := rows.Scan(&item.id, &item.duplicate); err != nil {
			rows.Close()
			return err
		}
		openProblems = append(openProblems, item)
	}
	rows.Close()

	for _, item := range openProblems {
		if item.duplicate.Valid {
			if err := planner.planDuplicate(ctx, item.id, item.duplicate.Int64); err != nil {
				log.Printf("delivery-planner: duplicate problem=%d: %v", item.id, err)
			}
			continue
		}
		if err := planner.planNew(ctx, item.id); err != nil {
			log.Printf("delivery-planner: new problem=%d: %v", item.id, err)
		}
	}
	if err := planner.planClosures(ctx); err != nil {
		return err
	}
	if err := planner.scheduleSupplements(ctx); err != nil {
		return err
	}
	if err := planner.planScenarios(ctx); err != nil {
		return err
	}
	if err := planner.planSLABreaches(ctx); err != nil {
		return err
	}
	return nil
}

func (planner *Planner) planNew(ctx context.Context, problemID int64) error {
	problem, err := planner.loadProblem(ctx, problemID)
	if err != nil {
		return err
	}
	recipients, err := planner.resolveRecipients(ctx, problem)
	if err != nil {
		return err
	}
	for _, recipient := range recipients {
		text := RenderNew(problem)
		if planner.publicURL != "" {
			var token string
			err := planner.pool.QueryRow(ctx,
				`SELECT access_token FROM subscribers WHERE trueconf_username=$1 AND active=TRUE`,
				recipient,
			).Scan(&token)
			if err == nil {
				text += fmt.Sprintf(
					"\n\n<a href=\"%s/alerts/%s/?token=%s\">Текущие алерты и последовательность</a>",
					planner.publicURL, url.PathEscape(recipient), url.QueryEscape(token),
				)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		_, err := planner.createDelivery(ctx, delivery{
			ProblemID: problem.ID,
			Type:      "NEW",
			Recipient: recipient,
			ChatID:    "recipient:" + recipient,
			Text:      text,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (planner *Planner) planDuplicate(ctx context.Context, problemID, originalProblemID int64) error {
	problem, err := planner.loadProblem(ctx, problemID)
	if err != nil {
		return err
	}
	original, err := planner.loadProblem(ctx, originalProblemID)
	if err != nil {
		return err
	}
	rows, err := planner.pool.Query(ctx, `
		SELECT id, chat_id, COALESCE(recipient, 'chat:' || chat_id)
		FROM notifications
		WHERE problem_id = $1 AND type = 'NEW' AND status = 'sent'
		ORDER BY id`, originalProblemID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var target NotificationTarget
		if err := rows.Scan(&target.ParentNotificationID, &target.ChatID, &target.Recipient); err != nil {
			return err
		}
		chatID := target.ChatID
		parentID := target.ParentNotificationID
		_, err := planner.createDelivery(ctx, delivery{
			ProblemID:             problemID,
			Type:                  "DUPLICATE_NOTE",
			Recipient:             target.Recipient,
			ChatID:                chatID,
			ProviderChatID:        &chatID,
			ReplyToNotificationID: &parentID,
			Text:                  RenderDuplicate(problemID, originalProblemID, original.IncidentID, problem.SourceSystem),
		})
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func (planner *Planner) planClosures(ctx context.Context) error {
	rows, err := planner.pool.Query(ctx, `
		SELECT p.id, n.id, n.chat_id, COALESCE(n.recipient, 'chat:' || n.chat_id)
		FROM problems p
		JOIN notifications n ON n.problem_id = p.id AND n.type = 'NEW' AND n.status = 'sent'
		WHERE p.status = 'RESOLVED'
		ORDER BY p.id, n.id`)
	if err != nil {
		return err
	}
	type closure struct {
		problemID int64
		target    NotificationTarget
	}
	var pending []closure
	for rows.Next() {
		var item closure
		if err := rows.Scan(&item.problemID, &item.target.ParentNotificationID, &item.target.ChatID, &item.target.Recipient); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	rows.Close()
	for _, item := range pending {
		problem, err := planner.loadProblem(ctx, item.problemID)
		if err != nil {
			return err
		}
		chatID := item.target.ChatID
		parentID := item.target.ParentNotificationID
		_, err = planner.createDelivery(ctx, delivery{
			ProblemID:             item.problemID,
			Type:                  "CLOSURE",
			Recipient:             item.target.Recipient,
			ChatID:                chatID,
			ProviderChatID:        &chatID,
			ReplyToNotificationID: &parentID,
			Text:                  RenderClosure(problem),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (planner *Planner) createDelivery(ctx context.Context, command delivery) (bool, error) {
	tx, err := planner.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	var notificationID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO notifications(
			problem_id, type, recipient, chat_id, status, created_at, ai_summary, ai_recommendation)
		VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		command.ProblemID, command.Type, command.Recipient, command.ChatID, now,
		command.AISummary, command.AIRecommendation,
	).Scan(&notificationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	idempotencyKey := fmt.Sprintf(
		"trueconf:problem:%d:type:%s:recipient:%s",
		command.ProblemID, command.Type, command.Recipient,
	)
	if command.IdempotencySuffix != "" {
		idempotencyKey += ":" + command.IdempotencySuffix
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO delivery_outbox(
			notification_id, contract_version, channel, idempotency_key,
			recipient, provider_chat_id, reply_to_notification_id, text,
			parse_mode, status, attempts, available_at, created_at)
		VALUES ($1, 1, 'trueconf', $2, $3, $4, $5, $6, 'HTML', 'pending', 0, $7, $7)`,
		notificationID, idempotencyKey, command.Recipient, command.ProviderChatID,
		command.ReplyToNotificationID, command.Text, now,
	)
	if err != nil {
		return false, fmt.Errorf("insert delivery outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (planner *Planner) loadProblem(ctx context.Context, problemID int64) (ProblemData, error) {
	var problem ProblemData
	var incidentID sql.NullInt64
	var priority, objectID, site, serviceName, hypothesis sql.NullString
	var resolvedAt, acknowledgedAt sql.NullTime
	err := planner.pool.QueryRow(ctx, `
		SELECT p.id, p.incident_id, p.priority, p.object_id,
		       COALESCE(object.name, p.object_id, 'неизвестный объект'),
		       p.site, service.name, p.symptom_class, p.ai_root_cause_hypothesis,
		       COALESCE(original.raw_body, '(оригинал не найден)'),
		       COALESCE(original.source_system, 'unknown'), p.opened_at, p.resolved_at,
		       p.closed_by_reconciliation,p.acknowledged_at
		FROM problems p
		LEFT JOIN cmdb_objects object ON object.id = p.object_id
		LEFT JOIN LATERAL (
			SELECT cmdb_services.name
			FROM cmdb_services
			JOIN cmdb_service_objects ON cmdb_service_objects.service_id = cmdb_services.id
			WHERE cmdb_service_objects.object_id = p.object_id
			LIMIT 1
		) service ON TRUE
		LEFT JOIN LATERAL (
			SELECT signals.raw_body, signals.source_system
			FROM events
			JOIN signals ON signals.id = events.signal_id
			WHERE events.problem_id = p.id
			ORDER BY events.id
			LIMIT 1
		) original ON TRUE
		WHERE p.id = $1`, problemID).Scan(
		&problem.ID, &incidentID, &priority, &objectID, &problem.ObjectName,
		&site, &serviceName, &problem.SymptomClass, &hypothesis,
		&problem.OriginalBody, &problem.SourceSystem, &problem.OpenedAt,
		&resolvedAt, &problem.ClosedByReconciliation, &acknowledgedAt,
	)
	if err != nil {
		return ProblemData{}, err
	}
	problem.IncidentID = int64Ptr(incidentID)
	problem.Priority = stringPtr(priority)
	problem.ObjectID = stringPtr(objectID)
	problem.Site = stringPtr(site)
	problem.ServiceName = stringPtr(serviceName)
	problem.AIRootCauseHypothesis = stringPtr(hypothesis)
	if resolvedAt.Valid {
		problem.ResolvedAt = resolvedAt.Time
	}
	if acknowledgedAt.Valid {
		problem.AcknowledgedAt = &acknowledgedAt.Time
	}
	return problem, nil
}

func (planner *Planner) resolveRecipients(ctx context.Context, problem ProblemData) ([]string, error) {
	subsidiaries := make(map[string]struct{})
	services := make(map[string]struct{})
	if problem.ObjectID != nil {
		rows, err := planner.pool.Query(ctx, `
			SELECT subsidiary FROM cmdb_ownership WHERE object_id = $1
			UNION
			SELECT ownership.subsidiary
			FROM cmdb_ownership ownership
			JOIN cmdb_service_objects service_object ON service_object.service_id = ownership.service_id
			WHERE service_object.object_id = $1`, *problem.ObjectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var subsidiary string
			if err := rows.Scan(&subsidiary); err != nil {
				rows.Close()
				return nil, err
			}
			subsidiaries[subsidiary] = struct{}{}
		}
		rows.Close()
		rows, err = planner.pool.Query(ctx,
			`SELECT service_id FROM cmdb_service_objects WHERE object_id = $1`, *problem.ObjectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var service string
			if err := rows.Scan(&service); err != nil {
				rows.Close()
				return nil, err
			}
			services[service] = struct{}{}
		}
		rows.Close()
	}
	rows, err := planner.pool.Query(ctx, `
		SELECT subscriber.trueconf_username, subscription.subsidiary,
		       subscription.service_id, subscription.priority_threshold
		FROM subscribers subscriber
		JOIN subscriptions subscription ON subscription.subscriber_id = subscriber.id
		WHERE subscriber.active = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subscriptions []Subscription
	for rows.Next() {
		var row Subscription
		var subsidiary, serviceID, threshold sql.NullString
		if err := rows.Scan(&row.Username, &subsidiary, &serviceID, &threshold); err != nil {
			return nil, err
		}
		row.Subsidiary = stringPtr(subsidiary)
		row.ServiceID = stringPtr(serviceID)
		row.PriorityThreshold = stringPtr(threshold)
		subscriptions = append(subscriptions, row)
	}
	return ResolveRecipients(subsidiaries, services, problem.Priority, subscriptions), rows.Err()
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
