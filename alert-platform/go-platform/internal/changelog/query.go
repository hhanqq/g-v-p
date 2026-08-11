package changelog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// FieldSpec — один допустимый столбец low-code фильтра. Fields — карта
// с фиксированными ключами; field из запроса используется ТОЛЬКО как
// ключ этой карты, реальное имя столбца никогда не собирается из
// пользовательского ввода — это и есть защита от инъекции.
type FieldSpec struct {
	Column string   `json:"-"`
	Label  string   `json:"label"`
	Kind   string   `json:"kind"` // "string" | "datetime"
	Ops    []string `json:"ops"`
}

var stringOps = []string{"eq", "contains"}
var dateOps = []string{"gte", "lte"}

// Fields — единственный источник правды о допустимых полях поиска;
// GET /api/change-history/fields отдаёт его же, чтобы React строил
// дропдауны из того же списка, а не дублировал его вручную.
var Fields = map[string]FieldSpec{
	"occurred_at":   {Column: "occurred_at", Label: "Время", Kind: "datetime", Ops: dateOps},
	"actor":         {Column: "actor", Label: "Пользователь", Kind: "string", Ops: stringOps},
	"actor_role":    {Column: "actor_role", Label: "Роль", Kind: "string", Ops: stringOps},
	"action":        {Column: "action", Label: "Действие", Kind: "string", Ops: stringOps},
	"resource_type": {Column: "resource_type", Label: "Тип объекта", Kind: "string", Ops: stringOps},
	"resource_id":   {Column: "resource_id", Label: "ID объекта", Kind: "string", Ops: stringOps},
	"result":        {Column: "result", Label: "Результат", Kind: "string", Ops: stringOps},
}

type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

type SearchRequest struct {
	Match      string      `json:"match"` // "all" | "any"
	Conditions []Condition `json:"conditions"`
	Limit      int         `json:"limit"`
}

type SearchResult struct {
	EventID      string    `json:"event_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Actor        string    `json:"actor"`
	ActorRole    string    `json:"actor_role"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Result       string    `json:"result"`
	Detail       string    `json:"detail"`
	BeforeJSON   string    `json:"before_json"`
	AfterJSON    string    `json:"after_json"`
}

func containsOp(ops []string, op string) bool {
	for _, candidate := range ops {
		if candidate == op {
			return true
		}
	}
	return false
}

// buildQuery переводит проверенный фильтр в параметризованный
// ClickHouse-запрос с именованными параметрами (@p0, @p1, ...) —
// значения никогда не подставляются в текст запроса напрямую.
func buildQuery(req SearchRequest) (query string, args []any, err error) {
	match := "AND"
	if req.Match == "any" {
		match = "OR"
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	var clauses []string
	for i, cond := range req.Conditions {
		spec, ok := Fields[cond.Field]
		if !ok {
			return "", nil, fmt.Errorf("неизвестное поле %q", cond.Field)
		}
		if !containsOp(spec.Ops, cond.Op) {
			return "", nil, fmt.Errorf("недопустимый оператор %q для поля %q", cond.Op, cond.Field)
		}
		name := fmt.Sprintf("p%d", i)
		switch cond.Op {
		case "eq":
			clauses = append(clauses, fmt.Sprintf("%s = @%s", spec.Column, name))
			args = append(args, clickhouse.Named(name, cond.Value))
		case "contains":
			clauses = append(clauses, fmt.Sprintf("%s ILIKE @%s", spec.Column, name))
			args = append(args, clickhouse.Named(name, "%"+cond.Value+"%"))
		case "gte", "lte":
			parsed, perr := time.Parse(time.RFC3339, cond.Value)
			if perr != nil {
				return "", nil, fmt.Errorf("некорректная дата для поля %q, ожидается RFC3339: %w", cond.Field, perr)
			}
			operator := ">="
			if cond.Op == "lte" {
				operator = "<="
			}
			clauses = append(clauses, fmt.Sprintf("%s %s @%s", spec.Column, operator, name))
			args = append(args, clickhouse.Named(name, parsed))
		}
	}

	query = "SELECT event_id,occurred_at,actor,actor_role,action,resource_type,resource_id,result,detail,before_json,after_json FROM alerting.change_events"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " "+match+" ")
	}
	query += fmt.Sprintf(" ORDER BY occurred_at DESC LIMIT %d", limit)
	return query, args, nil
}

// Search выполняет валидированный low-code поиск по ClickHouse.
func Search(ctx context.Context, conn clickhouse.Conn, req SearchRequest) ([]SearchResult, error) {
	query, args, err := buildQuery(req)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]SearchResult, 0)
	for rows.Next() {
		var item SearchResult
		if err := rows.Scan(
			&item.EventID, &item.OccurredAt, &item.Actor, &item.ActorRole, &item.Action,
			&item.ResourceType, &item.ResourceID, &item.Result, &item.Detail, &item.BeforeJSON, &item.AfterJSON,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
