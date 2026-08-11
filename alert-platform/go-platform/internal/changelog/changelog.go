// Package changelog records структурированную историю изменений
// (change_events) поверх существующего тонкого audit_log — before/after
// снимок на конкретную сущность (объект оборудования, сотрудник,
// группа, источник, подписка), а не просто строка "кто/что/когда".
// Пишется дополнительно к audit_log, не вместо него — /api/audit и
// Audit.tsx этот пакет не затрагивает.
package changelog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer — общий знаменатель *pgxpool.Pool и pgx.Tx: Record можно
// вызывать как best-effort отдельным вызовом (по образцу updateGroup),
// так и внутри уже открытой транзакции вызывающего (по образцу
// createGroup/addSource), когда before/after должны быть согласованным
// снимком под блокировкой.
type Execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Event — одна запись истории изменений. Форма соответствует целевой
// архитектуре (timestamp, actor_id, actor_role, action, resource_type,
// resource_id, before, after, result), адаптированной под конвенции
// этого репозитория.
type Event struct {
	OccurredAt   time.Time
	Actor        string
	ActorRole    string // "admin" | "user"
	Action       string // "cmdb_object.create", "subscriber.update", "group.create", ...
	ResourceType string // "cmdb_object" | "subscriber" | "group" | "source_instance" | "subscription"
	ResourceID   string
	Before       any    // маршалится в before_json; nil для create
	After        any    // маршалится в after_json; nil для delete
	Result       string // "success" | "failure"; пусто -> "success"
	Detail       string
	// ActorType/InitiatedBy — раздел «ADP AI» доп. ТЗ: действие, выполненное
	// ADP AI от имени пользователя, попадает в тот же Global Audit, но
	// помечено actor_type="ai", initiated_by=<username>. Пусто -> "user"
	// (обычные admin-мутации это поле не заполняют).
	ActorType   string
	InitiatedBy string
}

// RoleFromUser выводит actor_role из session-карты пользователя,
// которую уже передают все admin-хендлеры (user["is_admin"].(bool)) —
// отдельная модель ролей для этого не нужна.
func RoleFromUser(user map[string]any) string {
	if isAdmin, _ := user["is_admin"].(bool); isAdmin {
		return "admin"
	}
	return "user"
}

// Record вставляет одну строку change_events. Ошибка маршалинга
// before/after вырождается в NULL-колонку, а не в отказ вызова —
// история изменений best-effort-наблюдаемость, а не условие
// корректности транзакции, внутри которой её обычно вызывают.
func Record(ctx context.Context, exec Execer, ev Event) error {
	result := ev.Result
	if result == "" {
		result = "success"
	}
	actorType := ev.ActorType
	if actorType == "" {
		actorType = "user"
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO change_events(occurred_at,actor,actor_role,action,resource_type,resource_id,before_json,after_json,result,detail,actor_type,initiated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		ev.OccurredAt, ev.Actor, ev.ActorRole, ev.Action, ev.ResourceType, ev.ResourceID,
		marshal(ev.Before), marshal(ev.After), result, nullIfEmpty(ev.Detail), actorType, nullIfEmpty(ev.InitiatedBy))
	return err
}

func marshal(v any) *string {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
