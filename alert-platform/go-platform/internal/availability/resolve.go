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

type intervalRow struct {
	id         int64
	kind       string
	delegateTo *int64
	createdAt  time.Time
}

// Resolve возвращает доступность каждого из subscriberIDs на момент at.
// Сотрудник без единого покрывающего интервала считается доступным —
// это явно сохраняет сегодняшнее неявное поведение как базовый случай,
// а не молчаливую дыру в данных.
func Resolve(ctx context.Context, dbc dbTx, subscriberIDs []int64, at time.Time) (map[int64]Status, error) {
	result := make(map[int64]Status, len(subscriberIDs))
	for _, id := range subscriberIDs {
		result[id] = Status{Available: true}
	}
	if len(subscriberIDs) == 0 {
		return result, nil
	}
	rows, err := dbc.Query(ctx, `
		SELECT id, subscriber_id, kind, delegate_to_subscriber_id, created_at
		FROM employee_availability
		WHERE subscriber_id = ANY($1) AND valid_from <= $2 AND (valid_until IS NULL OR valid_until > $2)`,
		subscriberIDs, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	best := make(map[int64]intervalRow)
	for rows.Next() {
		var subscriberID int64
		var candidate intervalRow
		if err := rows.Scan(&candidate.id, &subscriberID, &candidate.kind, &candidate.delegateTo, &candidate.createdAt); err != nil {
			return nil, err
		}
		if current, exists := best[subscriberID]; !exists || outranks(candidate, current) {
			best[subscriberID] = candidate
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for subscriberID, candidate := range best {
		rule, ok := priorities[candidate.kind]
		if !ok {
			continue
		}
		result[subscriberID] = Status{Available: rule.available, Kind: candidate.kind, DelegateTo: candidate.delegateTo}
	}
	return result, nil
}

func outranks(candidate, current intervalRow) bool {
	candidateRank, currentRank := priorities[candidate.kind].rank, priorities[current.kind].rank
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	if !candidate.createdAt.Equal(current.createdAt) {
		return candidate.createdAt.After(current.createdAt)
	}
	return candidate.id > current.id
}
