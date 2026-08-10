// Package coverage находит окна времени, где у критичного объекта/
// группы меньше дежурных, чем требует политика — и для реального
// диапазона дат (раздел «Покрытие»), и гипотетически, «что если»
// (dry-run на создание интервала доступности, раздел «Календарь»).
// Оба сценария обслуживает одна функция Sweep: gap-обнаружение
// вычисляется по требованию (Go sweep), не materialized view — dry-run
// проверяет ещё не сохранённый кандидат-интервал, а MV физически не
// может обслужить гипотезу над несуществующей строкой.
package coverage

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
)

type dbTx interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Gap — непрерывное окно, где доступных дежурных меньше минимума.
// MinAvailable — наименьшее число доступных, наблюдённое внутри окна
// (не обязательно ноль — просто ниже требуемого минимума).
type Gap struct {
	From         time.Time
	To           time.Time
	MinAvailable int
}

// CandidateInterval — ещё не сохранённый интервал, который нужно
// проверить «как если бы» он уже существовал — ровно то, что нужно
// dry-run на создание интервала (Фаза 7) для честного ответа на
// «а не создаст ли это пробел в покрытии».
type CandidateInterval struct {
	SubscriberID int64
	Kind         string
	DelegateTo   *int64
	ValidFrom    time.Time
	ValidUntil   *time.Time
}

// Sweep находит окна внутри [from,to), где число доступных участников
// группы groupID опускается ниже minAvailable. candidate, если задан,
// подмешивается к реальным интервалам как ещё одна строка — тем самым
// не требуя отдельного пути для "как есть сейчас" и "что если".
func Sweep(ctx context.Context, dbc dbTx, groupID int64, from, to time.Time, minAvailable int, candidate *CandidateInterval) ([]Gap, error) {
	memberRows, err := dbc.Query(ctx, `SELECT subscriber_id FROM group_members WHERE group_id=$1`, groupID)
	if err != nil {
		return nil, err
	}
	var memberIDs []int64
	for memberRows.Next() {
		var id int64
		if err := memberRows.Scan(&id); err != nil {
			memberRows.Close()
			return nil, err
		}
		memberIDs = append(memberIDs, id)
	}
	if err := memberRows.Err(); err != nil {
		memberRows.Close()
		return nil, err
	}
	memberRows.Close()
	if len(memberIDs) == 0 {
		return nil, nil
	}

	intervalRows, err := dbc.Query(ctx, `
		SELECT id, subscriber_id, kind, delegate_to_subscriber_id, valid_from, valid_until, created_at
		FROM employee_availability
		WHERE subscriber_id = ANY($1) AND valid_from < $3 AND (valid_until IS NULL OR valid_until > $2)`,
		memberIDs, from, to)
	if err != nil {
		return nil, err
	}
	var intervals []availability.Interval
	for intervalRows.Next() {
		var iv availability.Interval
		if err := intervalRows.Scan(&iv.ID, &iv.SubscriberID, &iv.Kind, &iv.DelegateTo, &iv.ValidFrom, &iv.ValidUntil, &iv.CreatedAt); err != nil {
			intervalRows.Close()
			return nil, err
		}
		intervals = append(intervals, iv)
	}
	if err := intervalRows.Err(); err != nil {
		intervalRows.Close()
		return nil, err
	}
	intervalRows.Close()

	if candidate != nil {
		intervals = append(intervals, availability.Interval{
			ID: -1, SubscriberID: candidate.SubscriberID, Kind: candidate.Kind, DelegateTo: candidate.DelegateTo,
			ValidFrom: candidate.ValidFrom, ValidUntil: candidate.ValidUntil, CreatedAt: time.Now().UTC(),
		})
	}

	breakpoints := sweepBreakpoints(intervals, from, to)
	var gaps []Gap
	var open *Gap
	for i := 0; i < len(breakpoints)-1; i++ {
		at := breakpoints[i]
		statuses := availability.ResolveFromIntervals(intervals, memberIDs, at)
		available := 0
		for _, status := range statuses {
			if status.Available {
				available++
			}
		}
		if available < minAvailable {
			if open == nil {
				open = &Gap{From: at, MinAvailable: available}
			} else if available < open.MinAvailable {
				open.MinAvailable = available
			}
			open.To = breakpoints[i+1]
		} else if open != nil {
			gaps = append(gaps, *open)
			open = nil
		}
	}
	if open != nil {
		gaps = append(gaps, *open)
	}
	return gaps, nil
}

// sweepBreakpoints — отсортированные уникальные моменты, где состояние
// покрытия могло измениться: границы диапазона плюс каждая граница
// интервала, попадающая строго внутрь диапазона.
func sweepBreakpoints(intervals []availability.Interval, from, to time.Time) []time.Time {
	set := map[time.Time]bool{from: true, to: true}
	for _, iv := range intervals {
		if iv.ValidFrom.After(from) && iv.ValidFrom.Before(to) {
			set[iv.ValidFrom] = true
		}
		if iv.ValidUntil != nil && iv.ValidUntil.After(from) && iv.ValidUntil.Before(to) {
			set[*iv.ValidUntil] = true
		}
	}
	points := make([]time.Time, 0, len(set))
	for t := range set {
		points = append(points, t)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	return points
}
