package changelog

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Sink — Kafka consumer group → ClickHouse batch insert. Offset
// коммитится только после успешной вставки батча: при сбое ClickHouse
// сообщения не теряются, а перечитываются на следующей попытке.
// ReplacingMergeTree на стороне ClickHouse (clickhouse_migrations/0001)
// сходится к одной строке на event_id при такой повторной доставке —
// не нужна отдельная idempotency-логика на этой стороне.
type Sink struct {
	consumer      *kgo.Client
	ch            clickhouse.Conn
	flushEvery    int
	flushInterval time.Duration
}

func NewSink(consumer *kgo.Client, ch clickhouse.Conn) *Sink {
	return &Sink{consumer: consumer, ch: ch, flushEvery: 200, flushInterval: 2 * time.Second}
}

// Run блокирует до отмены ctx, накапливая записи и сбрасывая их в
// ClickHouse батчами — тот же принцип "не в горячем пути", что у
// остального проекта: отставание или недоступность ClickHouse не
// влияет ни на приём событий, ни на доставку уведомлений, только на
// задержку появления записи в low-code поиске.
func (s *Sink) Run(ctx context.Context) error {
	var buf []WireEvent
	flushDeadline := time.Now().Add(s.flushInterval)
	for {
		if ctx.Err() != nil {
			return nil
		}
		pollCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		fetches := s.consumer.PollFetches(pollCtx)
		cancel()
		fetches.EachError(func(_ string, _ int32, err error) {
			// Пустой опрос (нет новых сообщений в течение 500мс) —
			// ожидаемый, не ошибочный случай, отдельный context каждую
			// итерацию; логируем только реальные ошибки брокера.
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("changelog-worker: kafka fetch error: %v", err)
		})
		fetches.EachRecord(func(record *kgo.Record) {
			var event WireEvent
			if err := json.Unmarshal(record.Value, &event); err != nil {
				log.Printf("changelog-worker: bad change_events message, skipped: %v", err)
				return
			}
			buf = append(buf, event)
		})
		shouldFlush := len(buf) >= s.flushEvery || (len(buf) > 0 && time.Now().After(flushDeadline))
		if shouldFlush {
			if err := s.flush(ctx, buf); err != nil {
				log.Printf("changelog-worker: clickhouse flush failed, will retry: %v", err)
				continue
			}
			if err := s.consumer.CommitUncommittedOffsets(ctx); err != nil {
				log.Printf("changelog-worker: commit offsets failed: %v", err)
			}
			buf = buf[:0]
			flushDeadline = time.Now().Add(s.flushInterval)
		}
	}
}

func (s *Sink) flush(ctx context.Context, events []WireEvent) error {
	batch, err := s.ch.PrepareBatch(ctx, `
		INSERT INTO alerting.change_events
		(event_id, occurred_at, actor, actor_role, action, resource_type, resource_id, result, detail, before_json, after_json)`)
	if err != nil {
		return err
	}
	for _, event := range events {
		before, after := "", ""
		if len(event.Before) > 0 {
			before = string(event.Before)
		}
		if len(event.After) > 0 {
			after = string(event.After)
		}
		if err := batch.Append(
			event.EventID, event.Timestamp, event.ActorID, event.ActorRole, event.Action,
			event.ResourceType, event.ResourceID, event.Result, event.Detail, before, after,
		); err != nil {
			return err
		}
	}
	return batch.Send()
}
