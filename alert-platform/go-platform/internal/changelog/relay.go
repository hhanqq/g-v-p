package changelog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Relay переносит несинхронизированные строки change_events в Kafka —
// тот же идиом, что уже дважды используется в проекте (signal_queue,
// delivery_outbox): FOR UPDATE SKIP LOCKED + пометка synced_at в той
// же транзакции, at-least-once, повтор на следующем тике при ошибке
// продюсера — строка просто остаётся с synced_at IS NULL.
type Relay struct {
	pool   *pgxpool.Pool
	client *kgo.Client
	topic  string
	batch  int
}

func NewRelay(pool *pgxpool.Pool, client *kgo.Client, topic string) *Relay {
	return &Relay{pool: pool, client: client, topic: topic, batch: 100}
}

type relayRow struct {
	id   int64
	wire WireEvent
}

// Tick переносит один батч и возвращает число перенесённых строк.
func (r *Relay) Tick(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id,event_id,occurred_at,actor,actor_role,action,resource_type,resource_id,before_json,after_json,result,detail
		FROM change_events WHERE synced_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, r.batch)
	if err != nil {
		return 0, err
	}
	var picked []relayRow
	for rows.Next() {
		var id int64
		var eventID string
		var occurredAt time.Time
		var actor, actorRole, action, resourceType, resourceID, result string
		var beforeJSON, afterJSON, detail *string
		if err := rows.Scan(&id, &eventID, &occurredAt, &actor, &actorRole, &action, &resourceType, &resourceID, &beforeJSON, &afterJSON, &result, &detail); err != nil {
			rows.Close()
			return 0, err
		}
		wire := WireEvent{
			EventID: eventID, Timestamp: occurredAt, ActorID: actor, ActorRole: actorRole, Action: action,
			ResourceType: resourceType, ResourceID: resourceID, Result: result,
		}
		if beforeJSON != nil {
			wire.Before = json.RawMessage(*beforeJSON)
		}
		if afterJSON != nil {
			wire.After = json.RawMessage(*afterJSON)
		}
		if detail != nil {
			wire.Detail = *detail
		}
		picked = append(picked, relayRow{id: id, wire: wire})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(picked) == 0 {
		return 0, nil
	}

	ids := make([]int64, 0, len(picked))
	for _, item := range picked {
		payload, err := json.Marshal(item.wire)
		if err != nil {
			return 0, err
		}
		key := item.wire.ResourceType + ":" + item.wire.ResourceID
		produceResult := r.client.ProduceSync(ctx, &kgo.Record{Topic: r.topic, Key: []byte(key), Value: payload})
		if err := produceResult.FirstErr(); err != nil {
			return 0, err
		}
		ids = append(ids, item.id)
	}

	if _, err := tx.Exec(ctx, `UPDATE change_events SET synced_at=$2 WHERE id=ANY($1)`, ids, time.Now().UTC()); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}
