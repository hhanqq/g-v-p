package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FilterNode — единое представление условия фильтрации алертов: либо
// группа (Match + Conditions), либо лист (Field/Op/Value). И панель
// быстрых фильтров, и «Расширенный фильтр» (low-code конструктор)
// компилируются в одно и то же дерево и проходят через один и тот же
// compileFilterNode — умышленно нет двух параллельных систем фильтрации
// (ТЗ, раздел III: «очень важно не создавать две разные системы
// фильтрации»).
type FilterNode struct {
	Match      string       `json:"match,omitempty"`
	Conditions []FilterNode `json:"conditions,omitempty"`
	Field      string       `json:"field,omitempty"`
	Op         string       `json:"op,omitempty"`
	Value      any          `json:"value,omitempty"`
}

type filterFieldSpec struct {
	label string
	kind  string   // "enum" | "string" | "int"
	ops   []string // разрешённые операторы для этого поля
}

// alertFilterFields — allow-list полей, доступных и обычной панели
// фильтров, и Query Builder'у. Значения статусов/реакции берутся из
// фактической доменной модели (problems.status, acknowledged_at,
// scenario_run_steps), а не выдуманы для UI — см. CLAUDE.md.
var alertFilterFields = map[string]filterFieldSpec{
	"priority":       {label: "Приоритет", kind: "enum", ops: []string{"in"}},
	"status":         {label: "Статус", kind: "enum", ops: []string{"in"}},
	"source":         {label: "Источник", kind: "enum", ops: []string{"in"}},
	"symptom_class":  {label: "Тип события", kind: "string", ops: []string{"eq", "in"}},
	"object_id":      {label: "Оборудование", kind: "string", ops: []string{"eq", "in"}},
	"site":           {label: "Филиал", kind: "string", ops: []string{"eq", "in"}},
	"equipment_type": {label: "Категория оборудования", kind: "string", ops: []string{"eq", "in"}},
	"has_incident":   {label: "Входит в инцидент", kind: "bool", ops: []string{"eq"}},
	"incident_id":    {label: "Номер инцидента", kind: "int", ops: []string{"eq"}},
	"reaction":       {label: "Реакция", kind: "enum", ops: []string{"in"}},
	"sla_breached":   {label: "SLA нарушен", kind: "bool", ops: []string{"eq"}},
}

var alertStatusValues = map[string]string{
	"open":         "problem.status='OPEN'",
	"flapping":     "problem.status='FLAPPING'",
	"acknowledged": "(problem.acknowledged_at IS NOT NULL AND problem.status IN ('OPEN','FLAPPING'))",
	"resolved":     "problem.status='RESOLVED'",
	"closed":       "incident.closed_at IS NOT NULL",
}

var alertReactionValues = map[string]string{
	"acknowledged": "problem.acknowledged_at IS NOT NULL",
	"no_reaction":  "(problem.acknowledged_at IS NULL AND problem.status IN ('OPEN','FLAPPING'))",
	// Эскалация — та же трасса, что уже строит /api/analytics/scenarios:
	// прогон сценария на этой проблеме дошёл до второго notify-узла
	// (типично после ветки «нет» на ack_check).
	"escalated": `EXISTS (
		SELECT 1 FROM scenario_run_steps srs JOIN scenario_runs sr ON sr.id = srs.run_id
		WHERE sr.problem_id = problem.id AND srs.node_type = 'notify'
		GROUP BY sr.id HAVING count(*) > 1
	)`,
}

// alertFilterFrom — общий FROM/JOIN для всех запросов по алертам,
// используется и listAlerts, и BI-эндпоинтом алертов (Фаза BI), чтобы
// не разойтись в семантике джойнов.
const alertFilterFrom = ` FROM events event
	JOIN signals signal ON signal.id = event.signal_id
	LEFT JOIN problems problem ON problem.id = event.problem_id
	LEFT JOIN incidents incident ON incident.id = problem.incident_id
	LEFT JOIN cmdb_objects cmdb ON cmdb.id = event.object_id`

func compileFilterNode(node FilterNode, args *[]any) (string, error) {
	if len(node.Conditions) > 0 {
		match := node.Match
		if match == "" {
			match = "all"
		}
		joiner := " AND "
		if match == "any" {
			joiner = " OR "
		} else if match != "all" {
			return "", fmt.Errorf("недопустимый match: %s", match)
		}
		parts := make([]string, 0, len(node.Conditions))
		for _, child := range node.Conditions {
			part, err := compileFilterNode(child, args)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		return "(" + strings.Join(parts, joiner) + ")", nil
	}
	if node.Field == "" {
		return "", nil
	}
	return compileAlertCondition(node.Field, node.Op, node.Value, args)
}

func compileAlertCondition(field, op string, value any, args *[]any) (string, error) {
	spec, ok := alertFilterFields[field]
	if !ok {
		return "", fmt.Errorf("неизвестное поле фильтра: %s", field)
	}
	allowedOp := false
	for _, o := range spec.ops {
		if o == op {
			allowedOp = true
			break
		}
	}
	if !allowedOp {
		return "", fmt.Errorf("оператор %q недопустим для поля %q", op, field)
	}

	switch field {
	case "status":
		return compileEnumSetCondition(value, alertStatusValues)
	case "reaction":
		return compileEnumSetCondition(value, alertReactionValues)
	case "has_incident":
		want, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("has_incident требует boolean")
		}
		if want {
			return "problem.incident_id IS NOT NULL", nil
		}
		return "(problem.incident_id IS NULL)", nil
	case "sla_breached":
		want, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("sla_breached требует boolean")
		}
		if want {
			return "problem.id IN (SELECT problem_id FROM sla_breach_notices)", nil
		}
		return "problem.id NOT IN (SELECT problem_id FROM sla_breach_notices)", nil
	}

	column := map[string]string{
		"priority":       "problem.priority",
		"source":         "signal.source_system",
		"symptom_class":  "event.symptom_class",
		"object_id":      "event.object_id",
		"site":           "event.site",
		"equipment_type": "cmdb.equipment_type",
		"incident_id":    "problem.incident_id",
	}[field]

	switch op {
	case "eq":
		*args = append(*args, value)
		return fmt.Sprintf("%s = $%d", column, len(*args)), nil
	case "in":
		values, err := toStringSlice(value)
		if err != nil {
			return "", err
		}
		if len(values) == 0 {
			return "", nil
		}
		*args = append(*args, values)
		return fmt.Sprintf("%s = ANY($%d)", column, len(*args)), nil
	default:
		return "", fmt.Errorf("оператор %q не реализован для поля %q", op, field)
	}
}

