package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool       *pgxpool.Pool
	connectors map[string]Connector
	passports  map[string]string
	priority   priorityConfig
	ai         AIClient
	batchSize  int
}

func New(ctx context.Context, databaseURL, connectorsDir, priorityPath string, ai AIClient) (*Service, error) {
	connectors, err := LoadConnectors(connectorsDir)
	if err != nil {
		return nil, err
	}
	priority, err := LoadPriorityConfig(priorityPath)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	service := &Service{pool: pool, connectors: connectors, priority: priority, ai: ai, batchSize: 20}
	if err := service.seedSources(ctx, filepath.Join(connectorsDir, "sources.yaml")); err != nil {
		pool.Close()
		return nil, err
	}
	if err := service.refreshPassports(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return service, nil
}

func (service *Service) Close() { service.pool.Close() }

func (service *Service) Tick(ctx context.Context) (int, error) {
	ids, err := service.claimBatch(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := service.processOne(ctx, id); err != nil {
			log.Printf("pipeline-worker: entry=%d: %v", id, err)
			if markErr := service.markFailed(ctx, id, err); markErr != nil {
				log.Printf("pipeline-worker: mark failed entry=%d: %v", id, markErr)
			}
		}
	}
	return len(ids), nil
}

func (service *Service) Sweep(ctx context.Context, stuckTimeout time.Duration) (closed, requeued int64, err error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id,symptom_class,last_seen_at FROM problems WHERE status IN ('OPEN','ACKNOWLEDGED','FLAPPING') FOR UPDATE`)
	if err != nil {
		return 0, 0, err
	}
	type staleCandidate struct {
		id       int64
		symptom  string
		lastSeen time.Time
	}
	var candidates []staleCandidate
	for rows.Next() {
		var candidate staleCandidate
		if err := rows.Scan(&candidate.id, &candidate.symptom, &candidate.lastSeen); err != nil {
			rows.Close()
			return 0, 0, err
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	now := time.Now().UTC()
	for _, candidate := range candidates {
		if now.Sub(candidate.lastSeen) <= ttlForSymptom(candidate.symptom) {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE problems SET status='RESOLVED',resolved_at=last_seen_at,closed_by_reconciliation=TRUE WHERE id=$1`, candidate.id); err != nil {
			return 0, 0, err
		}
		closed++
	}
	result, err := tx.Exec(ctx, `
		UPDATE signal_queue SET status='pending'
		WHERE status='processing' AND processed_at < $1`, now.Add(-stuckTimeout))
	if err != nil {
		return 0, 0, err
	}
	requeued = result.RowsAffected()
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	if err := service.refreshPassports(ctx); err != nil {
		return closed, requeued, err
	}
	return closed, requeued, nil
}

