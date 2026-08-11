package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/coverage"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// routeEmployeeAvailability обрабатывает /api/employees/{id}/availability
// и вложенные под-ресурсы (произвольные интервалы, календарь, dry-run).
// Вынесено в отдельный диспетчер по образцу routeGroups/routeScenarios —
// набор под-путей здесь уже не укладывается в плоскую if-цепочку
// server.go без риска специфичный-suffix-после-общего-prefix (тот же
// урок, что уже дважды наступал).
func (server *Server) routeEmployeeAvailability(response http.ResponseWriter, request *http.Request, path string) bool {
	const prefix = "/api/employees/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	if len(segments) < 2 || segments[1] != "availability" {
		return false
	}
	employeeID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee id")
		return true
	}
	if len(segments) == 2 && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.AvailabilityManage, server.setEmployeeAvailability)
		return true
	}
	if len(segments) == 3 && segments[2] == "intervals" && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.AvailabilityManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.createAvailabilityInterval(w, r, employeeID, u)
		})
		return true
	}
	if len(segments) == 4 && segments[2] == "intervals" && request.Method == http.MethodDelete {
		intervalID, err := strconv.ParseInt(segments[3], 10, 64)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid interval id")
			return true
		}
		server.withPermission(response, request, rbac.AvailabilityManage, func(w http.ResponseWriter, r *http.Request, u map[string]any) {
			server.deleteAvailabilityInterval(w, r, employeeID, intervalID, u)
		})
		return true
	}
	if len(segments) == 3 && segments[2] == "calendar" && request.Method == http.MethodGet {
		server.withPermission(response, request, rbac.AvailabilityRead, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.employeeAvailabilityCalendar(w, r, employeeID)
		})
		return true
	}
	if len(segments) == 3 && segments[2] == "dry-run" && request.Method == http.MethodPost {
		server.withPermission(response, request, rbac.AvailabilityManage, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			server.employeeAvailabilityDryRun(w, r, employeeID)
		})
		return true
	}
	return false
}

type intervalRequest struct {
	Kind                   string  `json:"kind"`
	ValidFrom              string  `json:"valid_from"`
	ValidUntil             *string `json:"valid_until"`
	Note                   *string `json:"note"`
	DelegateToSubscriberID *int64  `json:"delegate_to_subscriber_id"`
	Recurrence             *struct {
		Weekdays []int  `json:"weekdays"` // 0=Sunday..6=Saturday (time.Weekday convention)
		Until    string `json:"until"`
	} `json:"recurrence"`
}

type occurrence struct {
	from time.Time
	to   *time.Time
}

func truncateToDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// expandOccurrences превращает один запрошенный интервал в список
// конкретных строк для вставки — один элемент без recurrence, иначе
// один на каждый совпадающий день недели между valid_from и
// recurrence.until (тот же диапазон времени суток, что у первого
// occurrence). Максимум 200 — защита от случайной опечатки в датах
// (например, until на 10 лет вперёд).
func expandOccurrences(validFrom time.Time, validUntil *time.Time, recurrence *struct {
	Weekdays []int  `json:"weekdays"`
	Until    string `json:"until"`
}) ([]occurrence, error) {
	if recurrence == nil {
		return []occurrence{{from: validFrom, to: validUntil}}, nil
	}
	if validUntil == nil {
		return nil, errors.New("recurrence requires valid_until on the first occurrence")
	}
	until, err := parseISO(recurrence.Until)
	if err != nil {
		return nil, errors.New("invalid recurrence.until")
	}
	duration := validUntil.Sub(validFrom)
	if duration <= 0 {
		return nil, errors.New("valid_until must be after valid_from")
	}
	weekdaySet := make(map[time.Weekday]bool, len(recurrence.Weekdays))
	for _, wd := range recurrence.Weekdays {
		weekdaySet[time.Weekday(wd)] = true
	}
	// Сравниваем только календарную дату, не время суток: day несёт то же
	// время суток, что и validFrom (например, 09:00), а until обычно
	// приходит с фронтенда как полночь этой даты — без усечения
	// последний день (until в 00:00 < day в 09:00 того же числа) молча
	// выпадал бы из диапазона, хотя пользователь явно включил его.
	untilDate := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, until.Location())
	var occurrences []occurrence
	for day := validFrom; !truncateToDate(day).After(untilDate); day = day.AddDate(0, 0, 1) {
		if weekdaySet[day.Weekday()] {
			end := day.Add(duration)
			occurrences = append(occurrences, occurrence{from: day, to: &end})
			if len(occurrences) > 200 {
				return nil, errors.New("recurrence produced too many occurrences (limit 200)")
			}
		}
	}
	if len(occurrences) == 0 {
		return nil, errors.New("recurrence produced no occurrences")
	}
	return occurrences, nil
}

