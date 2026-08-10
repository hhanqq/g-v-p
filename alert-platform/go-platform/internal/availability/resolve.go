// Package availability — единственный источник правды для «доступен
// ли сотрудник сейчас», начиная с раздела «Управление реакцией и
// доступностью». employee_availability — таблица интервалов
// (valid_from/valid_until); при пересечении нескольких интервалов у
// одного сотрудника побеждает не последний по времени вставки, а тот,
// чей kind стоит выше в таблице приоритета ниже.
package availability

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// dbTx — общий знаменатель *pgxpool.Pool и pgx.Tx (по образцу
// changelog.Execer/planner.dbTx): вызывающий код может резолвить
// доступность как отдельным запросом, так и внутри уже открытой
// транзакции (например, сценарный узел availability_check —
// internal/planner/automation.go — резолвит доступность на той же
// залоченной транзакции, что продвигает прогон).
type dbTx interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Status — резолвленная доступность одного сотрудника на момент At.
type Status struct {
	Available bool
	// Kind пуст, если ни один интервал не покрывает момент At — тогда
	// Available=true (сегодняшнее неявное поведение по умолчанию).
	Kind string
	// DelegateTo — ID сотрудника-делегата, только когда Kind=="delegation".
	DelegateTo *int64
}

type priorityRule struct {
	rank      int
	available bool
}

// priorities — единственная таблица правил приоритета, по духу тот же
// whitelist-паттерн, что changelog.Fields: неизвестный kind просто не
// матчится ни в одно правило (CHECK-ограничение в БД не должно этого
// допустить, но резолвер не паникует, если это всё же произойдёт).
var priorities = map[string]priorityRule{
	"override_available":   {rank: 100, available: true},
	"override_unavailable": {rank: 100, available: false},
	"sick_leave":           {rank: 90, available: false},
	"vacation":             {rank: 85, available: false},
	"delegation":           {rank: 70, available: false},
	"shift":                {rank: 60, available: true},
	"on_call":              {rank: 50, available: true},
	"unavailable":          {rank: 40, available: false},
	"available":            {rank: 10, available: true},
}

// Interval — сырая строка employee_availability. Экспортирован (не
// только для Resolve): internal/coverage.Sweep резолвит доступность в
// НЕСКОЛЬКИХ точках времени сразу и должен уметь подмешать один
// гипотетический (ещё не сохранённый) интервал в реальный набор — это
// единственный способ, которым dry-run проверяет «что если» без
// materialized view.
type Interval struct {
	ID           int64
	SubscriberID int64
	Kind         string
	DelegateTo   *int64
	ValidFrom    time.Time
	ValidUntil   *time.Time
	CreatedAt    time.Time
}

// ResolveFromIntervals — чистая (без обращения к БД) версия резолвинга:
// на входе уже загруженные интервалы (реальные и, опционально,
// гипотетические) и момент времени at. Resolve ниже — тонкая обёртка,
// которая сама загружает интервалы, покрывающие at, и зовёт эту
// функцию; coverage.Sweep вызывает её напрямую в цикле по нескольким
// моментам на одном и том же заранее загруженном наборе интервалов.
func ResolveFromIntervals(intervals []Interval, subscriberIDs []int64, at time.Time) map[int64]Status {
	result := make(map[int64]Status, len(subscriberIDs))
	for _, id := range subscriberIDs {
		result[id] = Status{Available: true}
	}
	best := make(map[int64]Interval)
	for _, iv := range intervals {
		if iv.ValidFrom.After(at) {
			continue
		}
		if iv.ValidUntil != nil && !iv.ValidUntil.After(at) {
			continue
		}
		if current, exists := best[iv.SubscriberID]; !exists || outranks(iv, current) {
			best[iv.SubscriberID] = iv
		}
	}
	for subscriberID, iv := range best {
		rule, ok := priorities[iv.Kind]
		if !ok {
			continue
		}
		result[subscriberID] = Status{Available: rule.available, Kind: iv.Kind, DelegateTo: iv.DelegateTo}
	}
	return result
}

// Resolve возвращает доступность каждого из subscriberIDs на момент at.
// Сотрудник без единого покрывающего интервала считается доступным —
// это явно сохраняет сегодняшнее неявное поведение как базовый случай,
// а не молчаливую дыру в данных.
func Resolve(ctx context.Context, dbc dbTx, subscriberIDs []int64, at time.Time) (map[int64]Status, error) {
	if len(subscriberIDs) == 0 {
		return map[int64]Status{}, nil
	}
	rows, err := dbc.Query(ctx, `
		SELECT id, subscriber_id, kind, delegate_to_subscriber_id, valid_from, valid_until, created_at
		FROM employee_availability
		WHERE subscriber_id = ANY($1) AND valid_from <= $2 AND (valid_until IS NULL OR valid_until > $2)`,
		subscriberIDs, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intervals []Interval
	for rows.Next() {
		var iv Interval
		if err := rows.Scan(&iv.ID, &iv.SubscriberID, &iv.Kind, &iv.DelegateTo, &iv.ValidFrom, &iv.ValidUntil, &iv.CreatedAt); err != nil {
			return nil, err
		}
		intervals = append(intervals, iv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ResolveFromIntervals(intervals, subscriberIDs, at), nil
}

func outranks(candidate, current Interval) bool {
	candidateRank, currentRank := priorities[candidate.Kind].rank, priorities[current.Kind].rank
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return candidate.ID > current.ID
}