func (service *Service) claimBatch(ctx context.Context) ([]int64, error) {
	rows, err := service.pool.Query(ctx, `
		WITH picked AS (
			SELECT id FROM signal_queue WHERE status='pending' ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		UPDATE signal_queue AS queue SET status='processing',processed_at=$2
		FROM picked WHERE queue.id=picked.id RETURNING queue.id`, service.batchSize, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (service *Service) processOne(ctx context.Context, entryID int64) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var signalID int64
	var sourceSystem, sourceInstance, rawBody string
	var receivedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT signal.id,signal.source_system,signal.source_instance,signal.received_at,signal.raw_body
		FROM signal_queue AS queue JOIN signals AS signal ON signal.id=queue.signal_id
		WHERE queue.id=$1 FOR UPDATE OF queue`, entryID,
	).Scan(&signalID, &sourceSystem, &sourceInstance, &receivedAt, &rawBody)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE signal_queue SET attempts=attempts+1,processed_at=$1,error=NULL WHERE id=$2`, time.Now().UTC(), entryID); err != nil {
		return err
	}
	connector, ok := service.connectors[sourceSystem]
	if !ok {
		return finishParseFailure(ctx, tx, entryID, fmt.Sprintf("неизвестная система источника: %s", sourceSystem))
	}
	var site *string
	if passportSite, ok := service.passports[sourceInstance]; ok {
		site = &passportSite
	}
	parsed := Parse(connector, rawBody, sourceInstance, receivedAt, site)
	if !parsed.Success {
		return finishParseFailure(ctx, tx, entryID, parsed.Error)
	}
	event := *parsed.Event
	symptomSource := "rule"
	if event.SymptomClass == "unknown" {
		if classification := classifySymptom(ctx, service.ai, event.Title); classification != nil {
			event.SymptomClass = *classification
			symptomSource = "ai"
		}
	}
	resolution, err := resolve(ctx, tx, event.Site, event.ObjectNameRaw, event.IPRaw)
	if err != nil {
		return err
	}
	dedupKey := ComputeDedupKey(event.Site, resolution.ObjectID, event.Component, event.SymptomClass)
	item, err := applyEvent(ctx, tx, dedupKey, event, resolution)
	if err != nil {
		return err
	}
	if item != nil {
		technicalSeverity := defaultTechnicalSeverity
		if event.SeverityRaw != nil {
			if mapped, exists := connector.SeverityMap[*event.SeverityRaw]; exists {
				technicalSeverity = mapped
			}
		}
		businessImpact, err := loadBusinessImpact(ctx, tx, resolution.ObjectID, service.priority)
		if err != nil {
			return err
		}
		priority, breakdown := computePriority(service.priority, technicalSeverity, businessImpact, item.RepeatCount, event.OccurredAt, false)
		encoded, err := json.Marshal(breakdown)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE problems SET priority=$1,priority_breakdown=$2 WHERE id=$3`, priority, string(encoded), item.ID); err != nil {
			return err
		}
		item.Priority = &priority
		if err := tryCorrelate(ctx, tx, item); err != nil {
			return err
		}
		if item.RepeatCount == 1 {
			if err := service.applyAIEnrichment(ctx, tx, item, rawBody); err != nil {
				return err
			}
		}
	}
	var problemID *int64
	if item != nil {
		problemID = &item.ID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO events(signal_id,dedup_key,state,occurred_at,ingest_ts,symptom_class,severity_raw,title,
		                   site,object_name_raw,ip_raw,component,parser_version,resolved,object_id,
		                   resolution_method,resolution_confidence,problem_id,symptom_class_source)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		signalID, dedupKey, event.State, event.OccurredAt, event.IngestTS, event.SymptomClass,
		event.SeverityRaw, event.Title, event.Site, event.ObjectNameRaw, event.IPRaw, event.Component,
		event.ParserVersion, resolution.ObjectID != nil, resolution.ObjectID, resolution.Method,
		resolution.Confidence, problemID, symptomSource)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE signal_queue SET status='done',error=NULL WHERE id=$1`, entryID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (service *Service) applyAIEnrichment(ctx context.Context, tx pgx.Tx, item *problem, rawBody string) error {
	if service.ai == nil {
		return nil
	}
	if item.ObjectID != nil {
		var candidateID int64
		var candidateBody string
		err := tx.QueryRow(ctx, `
			SELECT candidate.id,COALESCE((
				SELECT signal.raw_body FROM events event JOIN signals signal ON signal.id=event.signal_id
				WHERE event.problem_id=candidate.id ORDER BY event.id LIMIT 1
			),'')
			FROM problems candidate
			WHERE candidate.object_id=$1 AND candidate.id<>$2 AND candidate.symptom_class<>$3
			  AND candidate.status IN ('OPEN','FLAPPING') AND candidate.duplicate_of_problem_id IS NULL
			  AND candidate.opened_at >= $4::timestamp - interval '300 seconds'
			ORDER BY candidate.opened_at DESC LIMIT 1`,
			*item.ObjectID, item.ID, item.SymptomClass, item.OpenedAt).Scan(&candidateID, &candidateBody)
		if err == nil {
			if verdict := isDuplicate(ctx, service.ai, rawBody, candidateBody); verdict != nil && *verdict {
				if _, err := tx.Exec(ctx, `UPDATE problems SET duplicate_of_problem_id=$1 WHERE id=$2`, candidateID, item.ID); err != nil {
					return err
				}
				item.DuplicateOfProblemID = &candidateID
			}
		} else if err != pgx.ErrNoRows {
			return err
		}
	}
	if item.IncidentID != nil || item.DuplicateOfProblemID != nil || item.Priority == nil || *item.Priority == "P3" || item.Site == nil {
		return nil
	}
	var siblingBody string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT signal.raw_body FROM events event JOIN signals signal ON signal.id=event.signal_id
			WHERE event.problem_id=sibling.id ORDER BY event.id LIMIT 1
		),'')
		FROM problems sibling
		WHERE sibling.site=$1 AND sibling.id<>$2 AND sibling.object_id<>$3 AND sibling.symptom_class<>$4
		  AND sibling.incident_id IS NULL AND sibling.duplicate_of_problem_id IS NULL
		  AND sibling.status IN ('OPEN','FLAPPING') AND sibling.priority IN ('P0','P1','P2')
		  AND sibling.opened_at >= $5::timestamp - interval '300 seconds'
		ORDER BY sibling.opened_at DESC LIMIT 1`, *item.Site, item.ID, item.ObjectID, item.SymptomClass, item.OpenedAt).Scan(&siblingBody)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if hypothesis := suggestRootCause(ctx, service.ai, *item.Site, siblingBody, rawBody); hypothesis != nil {
		_, err = tx.Exec(ctx, `UPDATE problems SET ai_root_cause_hypothesis=$1 WHERE id=$2`, *hypothesis, item.ID)
	}
	return err
}

func finishParseFailure(ctx context.Context, tx pgx.Tx, entryID int64, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE signal_queue SET status='parse_failed',error=$1 WHERE id=$2`, reason, entryID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (service *Service) markFailed(ctx context.Context, entryID int64, failure error) error {
	message := failure.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := service.pool.Exec(ctx, `UPDATE signal_queue SET status='parse_failed',error=$1 WHERE id=$2`, message, entryID)
	return err
}

func loadBusinessImpact(ctx context.Context, tx pgx.Tx, objectID *string, cfg priorityConfig) (int, error) {
	if objectID == nil {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT service.criticality FROM cmdb_services service
		JOIN cmdb_service_objects relation ON relation.service_id=service.id
		WHERE relation.object_id=$1`, *objectID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	impact := 0
	for rows.Next() {
		var criticality string
		if err := rows.Scan(&criticality); err != nil {
			return 0, err
		}
		if candidate := cfg.BusinessImpactByCriticality[criticality]; candidate > impact {
			impact = candidate
		}
	}
	return impact, rows.Err()
}

func (service *Service) refreshPassports(ctx context.Context) error {
	rows, err := service.pool.Query(ctx, `SELECT instance,site FROM source_instances`)
	if err != nil {
		return err
	}
	defer rows.Close()
	passports := make(map[string]string)
	for rows.Next() {
		var instance, site string
		if err := rows.Scan(&instance, &site); err != nil {
			return err
		}
		passports[instance] = site
	}
	if err := rows.Err(); err != nil {
		return err
	}
	service.passports = passports
	return nil
}

func (service *Service) seedSources(ctx context.Context, path string) error {
	var count int
	if err := service.pool.QueryRow(ctx, `SELECT COUNT(*) FROM source_instances`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	seed, err := loadSourceSeed(path)
	if err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, source := range seed.Sources {
		_, err := tx.Exec(ctx, `
			INSERT INTO source_instances(instance,system,site,created_at)
			VALUES($1,$2,$3,$4) ON CONFLICT(instance) DO NOTHING`,
			source.Instance, source.System, source.Site, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func ttlForSymptom(symptom string) time.Duration {
	seconds := map[string]int{
		"host_unreachable": 600, "node_down": 600, "interface_down": 300,
		"service_down": 900, "power_lost": 900, "disk_space": 1800, "unknown": 1200,
	}
	value, ok := seconds[symptom]
	if !ok {
		value = seconds["unknown"]
	}
	return time.Duration(value) * time.Second
}