// compileEnumSetCondition — для «производных» полей (статус, реакция),
// у которых значение — не колонка, а SQL-фрагмент из allow-list.
func compileEnumSetCondition(value any, known map[string]string) (string, error) {
	values, err := toStringSlice(value)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		fragment, ok := known[v]
		if !ok {
			return "", fmt.Errorf("недопустимое значение: %s", v)
		}
		parts = append(parts, fragment)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", nil
}

func toStringSlice(value any) ([]string, error) {
	switch v := value.(type) {
	case string:
		return []string{v}, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("значение должно быть строкой: %v", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("недопустимый тип значения: %T", value)
	}
}

// buildSimpleAlertFilter — панель быстрых фильтров и presets собирают
// свои плоские query-параметры прямо в FilterNode, тем самым проходя
// через тот же compileFilterNode, что и «Расширенный фильтр».
func buildSimpleAlertFilter(request *http.Request) FilterNode {
	query := request.URL.Query()
	conditions := make([]FilterNode, 0, 8)

	if values, ok := query["priority"]; ok && len(values) > 0 {
		conditions = append(conditions, FilterNode{Field: "priority", Op: "in", Value: values})
	}
	if values, ok := query["status"]; ok && len(values) > 0 {
		conditions = append(conditions, FilterNode{Field: "status", Op: "in", Value: values})
	}
	if values, ok := query["source"]; ok && len(values) > 0 {
		conditions = append(conditions, FilterNode{Field: "source", Op: "in", Value: values})
	}
	if v := query.Get("object_id"); v != "" {
		conditions = append(conditions, FilterNode{Field: "object_id", Op: "eq", Value: v})
	}
	if v := query.Get("site"); v != "" {
		conditions = append(conditions, FilterNode{Field: "site", Op: "eq", Value: v})
	}
	if v := query.Get("equipment_type"); v != "" {
		conditions = append(conditions, FilterNode{Field: "equipment_type", Op: "eq", Value: v})
	}
	if v := query.Get("has_incident"); v == "true" || v == "false" {
		conditions = append(conditions, FilterNode{Field: "has_incident", Op: "eq", Value: v == "true"})
	}
	if v := queryInt(request, "incident_id", 0); v > 0 {
		conditions = append(conditions, FilterNode{Field: "incident_id", Op: "eq", Value: v})
	}
	if values, ok := query["reaction"]; ok && len(values) > 0 {
		conditions = append(conditions, FilterNode{Field: "reaction", Op: "in", Value: values})
	}
	return FilterNode{Match: "all", Conditions: conditions}
}

// resolveAlertFilter — если передан ?filter=<json AST> (Query Builder),
// используем его как есть; иначе собираем AST из плоских параметров
// панели фильтров. Ровно одна точка входа для обоих путей UI.
func resolveAlertFilter(request *http.Request) (FilterNode, error) {
	if raw := request.URL.Query().Get("filter"); raw != "" {
		var node FilterNode
		if err := json.Unmarshal([]byte(raw), &node); err != nil {
			return FilterNode{}, fmt.Errorf("некорректный filter JSON: %w", err)
		}
		return node, nil
	}
	return buildSimpleAlertFilter(request), nil
}

// alertFilterOptions — GET /api/alerts/filter-options. Источники и
// приоритеты/статусы фиксированы доменной моделью, но список источников
// должен приходить с backend (реально зарегистрированные, а не
// захардкоженный Zabbix/SolarWinds), поэтому он — единственная часть
// динамического SQL-запроса здесь.
func (server *Server) alertFilterOptions(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	sources, err := distinctSourceSystems(request.Context(), server.pool)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"priorities": []string{"P0", "P1", "P2", "P3"},
		"sources":    sources,
		"statuses": []map[string]string{
			{"value": "open", "label": "Открыт"},
			{"value": "flapping", "label": "Нестабильно"},
			{"value": "acknowledged", "label": "Подтверждён"},
			{"value": "resolved", "label": "Восстановлен"},
			{"value": "closed", "label": "Закрыт"},
		},
		"reactions": []map[string]string{
			{"value": "acknowledged", "label": "Подтверждено"},
			{"value": "no_reaction", "label": "Без реакции"},
			{"value": "escalated", "label": "Эскалировано"},
		},
	})
}

func distinctSourceSystems(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT source_system FROM signals ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
