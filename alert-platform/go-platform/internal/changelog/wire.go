package changelog

import (
	"encoding/json"
	"time"
)

// WireEvent — форма сообщения в топике change_events.v1: дословно
// целевая архитектура (timestamp, actor_id, actor_role, action,
// resource_type, resource_id, before, after, result), плюс EventID —
// нужен ClickHouse-стороне для дедупликации при повторной доставке
// (ReplacingMergeTree, database/clickhouse_migrations/0001).
type WireEvent struct {
	EventID      string          `json:"event_id"`
	Timestamp    time.Time       `json:"timestamp"`
	ActorID      string          `json:"actor_id"`
	ActorRole    string          `json:"actor_role"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Before       json.RawMessage `json:"before,omitempty"`
	After        json.RawMessage `json:"after,omitempty"`
	Result       string          `json:"result"`
	Detail       string          `json:"detail,omitempty"`
}
