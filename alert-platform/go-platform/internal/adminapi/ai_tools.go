package adminapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/coverage"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// Package-level design note (раздел «ADP AI» доп. ТЗ): LLM не имеет
// прямого доступа к БД. Поток — User → intent/tool selection (Ollama,
// aiSelectTool в ai_routes.go) → Allowed tool (реестр ниже) → Permission
// check → Application Use Case → Repository. Каждый tool здесь — тонкая
// обёртка над уже существующими запросами (equipmentResponsibleGroups,
// FilterNode, availability.Resolve, coverage.Sweep, loadNoiseFunnel) —
// не параллельный движок запросов. Итоговый текст ответа собирается
// шаблонно из структурных данных tool'а, не LLM-парафразом: это и есть
// защита от галлюцинаций для MVP (LLM выбирает ЧТО спросить, не
// придумывает, ЧТО ответить).

var errAIPermissionDenied = errors.New("permission denied")

type aiEntityRef struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type aiToolResult struct {
	Summary  string        `json:"summary"`
	Data     any           `json:"data"`
	Entities []aiEntityRef `json:"entities,omitempty"`
	// Navigate — заполняется только open_entity: структурная команда
	// навигации, маршрут строит фронтенд, не LLM-сгенерированный URL.
	Navigate *aiEntityRef `json:"navigate,omitempty"`
}

type aiToolExecFunc func(ctx context.Context, server *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error)

type aiTool struct {
	Name         string
	Description  string
	ParamsHint   string
	ActionType   string // "read" | "navigate"
	Permission   rbac.Permission
	ResourceType string
	Execute      aiToolExecFunc
}

var aiToolRegistry = []aiTool{
	{
		Name:        "list_active_incidents",
		Description: "Список открытых инцидентов (closed_at IS NULL), опционально по приоритету",
		ParamsHint:  `{"priority": "P0"|"P1"|"P2"|"P3" (опционально), "limit": число (опционально, по умолчанию 10)}`,
		ActionType:  "read", Permission: rbac.IncidentsRead, ResourceType: "incident",
		Execute: aiListActiveIncidents,
	},
	{
		Name:        "get_incident",
		Description: "Детали одного инцидента по id",
		ParamsHint:  `{"incident_id": число}`,
		ActionType:  "read", Permission: rbac.IncidentsRead, ResourceType: "incident",
		Execute: aiGetIncident,
	},
	{
		Name:        "find_alerts",
		Description: "Поиск алертов по приоритету/статусу/филиалу/типу события",
		ParamsHint:  `{"priority": "P0".., "status": "open"|"acknowledged"|"resolved"|"closed", "site": строка, "symptom_class": строка, "limit": число}`,
		ActionType:  "read", Permission: rbac.AlertsRead, ResourceType: "alert",
		Execute: aiFindAlerts,
	},
	{
		Name:        "find_equipment",
		Description: "Поиск оборудования по названию/id/филиалу",
		ParamsHint:  `{"query": строка, "site": строка (опционально), "limit": число}`,
		ActionType:  "read", Permission: rbac.EquipmentRead, ResourceType: "cmdb_object",
		Execute: aiFindEquipment,
	},
	{
		Name:        "get_available_responders",
		Description: "Кто сейчас доступен для реакции на инцидент — по группам, ответственным за оборудование первопричины",
		ParamsHint:  `{"incident_id": число}`,
		ActionType:  "read", Permission: rbac.EmployeesRead, ResourceType: "subscriber",
		Execute: aiGetAvailableResponders,
	},
	{
		Name:        "get_coverage",
		Description: "Разрывы покрытия (недостаточно доступных дежурных) по активным политикам",
		ParamsHint:  `{}`,
		ActionType:  "read", Permission: rbac.CoverageRead, ResourceType: "coverage_policy",
		Execute: aiGetCoverage,
	},
	{
		Name:        "get_analytics",
		Description: "Сводная аналитика за последние 7 дней: воронка снижения шума, MTTA/MTTR, ack rate",
		ParamsHint:  `{}`,
		ActionType:  "read", Permission: rbac.AnalyticsRead, ResourceType: "analytics",
		Execute: aiGetAnalytics,
	},
	{
		Name:        "open_entity",
		Description: "Структурная навигация на карточку сущности (не URL, тип+id — маршрут строит фронтенд)",
		ParamsHint:  `{"entity_type": "incident"|"equipment"|"employee"|"alert", "entity_id": строка}`,
		ActionType:  "navigate", ResourceType: "",
		Execute: aiOpenEntity,
	},
}

