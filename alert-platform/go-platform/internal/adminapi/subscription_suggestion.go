package adminapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Раздел «Использование ИИ»: умная маршрутизация на основе истории.
// Не выдуманная логика — реальный запрос к истории существующих
// подписок коллег из тех же групп. Ollama (если доступна) только
// формулирует объяснение по этим готовым фактам; при отказе/таймауте —
// шаблонная фраза с теми же фактами, раздел И5.

type SubscriptionSuggestion struct {
	Subsidiary        *string `json:"subsidiary"`
	ServiceID         *string `json:"service_id"`
	PriorityThreshold *string `json:"priority_threshold"`
	PeerCount         int     `json:"peer_count"`
	Explanation       string  `json:"explanation"`
}

// suggestSubscription ищет самый частый паттерн подписки среди коллег
// сотрудника (участников тех же групп) и предлагает его. Возвращает
// nil, nil, если данных недостаточно (сотрудник не в группе, у коллег
// нет подписок) ИЛИ у самого сотрудника уже есть хоть одна подписка —
// рекомендация только тем, кому реально нужна, не гадаем и не
// навязываем поверх уже сделанного выбора.
func (server *Server) suggestSubscription(ctx context.Context, subscriberID int64) (*SubscriptionSuggestion, error) {
	var alreadySubscribed bool
	if err := server.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE subscriber_id = $1)`, subscriberID).Scan(&alreadySubscribed); err != nil {
		return nil, err
	}
	if alreadySubscribed {
		return nil, nil
	}

	var subsidiary, serviceID, priority sql.NullString
	var peerCount int
	err := server.pool.QueryRow(ctx, `
		SELECT s.subsidiary, s.service_id, s.priority_threshold, count(*) AS cnt
		FROM subscriptions s
		JOIN group_members gm ON gm.subscriber_id = s.subscriber_id
		WHERE gm.group_id IN (SELECT group_id FROM group_members WHERE subscriber_id = $1)
		  AND s.subscriber_id <> $1
		GROUP BY s.subsidiary, s.service_id, s.priority_threshold
		ORDER BY cnt DESC
		LIMIT 1`, subscriberID).Scan(&subsidiary, &serviceID, &priority, &peerCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	suggestion := &SubscriptionSuggestion{
		Subsidiary: nullableString(subsidiary), ServiceID: nullableString(serviceID),
		PriorityThreshold: nullableString(priority), PeerCount: peerCount,
	}
	suggestion.Explanation = fallbackSuggestionExplanation(*suggestion)
	if server.ollama != nil {
		if text := server.ollama.Ask(ctx, buildSuggestionPrompt(*suggestion), 50); text != nil {
			suggestion.Explanation = *text
		}
	}
	return suggestion, nil
}

// Три метода ниже — только для html/template в личном кабинете
// (personal.go): скрытые поля формы «Подписаться по рекомендации»
// нужны как обычные строки, а не *string, чтобы шаблон не печатал
// literal "<nil>" в значение поля при отсутствующем условии (means
// "любой" — то же самое, что оставить поле формы пустым).
func (s *SubscriptionSuggestion) SubsidiaryValue() string { return derefOrEmpty(s.Subsidiary) }
func (s *SubscriptionSuggestion) ServiceIDValue() string  { return derefOrEmpty(s.ServiceID) }
func (s *SubscriptionSuggestion) PriorityValue() string   { return derefOrEmpty(s.PriorityThreshold) }

func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func fallbackSuggestionExplanation(s SubscriptionSuggestion) string {
	noun, verb := "коллег", "подписаны"
	if s.PeerCount == 1 {
		noun, verb = "коллега", "подписан"
	}
	return fmt.Sprintf(
		"%d %s из ваших групп %s на похожий набор условий — вероятно, он подойдёт и вам.",
		s.PeerCount, noun, verb,
	)
}

func buildSuggestionPrompt(s SubscriptionSuggestion) string {
	subsidiary := "любой филиал"
	if s.Subsidiary != nil {
		subsidiary = "филиал " + *s.Subsidiary
	}
	service := "любой сервис"
	if s.ServiceID != nil {
		service = "сервис " + *s.ServiceID
	}
	priority := "любой приоритет"
	if s.PriorityThreshold != nil {
		priority = "приоритет ≤ " + *s.PriorityThreshold
	}
	return fmt.Sprintf(
		"Платформа мониторинга промышленного предприятия. У нового сотрудника нет ни одной подписки на уведомления. "+
			"%d сотрудников из его рабочих групп уже подписаны на: %s, %s, %s.\n\n"+
			"Сформулируй для этого сотрудника одну короткую дружелюбную фразу (1 предложение, без списка и заголовка), "+
			"рекомендующую подписаться на эти же условия — используй ТОЛЬКО факты выше, ничего не добавляй от себя.",
		s.PeerCount, subsidiary, service, priority,
	)
}

func (server *Server) employeeSubscriptionSuggestion(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	path := strings.TrimSuffix(normalizePath(request.URL.Path), "/subscription-suggestion")
	id, ok := pathInt(path, "/api/employees/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee id")
		return
	}
	suggestion, err := server.suggestSubscription(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if suggestion == nil {
		writeJSON(response, http.StatusOK, nil)
		return
	}
	writeJSON(response, http.StatusOK, suggestion)
}
