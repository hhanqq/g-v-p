package planner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	RoutingTrace          any
	// Channel — "trueconf" (по умолчанию, сохраняет поведение до Части
	// VII ТЗ) или "email". Один Notification соответствует ровно одной
	// строке delivery_outbox (UNIQUE(notification_id) в схеме) — поэтому
	// параллельная доставка в оба канала не расширяет одну строку, а
	// создаёт вторую пару Notification+outbox с тем же содержанием и
	// каналом "email"; execCreateDelivery для этого достаточно вызвать
	// дважды с разным Channel, схема не меняется.
	Channel  string
	Subject  *string
	BodyHTML *string
}

// RecipientDecision — один получатель после применения доступности к
// уже отфильтрованному по подписке списку (resolveRecipients). Раздел
// «Маршрутизация с учётом доступности»: недоступные НЕ исключаются из
// набора целиком (в отличие от сценарных узлов availability_check/
// first_available — это осознанное решение, полное исключение
// недоступных для этого пути никем не запрошено и меняло бы поведение
// самого высоконагруженного тракта), только delegation-интервал
// перенаправляет получателя на делегата вместо него.
type RecipientDecision struct {
	Username      string
	SubscriberID  int64
	Available     bool
	Kind          string
	DelegatedFrom *string
}

func (planner *Planner) resolveRoutingDecisions(ctx context.Context, usernames []string, at time.Time) ([]RecipientDecision, error) {
	if len(usernames) == 0 {
		return nil, nil
	}
	rows, err := planner.pool.Query(ctx, `SELECT id, trueconf_username FROM subscribers WHERE trueconf_username = ANY($1) AND active = TRUE`, usernames)
	if err != nil {
		return nil, err
	}
	type subject struct {
		id       int64
		username string
	}
	var subjects []subject
	ids := make([]int64, 0, len(usernames))
	for rows.Next() {
		var s subject
		if err := rows.Scan(&s.id, &s.username); err != nil {
			rows.Close()
			return nil, err
		}
		subjects = append(subjects, s)
		ids = append(ids, s.id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	statuses, err := availability.Resolve(ctx, planner.pool, ids, at)
	if err != nil {
		return nil, err
	}

	var decisions []RecipientDecision
	seen := make(map[string]bool)
	for _, s := range subjects {
		status := statuses[s.id]
		if status.Kind == "delegation" && status.DelegateTo != nil {
			var delegateUsername string
			err := planner.pool.QueryRow(ctx, `SELECT trueconf_username FROM subscribers WHERE id=$1 AND active=TRUE`, *status.DelegateTo).Scan(&delegateUsername)
			if errors.Is(err, pgx.ErrNoRows) {
				// Делегат неактивен — редкая гонка. Редирект некуда;
				// намеренно НЕ откатываемся на делегатора (он явно
				// попросил его не тревожить), пропускаем этого получателя.
				continue
			}
			if err != nil {
				return nil, err
			}
			if seen[delegateUsername] {
				continue
			}
			seen[delegateUsername] = true
			delegator := s.username
			decisions = append(decisions, RecipientDecision{Username: delegateUsername, SubscriberID: *status.DelegateTo, Available: true, Kind: "delegation", DelegatedFrom: &delegator})
			continue
		}
		if seen[s.username] {
			continue
		}
		seen[s.username] = true
		decisions = append(decisions, RecipientDecision{Username: s.username, SubscriberID: s.id, Available: status.Available, Kind: status.Kind})
	}
	return decisions, nil
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
	if err := planner.planAIAnalysisRequests(ctx); err != nil {
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
	decisions, err := planner.resolveRoutingDecisions(ctx, recipients, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, decision := range decisions {
		recipient := decision.Username
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
		trace := map[string]any{
			"available": decision.Available, "kind": decision.Kind, "delegated_from": decision.DelegatedFrom,
		}
		if _, err := planner.createDelivery(ctx, delivery{
			ProblemID: problem.ID, Type: "NEW", Recipient: recipient,
			ChatID: "recipient:" + recipient, Text: text, RoutingTrace: trace,
		}); err != nil {
			return err
		}
		channels, err := planner.subscriberChannels(ctx, decision.SubscriberID)
		if err != nil {
			return err
		}
		if channels.emailEnabled && channels.email != nil {
			subject := fmt.Sprintf("[%s] %s", stringOrDash(problem.Priority), problem.SymptomClass)
			if _, err := planner.createDelivery(ctx, delivery{
				ProblemID: problem.ID, Type: "NEW", Recipient: *channels.email,
				ChatID: "email:" + *channels.email, Channel: "email",
				Subject: &subject, Text: text, RoutingTrace: trace,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

type subscriberChannelPrefs struct {
	trueconfEnabled, emailEnabled bool
	email                         *string
}

// subscriberChannels — раздел VII ТЗ: параллельная доставка в оба канала
// на основе предпочтений сотрудника (subscribers.trueconf_enabled/
// email_enabled, миграция 0017). TrueConf-путь выше не проверяет
// trueconf_enabled намеренно — отключение TrueConf для человека, у
// которого это единственный канал получения NEW, увело бы уведомление в
// никуда; email — строго дополнительный канал, не замена.
func (planner *Planner) subscriberChannels(ctx context.Context, subscriberID int64) (subscriberChannelPrefs, error) {
	var prefs subscriberChannelPrefs
	var email sql.NullString
	err := planner.pool.QueryRow(ctx,
		`SELECT trueconf_enabled, email_enabled, email FROM subscribers WHERE id=$1`, subscriberID,
	).Scan(&prefs.trueconfEnabled, &prefs.emailEnabled, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return prefs, nil
	}
	if err != nil {
		return prefs, err
	}
	if email.Valid && email.String != "" {
		prefs.email = &email.String
	}
	return prefs, nil
}

func stringOrDash(value *string) string {
	if value == nil {
		return "—"
	}
	return *value
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

// dbTx — общий знаменатель *pgxpool.Pool и pgx.Tx (по образцу
// changelog.Execer). execCreateDelivery принимает его вместо
// *pgxpool.Pool напрямую, чтобы сценарный notify-путь
// (internal/planner/automation.go) мог создавать доставку на уже
// открытой и залоченной FOR UPDATE SKIP LOCKED транзакции — тогда лок
// строки scenario_runs покрывает и продвижение состояния, и создание
// доставки одной атомарной единицей, без окна между ними.
type dbTx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (planner *Planner) createDelivery(ctx context.Context, command delivery) (bool, error) {
	tx, err := planner.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	created, err := execCreateDelivery(ctx, tx, command)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return created, nil
}

func execCreateDelivery(ctx context.Context, dbc dbTx, command delivery) (bool, error) {
	// Идемпотентность держится на этом ключе (стабилен: problem+type+
	// recipient, не зависит от chat_id). Раньше единственной защитой был
	// ON CONFLICT DO NOTHING на notifications(problem_id,type,chat_id) —
	// но chat_id у notifications ПЕРЕЗАПИСЫВАЕТСЯ Python-адаптером с
	// плейсхолдера "recipient:X" на реальный provider chat_id после
	// успешной отправки (services/delivery_trueconf/outbox.py). На
	// следующем тике та же проверка уже не находит совпадения (плейсхолдер
	// снова "recipient:X", а у существующей строки — реальный chat_id),
	// создаёт лишнюю notifications-строку и падает только на INSERT в
	// delivery_outbox — уже отправленные проблемы, остающиеся открытыми
	// несколько тиков, спамили этой ошибкой на каждый тик. Проверяем по
	// стабильному ключу ДО каких-либо вставок.
	channel := command.Channel
	if channel == "" {
		channel = "trueconf"
	}
	idempotencyKey := fmt.Sprintf(
		"%s:problem:%d:type:%s:recipient:%s",
		channel, command.ProblemID, command.Type, command.Recipient,
	)
	if command.IdempotencySuffix != "" {
		idempotencyKey += ":" + command.IdempotencySuffix
	}

	var alreadyQueued bool
	if err := dbc.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM delivery_outbox WHERE idempotency_key = $1)`,
		idempotencyKey,
	).Scan(&alreadyQueued); err != nil {
		return false, fmt.Errorf("check delivery idempotency: %w", err)
	}
	if alreadyQueued {
		return false, nil
	}

	now := time.Now().UTC()
	var routingTraceJSON *string
	if command.RoutingTrace != nil {
		if data, err := json.Marshal(command.RoutingTrace); err == nil {
			text := string(data)
			routingTraceJSON = &text
		}
	}
	var notificationID int64
	err := dbc.QueryRow(ctx, `
		INSERT INTO notifications(
			problem_id, type, recipient, chat_id, status, created_at, ai_summary, ai_recommendation, routing_trace_json)
		VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7, $8)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		command.ProblemID, command.Type, command.Recipient, command.ChatID, now,
		command.AISummary, command.AIRecommendation, routingTraceJSON,
	).Scan(&notificationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	parseMode := "HTML"
	if channel == "email" {
		parseMode = "PLAIN"
	}
	_, err = dbc.Exec(ctx, `
		INSERT INTO delivery_outbox(
			notification_id, contract_version, channel, idempotency_key,
			recipient, provider_chat_id, reply_to_notification_id, text,
			subject, body_html, parse_mode, status, attempts, available_at, created_at)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', 0, $11, $11)`,
		notificationID, channel, idempotencyKey, command.Recipient, command.ProviderChatID,
		command.ReplyToNotificationID, command.Text, command.Subject, command.BodyHTML, parseMode, now,
	)
	if err != nil {
		return false, fmt.Errorf("insert delivery outbox: %w", err)
	}
	return true, nil
}

func (planner *Planner) loadProblem(ctx context.Context, problemID int64) (ProblemData, error) {
	var problem ProblemData
	var incidentID sql.NullInt64
	var priority, objectID, equipmentType, site, serviceName, hypothesis sql.NullString
	var resolvedAt, acknowledgedAt sql.NullTime
	err := planner.pool.QueryRow(ctx, `
		SELECT p.id, p.incident_id, p.priority, p.object_id,
		       COALESCE(object.name, p.object_id, 'неизвестный объект'), object.equipment_type,
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
		&problem.ID, &incidentID, &priority, &objectID, &problem.ObjectName, &equipmentType,
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
	problem.EquipmentType = stringPtr(equipmentType)
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