// createAvailabilityInterval — календарное создание произвольного
// интервала (в т.ч. повторяющегося). В отличие от быстрого тумблера
// (setEmployeeAvailability, Фаза 3), НИЧЕГО не закрывает — интервалы
// намеренно сосуществуют и разрешаются приоритетом при чтении
// (internal/availability).
func (server *Server) createAvailabilityInterval(response http.ResponseWriter, request *http.Request, id int64, user map[string]any) {
	var payload intervalRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || !validAvailabilityKinds[payload.Kind] {
		writeError(response, http.StatusUnprocessableEntity, "invalid interval payload")
		return
	}
	if (payload.Kind == "delegation") != (payload.DelegateToSubscriberID != nil) {
		writeError(response, http.StatusUnprocessableEntity, "delegation requires delegate_to_subscriber_id, other kinds must not set it")
		return
	}
	validFrom, err := parseISO(payload.ValidFrom)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid valid_from")
		return
	}
	var validUntil *time.Time
	if payload.ValidUntil != nil {
		parsed, err := parseISO(*payload.ValidUntil)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid valid_until")
			return
		}
		validUntil = &parsed
	}
	occurrences, err := expandOccurrences(validFrom, validUntil, payload.Recurrence)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}

	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var username string
	if err := tx.QueryRow(ctx, `SELECT trueconf_username FROM subscribers WHERE id=$1 FOR UPDATE`, id).Scan(&username); errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Сотрудник не найден")
		return
	} else if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	now := time.Now().UTC()
	ids := make([]int64, 0, len(occurrences))
	for _, occ := range occurrences {
		var intervalID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO employee_availability(subscriber_id,kind,delegate_to_subscriber_id,valid_from,valid_until,source,note,created_at)
			VALUES($1,$2,$3,$4,$5,'manual',$6,$7) RETURNING id`,
			id, payload.Kind, payload.DelegateToSubscriberID, occ.from, occ.to, payload.Note, now,
		).Scan(&intervalID); err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		ids = append(ids, intervalID)
	}
	actor, _ := user["username"].(string)
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "subscriber.add_availability_interval",
		ResourceType: "subscriber", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{"kind": payload.Kind, "count": len(ids)},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "ids": ids})
}

// deleteAvailabilityInterval — только для ещё не начавшихся интервалов;
// прошедшее/текущее — история (append-only), редактируется новым
// override-интервалом поверх, не удалением.
func (server *Server) deleteAvailabilityInterval(response http.ResponseWriter, request *http.Request, id, intervalID int64, user map[string]any) {
	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var validFrom time.Time
	err = tx.QueryRow(ctx, `SELECT valid_from FROM employee_availability WHERE id=$1 AND subscriber_id=$2 FOR UPDATE`, intervalID, id).Scan(&validFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Интервал не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !validFrom.After(time.Now().UTC()) {
		writeError(response, http.StatusUnprocessableEntity, "нельзя удалить уже начавшийся или прошедший интервал — это история")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM employee_availability WHERE id=$1`, intervalID); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	actor, _ := user["username"].(string)
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: time.Now().UTC(), Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "subscriber.delete_availability_interval",
		ResourceType: "subscriber", ResourceID: strconv.FormatInt(id, 10),
		Before: map[string]any{"interval_id": intervalID},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