func findAITool(name string) (aiTool, bool) {
	for _, tool := range aiToolRegistry {
		if tool.Name == name {
			return tool, true
		}
	}
	return aiTool{}, false
}

func paramString(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func paramIntDefault(params map[string]any, key string, def int) int {
	switch value := params[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case string:
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return def
}

func aiClampLimit(limit, max int) int {
	if limit <= 0 {
		return max
	}
	if limit > max {
		return max
	}
	return limit
}

func aiListActiveIncidents(ctx context.Context, server *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error) {
	priority := paramString(params, "priority")
	limit := aiClampLimit(paramIntDefault(params, "limit", 10), 20)
	where := []string{"incident.closed_at IS NULL"}
	args := make([]any, 0, 3)
	if priority != "" {
		args = append(args, priority)
		where = append(where, fmt.Sprintf("incident.priority = $%d", len(args)))
	}
	if grant.HasScope(rbac.ScopeSite) {
		args = append(args, grant.ScopeValues(rbac.ScopeSite))
		where = append(where, fmt.Sprintf("root.site = ANY($%d)", len(args)))
	}
	args = append(args, limit)
	rows, err := server.pool.Query(ctx, `
		SELECT incident.id, incident.priority, incident.opened_at, root.object_id, root.symptom_class, root.site
		FROM incidents incident
		LEFT JOIN problems root ON root.id = incident.root_problem_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY incident.opened_at DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type incidentRow struct {
		ID           int64   `json:"id"`
		Priority     *string `json:"priority"`
		OpenedAt     string  `json:"opened_at"`
		ObjectID     *string `json:"object_id"`
		SymptomClass *string `json:"symptom_class"`
		Site         *string `json:"site"`
	}
	items := make([]incidentRow, 0)
	entities := make([]aiEntityRef, 0)
	for rows.Next() {
		var id int64
		var priorityValue, objectID, symptomClass, site sql.NullString
		var openedAt time.Time
		if err := rows.Scan(&id, &priorityValue, &openedAt, &objectID, &symptomClass, &site); err != nil {
			return nil, err
		}
		items = append(items, incidentRow{
			ID: id, Priority: nullableString(priorityValue), OpenedAt: formatISO(openedAt),
			ObjectID: nullableString(objectID), SymptomClass: nullableString(symptomClass), Site: nullableString(site),
		})
		label := fmt.Sprintf("INC-%04d", id)
		if objectID.Valid {
			label += " · " + objectID.String
		}
		entities = append(entities, aiEntityRef{Type: "incident", ID: strconv.FormatInt(id, 10), Label: label})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("Активных инцидентов: %d", len(items))
	if len(items) == 0 {
		summary = "Активных инцидентов не найдено" + priorityQualifier(priority)
	}
	return &aiToolResult{Summary: summary, Data: items, Entities: entities}, nil
}

func priorityQualifier(priority string) string {
	if priority == "" {
		return ""
	}
	return " с приоритетом " + priority
}

func aiGetIncident(ctx context.Context, server *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error) {
	id := int64(paramIntDefault(params, "incident_id", 0))
	if id <= 0 {
		return nil, fmt.Errorf("incident_id обязателен")
	}
	var priority, objectID, symptomClass, site sql.NullString
	var openedAt time.Time
	var closedAt sql.NullTime
	err := server.pool.QueryRow(ctx, `
		SELECT incident.priority, incident.opened_at, incident.closed_at, root.object_id, root.symptom_class, root.site
		FROM incidents incident LEFT JOIN problems root ON root.id = incident.root_problem_id
		WHERE incident.id=$1`, id,
	).Scan(&priority, &openedAt, &closedAt, &objectID, &symptomClass, &site)
	if errors.Is(err, pgx.ErrNoRows) {
		return &aiToolResult{Summary: fmt.Sprintf("Инцидент INC-%04d не найден", id)}, nil
	}
	if err != nil {
		return nil, err
	}
	if grant.HasScope(rbac.ScopeSite) && site.Valid && !grant.AllowsSite(site.String) {
		return &aiToolResult{Summary: fmt.Sprintf("Инцидент INC-%04d не найден", id)}, nil
	}
	status := "открыт"
	if closedAt.Valid {
		status = "закрыт " + formatISO(closedAt.Time)
	}
	data := map[string]any{
		"id": id, "priority": nullableString(priority), "opened_at": formatISO(openedAt),
		"closed_at": nullableISO(closedAt), "object_id": nullableString(objectID),
		"symptom_class": nullableString(symptomClass), "site": nullableString(site),
	}
	summary := fmt.Sprintf("INC-%04d (%s) — %s", id, valueOrDash(nullableString(priority)), status)
	if objectID.Valid {
		summary += ", объект " + objectID.String
	}
	entity := aiEntityRef{Type: "incident", ID: strconv.FormatInt(id, 10), Label: fmt.Sprintf("INC-%04d", id)}
	return &aiToolResult{Summary: summary, Data: data, Entities: []aiEntityRef{entity}}, nil
}

func valueOrDash(value *string) string {
	if value == nil {
		return "—"
	}
	return *value
}

// aiAlertFilterConditions строит FilterNode-условия из параметров tool'а
// и, если у пользователя задан scope по филиалу, сужает ими же — прежде
// чем запрос вообще уйдёт в БД (раздел «ADP AI + scope» доп. ТЗ: scope
// проверяется в бэкенде, не в модели). inScope=false означает «запрошенный
// site вне зоны доступа», не ошибку — вызывающий код должен вернуть
// пустой результат без похода в БД.
func aiAlertFilterConditions(params map[string]any, grant rbac.Grant) (conditions []FilterNode, inScope bool) {
	conditions = make([]FilterNode, 0, 4)
	if priority := paramString(params, "priority"); priority != "" {
		conditions = append(conditions, FilterNode{Field: "priority", Op: "in", Value: []string{priority}})
	}
	if status := paramString(params, "status"); status != "" {
		conditions = append(conditions, FilterNode{Field: "status", Op: "in", Value: []string{status}})
	}
	if symptomClass := paramString(params, "symptom_class"); symptomClass != "" {
		conditions = append(conditions, FilterNode{Field: "symptom_class", Op: "eq", Value: symptomClass})
	}
	site := paramString(params, "site")
	if site != "" {
		conditions = append(conditions, FilterNode{Field: "site", Op: "eq", Value: site})
	}
	if !grant.HasScope(rbac.ScopeSite) {
		return conditions, true
	}
	allowedSites := grant.ScopeValues(rbac.ScopeSite)
	if site == "" {
		conditions = append(conditions, FilterNode{Field: "site", Op: "in", Value: allowedSites})
		return conditions, true
	}
	return conditions, grant.AllowsSite(site)
}

func aiFindAlerts(ctx context.Context, server *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error) {
	conditions, inScope := aiAlertFilterConditions(params, grant)
	if !inScope {
		return &aiToolResult{Summary: "Алертов не найдено (вне вашей зоны доступа)"}, nil
	}
	args := make([]any, 0, 8)
	condition, err := compileFilterNode(FilterNode{Match: "all", Conditions: conditions}, &args)
	if err != nil {
		return nil, err
	}
	where := ""
	if condition != "" {
		where = " WHERE " + condition
	}
	limit := aiClampLimit(paramIntDefault(params, "limit", 10), 20)
	args = append(args, limit)
	query := `SELECT event.id, event.symptom_class, event.site, event.object_id, event.occurred_at, problem.priority, problem.status` +
		alertFilterFrom + where + fmt.Sprintf(" ORDER BY event.id DESC LIMIT $%d", len(args))
	rows, err := server.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	entities := make([]aiEntityRef, 0)
	for rows.Next() {
		var id int64
		var symptomClass, eventSite string
		var objectID, priority, status sql.NullString
		var occurredAt time.Time
		if err := rows.Scan(&id, &symptomClass, &eventSite, &objectID, &occurredAt, &priority, &status); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "symptom_class": symptomClass, "site": eventSite, "object_id": nullableString(objectID),
			"occurred_at": formatISO(occurredAt), "priority": nullableString(priority), "status": nullableString(status),
		})
		label := symptomClass
		if objectID.Valid {
			label += " · " + objectID.String
		}
		entities = append(entities, aiEntityRef{Type: "alert", ID: strconv.FormatInt(id, 10), Label: label})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &aiToolResult{Summary: fmt.Sprintf("Найдено алертов: %d", len(items)), Data: items, Entities: entities}, nil
}

func aiFindEquipment(ctx context.Context, server *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error) {
	query := paramString(params, "query")
	site := paramString(params, "site")
	limit := aiClampLimit(paramIntDefault(params, "limit", 10), 20)
	where := []string{"TRUE"}
	args := make([]any, 0, 3)
	if query != "" {
		args = append(args, "%"+query+"%")
		where = append(where, fmt.Sprintf("(id ILIKE $%d OR name ILIKE $%d)", len(args), len(args)))
	}
	if site != "" {
		args = append(args, site)
		where = append(where, fmt.Sprintf("site = $%d", len(args)))
	} else if grant.HasScope(rbac.ScopeSite) {
		args = append(args, grant.ScopeValues(rbac.ScopeSite))
		where = append(where, fmt.Sprintf("site = ANY($%d)", len(args)))
	}
	args = append(args, limit)
	rows, err := server.pool.Query(ctx, `
		SELECT id, name, site, equipment_type FROM cmdb_objects
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY name LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	entities := make([]aiEntityRef, 0)
	for rows.Next() {
		var id, name string
		var objSite, equipmentType sql.NullString
		if err := rows.Scan(&id, &name, &objSite, &equipmentType); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "site": nullableString(objSite), "equipment_type": nullableString(equipmentType),
		})
		entities = append(entities, aiEntityRef{Type: "equipment", ID: id, Label: name})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &aiToolResult{Summary: fmt.Sprintf("Найдено объектов оборудования: %d", len(items)), Data: items, Entities: entities}, nil
}

// aiGetAvailableResponders — «кто сейчас доступен для этого инцидента»:
// та же обратная связь group_equipment_scope, что и карточка оборудования
// (equipmentResponsibleGroups), плюс availability.Resolve — тот же движок,
// что и раздел «Покрытие». Не отдельный расчёт.
// aiIncidentResponsibleMembers — раздел ADP AI: обратная связь
// equipmentResponsibleGroups (та же, что карточка оборудования) от
// инцидента к его ответственным группам, объединённым по subscriber_id
// (один человек может входить в несколько групп сразу). deniedOrEmpty
// непустой означает «вернуть этот текст пользователю», не ошибку.
func aiIncidentResponsibleMembers(ctx context.Context, server *Server, grant rbac.Grant, incidentID int64) (members map[int64]map[string]any, deniedOrEmpty string, err error) {
	var objectID, site sql.NullString
	err = server.pool.QueryRow(ctx, `
		SELECT root.object_id, root.site FROM incidents incident
		LEFT JOIN problems root ON root.id = incident.root_problem_id
		WHERE incident.id=$1`, incidentID,
	).Scan(&objectID, &site)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Sprintf("Инцидент INC-%04d не найден", incidentID), nil
	}
	if err != nil {
		return nil, "", err
	}
	if !objectID.Valid {
		return nil, "У инцидента не определён объект — невозможно определить ответственных", nil
	}
	if grant.HasScope(rbac.ScopeSite) && site.Valid && !grant.AllowsSite(site.String) {
		return nil, fmt.Sprintf("Инцидент INC-%04d не найден", incidentID), nil
	}
	groups, err := server.equipmentResponsibleGroups(ctx, objectID.String)
	if err != nil {
		return nil, "", err
	}
	if len(groups) == 0 {
		return nil, "Ни одна группа не отвечает за это оборудование", nil
	}
	memberByID := make(map[int64]map[string]any)
	for _, group := range groups {
		groupID, _ := group["id"].(int64)
		members, err := server.loadGroupMembers(ctx, groupID)
		if err != nil {
			return nil, "", err
		}
		for _, member := range members {
			id, _ := member["subscriber_id"].(int64)
			memberByID[id] = member
		}
	}
	return memberByID, "", nil
}

func aiGetAvailableResponders(ctx context.Context, server *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error) {
	incidentID := int64(paramIntDefault(params, "incident_id", 0))
	if incidentID <= 0 {
		return nil, fmt.Errorf("incident_id обязателен")
	}
	memberByID, message, err := aiIncidentResponsibleMembers(ctx, server, grant, incidentID)
	if err != nil {
		return nil, err
	}
	if message != "" {
		return &aiToolResult{Summary: message}, nil
	}
	subscriberIDs := make([]int64, 0, len(memberByID))
	for id := range memberByID {
		subscriberIDs = append(subscriberIDs, id)
	}
	statuses, err := availability.Resolve(ctx, server.pool, subscriberIDs, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(memberByID))
	for id, member := range memberByID {
		status := statuses[id]
		items = append(items, map[string]any{
			"trueconf_username": member["trueconf_username"], "full_name": member["full_name"],
			"available": status.Available, "availability_kind": availabilityBucket(status),
		})
	}
	available := 0
	for _, item := range items {
		if item["available"] == true {
			available++
		}
	}
	summary := fmt.Sprintf("Ответственных %d, сейчас доступно %d", len(items), available)
	return &aiToolResult{Summary: summary, Data: items}, nil
}

func aiGetCoverage(ctx context.Context, server *Server, grant rbac.Grant, _ map[string]any) (*aiToolResult, error) {
	rows, err := server.pool.Query(ctx, `SELECT id, name, group_id, min_available FROM coverage_policies WHERE active=TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type policyRow struct {
		id, groupID  int64
		name         string
		minAvailable int
	}
	policies := make([]policyRow, 0)
	for rows.Next() {
		var p policyRow
		if err := rows.Scan(&p.id, &p.name, &p.groupID, &p.minAvailable); err != nil {
			rows.Close()
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	now := time.Now().UTC()
	gaps := make([]map[string]any, 0)
	for _, p := range policies {
		policyGaps, err := coverage.Sweep(ctx, server.pool, p.groupID, now, now.Add(time.Minute), p.minAvailable, nil)
		if err != nil {
			return nil, err
		}
		if len(policyGaps) > 0 {
			gaps = append(gaps, map[string]any{"policy": p.name, "min_available": p.minAvailable})
		}
	}
	summary := "Все политики покрытия соблюдены прямо сейчас"
	if len(gaps) > 0 {
		summary = fmt.Sprintf("Разрывов покрытия: %d из %d политик", len(gaps), len(policies))
	}
	return &aiToolResult{Summary: summary, Data: gaps}, nil
}

func aiGetAnalytics(ctx context.Context, server *Server, grant rbac.Grant, _ map[string]any) (*aiToolResult, error) {
	now := time.Now().UTC()
	rng := analyticsRange{From: now.AddDate(0, 0, -7), To: now, Days: 7}
	site := ""
	if grant.HasScope(rbac.ScopeSite) {
		sites := grant.ScopeValues(rbac.ScopeSite)
		if len(sites) > 0 {
			site = sites[0]
		}
	}
	funnel, err := server.loadNoiseFunnel(ctx, rng, site)
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf(
		"За 7 дней: %d событий → %d проблем → %d инцидентов, %d уведомлений отправлено",
		funnel.rawEvents, funnel.problemsDeduped, funnel.incidentsTotal, funnel.notificationsSent,
	)
	return &aiToolResult{Summary: summary, Data: funnel}, nil
}

var knownEntityPermissions = map[string]rbac.Permission{
	"incident": rbac.IncidentsRead, "equipment": rbac.EquipmentRead,
	"employee": rbac.EmployeesRead, "alert": rbac.AlertsRead,
}

func aiOpenEntity(_ context.Context, _ *Server, grant rbac.Grant, params map[string]any) (*aiToolResult, error) {
	entityType := paramString(params, "entity_type")
	entityID := paramString(params, "entity_id")
	permission, known := knownEntityPermissions[entityType]
	if !known || entityID == "" {
		return nil, fmt.Errorf("неизвестный entity_type или пустой entity_id")
	}
	if !grant.Has(permission) {
		return nil, errAIPermissionDenied
	}
	ref := aiEntityRef{Type: entityType, ID: entityID, Label: entityID}
	return &aiToolResult{Summary: "Открываю " + entityType + " " + entityID, Navigate: &ref}, nil
}