// employeeAvailabilityCalendar отдаёт и по-дневное резолвленное
// состояние (для закраски ячеек календаря — та же логика приоритета,
// что видит маршрутизация), и сырые интервалы, пересекающие диапазон
// (для списка/удаления в UI).
func (server *Server) employeeAvailabilityCalendar(response http.ResponseWriter, request *http.Request, id int64) {
	ctx := request.Context()
	query := request.URL.Query()
	from, err := parseISO(query.Get("from"))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid from")
		return
	}
	to, err := parseISO(query.Get("to"))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid to")
		return
	}
	if !to.After(from) || to.Sub(from) > 100*24*time.Hour {
		writeError(response, http.StatusUnprocessableEntity, "range must be positive and at most ~100 days")
		return
	}

	days := make([]map[string]any, 0)
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		statuses, err := availability.Resolve(ctx, server.pool, []int64{id}, day.Add(12*time.Hour))
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		status := statuses[id]
		days = append(days, map[string]any{"date": day.Format("2006-01-02"), "available": status.Available, "kind": status.Kind})
	}

	rows, err := server.pool.Query(ctx, `
		SELECT id,kind,valid_from,valid_until,delegate_to_subscriber_id,note
		FROM employee_availability
		WHERE subscriber_id=$1 AND valid_from < $3 AND (valid_until IS NULL OR valid_until > $2)
		ORDER BY valid_from`, id, from, to)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	intervals := make([]map[string]any, 0)
	for rows.Next() {
		var intervalID int64
		var kind string
		var validFrom time.Time
		var validUntil sql.NullTime
		var delegateTo sql.NullInt64
		var note sql.NullString
		if err := rows.Scan(&intervalID, &kind, &validFrom, &validUntil, &delegateTo, &note); err != nil {
			writeError(response, http.StatusInternalServerError, "scan availability intervals")
			return
		}
		intervals = append(intervals, map[string]any{
			"id": intervalID, "kind": kind, "valid_from": formatISO(validFrom), "valid_until": nullableISO(validUntil),
			"delegate_to_subscriber_id": nullableInt64(delegateTo), "note": nullableString(note),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load availability intervals")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"days": days, "intervals": intervals})
}

type dryRunRequest struct {
	Kind       string  `json:"kind"`
	ValidFrom  string  `json:"valid_from"`
	ValidUntil *string `json:"valid_until"`
}

// employeeAvailabilityDryRun — «что если сохранить этот интервал».
// Никогда не блокирует (всегда 200): реальные HR-решения (одобренный
// отпуск) не должны упираться в инструмент планирования, только
// предупреждать. Пока нет ни одной политики покрытия (следующий этап)
// — честно пустой список предупреждений, не ошибка и не выдумка.
func (server *Server) employeeAvailabilityDryRun(response http.ResponseWriter, request *http.Request, id int64) {
	var payload dryRunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || !validAvailabilityKinds[payload.Kind] {
		writeError(response, http.StatusUnprocessableEntity, "invalid dry-run payload")
		return
	}
	validFrom, err := parseISO(payload.ValidFrom)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid valid_from")
		return
	}
	var validUntil *time.Time
	if payload.ValidUntil != nil {
		parsed, err := parseISO(*payload.ValidUntil)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid valid_until")
			return
		}
		validUntil = &parsed
	}
	ctx := request.Context()
	// Окно проверки — сам кандидат-интервал; бессрочный кандидат
	// проверяется на разумный горизонт вперёд (90 дней), а не вечность —
	// покрытие дальше всё равно можно пересчитать позже.
	sweepTo := validFrom.Add(90 * 24 * time.Hour)
	if validUntil != nil {
		sweepTo = *validUntil
	}
	rows, err := server.pool.Query(ctx, `
		SELECT policy.id, policy.name, policy.group_id, policy.min_available
		FROM coverage_policies policy
		JOIN group_members member ON member.group_id = policy.group_id
		WHERE policy.active=TRUE AND member.subscriber_id=$1`, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	type policyRow struct {
		id, groupID  int64
		name         string
		minAvailable int
	}
	var policies []policyRow
	for rows.Next() {
		var p policyRow
		if err := rows.Scan(&p.id, &p.name, &p.groupID, &p.minAvailable); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan coverage policies")
			return
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load coverage policies")
		return
	}
	rows.Close()

	warnings := make([]string, 0)
	candidate := &coverage.CandidateInterval{SubscriberID: id, Kind: payload.Kind, ValidFrom: validFrom, ValidUntil: validUntil}
	for _, p := range policies {
		gaps, err := coverage.Sweep(ctx, server.pool, p.groupID, validFrom, sweepTo, p.minAvailable, candidate)
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		if len(gaps) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"нарушает покрытие «%s» (минимум %d) — начиная с %s",
				p.name, p.minAvailable, gaps[0].From.Format("2006-01-02 15:04"),
			))
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"warnings": warnings})
}
