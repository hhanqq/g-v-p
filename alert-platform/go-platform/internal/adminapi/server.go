package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/availability"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/planner"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

type Server struct {
	pool             *pgxpool.Pool
	demo             *httputil.ReverseProxy
	staticDir        string
	sessions         *SessionManager
	clickhouse       clickhouse.Conn
	ollama           *planner.OllamaClient
	resources        *resourceSampler
	gpuTotalVRAMMB   int
	gatewayHealthURL string
}

func (server *Server) UseSessions(sessions *SessionManager) { server.sessions = sessions }

// UseClickHouse подключает опциональный аналитический сток для
// low-code поиска (раздел «История изменений»). Не установлен — не
// авария: остальной admin API (LDAP, источники, группы, карточки)
// не зависит от ClickHouse, только /api/change-history/search вернёт
// понятную 503 вместо паники на nil-указателе.
func (server *Server) UseClickHouse(conn clickhouse.Conn) { server.clickhouse = conn }

// UseOllama подключает опциональную локальную LLM для формулировки
// подсказки «умная маршрутизация на основе истории» (раздел
// «Использование ИИ»). Не установлен/недоступен — не авария: сама
// подсказка (реальный SQL-запрос к истории подписок коллег) всё равно
// показывается, только шаблонной фразой вместо ИИ-формулировки — раздел
// И5, тот же контракт деградации, что у всех остальных ИИ-сценариев.
func (server *Server) UseOllama(client *planner.OllamaClient) { server.ollama = client }

// UseGPUCapacity — общая физическая емкость VRAM конкретной карты
// (мегабайты), задаётся один раз оператором из `nvidia-smi
// --query-gpu=memory.total`: Ollama API её не отдаёт, а сам admin-api
// не имеет доступа к nvidia-smi без GPU passthrough в контейнер (раздел
// 26 доп. ТЗ). 0 — «неизвестно», используемая VRAM (реальная, из Ollama
// /api/ps) в этом случае показывается без total, не с выдуманным
// значением по умолчанию.
func (server *Server) UseGPUCapacity(totalMB int) { server.gpuTotalVRAMMB = totalMB }

// UseGatewayHealthURL — переопределяет адрес самодиагностики Gateway
// (по умолчанию — внутреннее docker-имя сервиса); нужно для тестов и
// для нестандартных docker-compose топологий.
func (server *Server) UseGatewayHealthURL(url string) { server.gatewayHealthURL = url }

func New(pool *pgxpool.Pool, demoURL, staticDir string) (*Server, error) {
	target, err := url.Parse(demoURL)
	if err != nil {
		return nil, fmt.Errorf("parse demo runner URL: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(response http.ResponseWriter, _ *http.Request, proxyErr error) {
		writeError(response, http.StatusBadGateway, "demo runner unavailable: "+proxyErr.Error())
	}
	server := &Server{
		pool: pool, demo: proxy, staticDir: staticDir,
		resources: newResourceSampler(), gatewayHealthURL: "http://gateway:8080/api/v1/health",
	}
	server.resources.start(context.Background())
	return server, nil
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	path := normalizePath(request.URL.Path)
	if path == "/app" || strings.HasPrefix(path, "/app/") {
		server.serveSPA(response, request, path)
		return
	}
	if request.Method == http.MethodGet && path == "/openapi.json" {
		server.openapi(response)
		return
	}
	if request.Method == http.MethodGet && path == "/docs" {
		server.docs(response)
		return
	}
	if server.handleLegacyRedirect(response, request, path) {
		return
	}
	if strings.HasPrefix(path, "/me/") {
		server.personalCabinet(response, request, path)
		return
	}
	if strings.HasPrefix(path, "/alerts/") {
		server.personalAlerts(response, request, path)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/email-track/open/") {
		server.emailTrackOpen(response, request, path)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/email-track/click/") {
		server.emailTrackClick(response, request, path)
		return
	}
	if server.isDemoRoute(request.Method, path) {
		server.proxyDemo(response, request, path)
		return
	}
	if server.sessions != nil && path == "/api/auth/login" && request.Method == http.MethodPost {
		server.sessions.login(response, request, server)
		return
	}
	if server.sessions != nil && path == "/api/auth/guest-login" && request.Method == http.MethodPost {
		server.sessions.guestLogin(response, request)
		return
	}
	if server.sessions != nil && path == "/api/auth/logout" && request.Method == http.MethodPost {
		server.sessions.logout(response)
		return
	}
	if server.sessions != nil && path == "/api/auth/me" && request.Method == http.MethodGet {
		user, err := server.currentUserWithGrant(request)
		if err != nil {
			writeError(response, http.StatusUnauthorized, "Не авторизован")
			return
		}
		delete(user, "_grant")
		writeJSON(response, http.StatusOK, user)
		return
	}
	if server.routeBI(response, request, path) {
		return
	}
	if server.routeBIAdmin(response, request, path) {
		return
	}
	if server.routeGroups(response, request, path) {
		return
	}
	if request.Method == http.MethodGet && path == "/api/compliance-metrics" {
		server.complianceMetrics(response, request)
		return
	}
	if request.Method == http.MethodGet && path == "/api/home/summary" {
		server.withPermission(response, request, rbac.DashboardRead, server.homeSummary)
		return
	}
	if request.Method == http.MethodGet && path == "/api/home/overview" {
		server.withPermission(response, request, rbac.DashboardRead, server.homeOverview)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/summary" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsSummary)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/overview" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsOverview)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/alerts-timeseries" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsAlertsTimeseries)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/incidents-timeseries" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsIncidentsTimeseries)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/delivery" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsDelivery)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/sla" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsSLA)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/equipment-top" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsEquipmentTop)
		return
	}
	if request.Method == http.MethodGet && path == "/api/analytics/scenarios" {
		server.withPermission(response, request, rbac.AnalyticsRead, server.analyticsScenarios)
		return
	}
	if request.Method == http.MethodGet && path == "/api/metrics" {
		server.withPermission(response, request, rbac.DashboardRead, server.homeSummary)
		return
	}
	if request.Method == http.MethodGet && path == "/api/platform-health" {
		server.withPermission(response, request, rbac.PlatformHealthRead, server.platformHealth)
		return
	}
	if request.Method == http.MethodGet && path == "/api/incidents" {
		server.withPermission(response, request, rbac.IncidentsRead, server.listIncidents)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/api/incidents/") {
		server.withPermission(response, request, rbac.IncidentsRead, server.getIncident)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/routing-trace") && strings.HasPrefix(path, "/api/problems/") {
		server.withPermission(response, request, rbac.IncidentsRead, server.problemRoutingTrace)
		return
	}
	if request.Method == http.MethodGet && path == "/api/alerts/filter-options" {
		server.withPermission(response, request, rbac.AlertsRead, server.alertFilterOptions)
		return
	}
	if request.Method == http.MethodGet && path == "/api/alerts" {
		server.withPermission(response, request, rbac.AlertsRead, server.listAlerts)
		return
	}
	if request.Method == http.MethodGet && path == "/api/employees" {
		server.withPermission(response, request, rbac.EmployeesRead, server.listEmployees)
		return
	}
	if request.Method == http.MethodPost && path == "/api/employees" {
		server.withPermission(response, request, rbac.EmployeesManage, server.createEmployee)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/history") && strings.HasPrefix(path, "/api/employees/") {
		server.withPermission(response, request, rbac.EmployeesRead, server.employeeHistory)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/subscription-suggestion") && strings.HasPrefix(path, "/api/employees/") {
		server.withPermission(response, request, rbac.EmployeesRead, server.employeeSubscriptionSuggestion)
		return
	}
	if server.routeEmployeeAvailability(response, request, path) {
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/api/employees/") {
		server.withPermission(response, request, rbac.EmployeesRead, server.getEmployee)
		return
	}
	if request.Method == http.MethodPut && strings.HasPrefix(path, "/api/employees/") {
		server.withPermission(response, request, rbac.EmployeesManage, server.updateEmployee)
		return
	}

	if request.Method == http.MethodGet && path == "/api/equipment" {
		server.withPermission(response, request, rbac.EquipmentRead, server.listEquipment)
		return
	}
	if request.Method == http.MethodPost && path == "/api/equipment" {
		server.withPermission(response, request, rbac.EquipmentManage, server.createEquipment)
		return
	}
	if request.Method == http.MethodGet && path == "/api/equipment/groups" {
		server.withPermission(response, request, rbac.EquipmentRead, server.listEquipmentGroups)
		return
	}
	if request.Method == http.MethodGet && path == "/api/equipment/search" {
		server.withPermission(response, request, rbac.EquipmentRead, server.equipmentSearch)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/history") && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentRead, server.equipmentHistory)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/summary") && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentRead, server.equipmentSummary)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/incidents") && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentRead, server.equipmentIncidentsList)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/timeline") && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentRead, server.equipmentTimeline)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/graph") && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentRead, server.equipmentGraph)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(path, "/coverage") && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.CoverageRead, server.equipmentCoverage)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentRead, server.getEquipment)
		return
	}
	if request.Method == http.MethodPut && strings.HasPrefix(path, "/api/equipment/") {
		server.withPermission(response, request, rbac.EquipmentManage, server.updateEquipment)
		return
	}
	if request.Method == http.MethodGet && path == "/api/change-history/fields" {
		server.withPermission(response, request, rbac.AuditRead, server.changeHistoryFields)
		return
	}
	if request.Method == http.MethodPost && path == "/api/change-history/search" {
		server.withPermission(response, request, rbac.AuditRead, server.changeHistorySearch)
		return
	}
	if server.routeScenarios(response, request, path) {
		return
	}
	if server.routeCoverage(response, request, path) {
		return
	}
	if request.Method == http.MethodGet && path == "/api/sla-rules" {
		server.withPermission(response, request, rbac.SLARead, server.listSLARules)
		return
	}
	if request.Method == http.MethodPost && path == "/api/sla-rules" {
		server.withPermission(response, request, rbac.SLAManage, server.createSLARule)
		return
	}
	if request.Method == http.MethodGet && path == "/api/integrations/status" {
		server.withPermission(response, request, rbac.IntegrationsRead, server.integrationsStatus)
		return
	}
	if request.Method == http.MethodGet && path == "/api/integrations/delivery-analytics" {
		server.withPermission(response, request, rbac.IntegrationsRead, server.deliveryAnalytics)
		return
	}
	if request.Method == http.MethodGet && path == "/api/sources" {
		server.withPermission(response, request, rbac.SourcesRead, server.listSources)
		return
	}
	if request.Method == http.MethodPost && path == "/api/sources" {
		server.withPermission(response, request, rbac.SourcesManage, server.addSource)
		return
	}
	if request.Method == http.MethodDelete && strings.HasPrefix(path, "/api/sources/") {
		server.withPermission(response, request, rbac.SourcesManage, server.deleteSource)
		return
	}
	if request.Method == http.MethodGet && path == "/api/audit" {
		server.withPermission(response, request, rbac.AuditRead, server.listAudit)
		return
	}
	if server.routeUsers(response, request, path) {
		return
	}

	writeError(response, http.StatusNotFound, "route not found")
}

func (server *Server) homeSummary(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	queue := make(map[string]int64)
	rows, err := server.pool.Query(ctx, `SELECT status,COUNT(*) FROM signal_queue GROUP BY status`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan queue metrics")
			return
		}
		queue[status] = count
	}
	rows.Close()

	var signals, events, parseFailed int64
	if err := server.pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM signals),(SELECT COUNT(*) FROM events),
		       (SELECT COUNT(*) FROM signal_queue WHERE status='parse_failed')`).Scan(&signals, &events, &parseFailed); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var parseSuccessRate *float64
	if signals > 0 {
		value := round(float64(signals-parseFailed)*100/float64(signals), 1)
		parseSuccessRate = &value
	}

	var deliveryTotal, deliverySent, deliveryFailed, supplementsSent int64
	if err := server.pool.QueryRow(ctx, `
		SELECT COUNT(*),COUNT(*) FILTER(WHERE status='sent'),COUNT(*) FILTER(WHERE status='failed'),
		       COUNT(*) FILTER(WHERE type='SUPPLEMENT' AND status='sent') FROM notifications`).Scan(
		&deliveryTotal, &deliverySent, &deliveryFailed, &supplementsSent,
	); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var deliveredPercent *float64
	if deliveryTotal > 0 {
		value := round(float64(deliverySent)*100/float64(deliveryTotal), 1)
		deliveredPercent = &value
	}

	topSymptoms := make([][]any, 0)
	rows, err = server.pool.Query(ctx, `SELECT symptom_class,COUNT(*) FROM problems GROUP BY symptom_class ORDER BY COUNT(*) DESC LIMIT 5`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var symptom string
		var count int64
		if err := rows.Scan(&symptom, &count); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan symptom metrics")
			return
		}
		topSymptoms = append(topSymptoms, []any{symptom, count})
	}
	rows.Close()

	priorityDistribution := make(map[string]int64)
	rows, err = server.pool.Query(ctx, `SELECT priority,COUNT(*) FROM problems WHERE priority IS NOT NULL GROUP BY priority`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var priority string
		var count int64
		if err := rows.Scan(&priority, &count); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan priority metrics")
			return
		}
		priorityDistribution[priority] = count
	}
	rows.Close()

	var resolvedEvents int64
	if err := server.pool.QueryRow(ctx, `SELECT COUNT(*) FROM events WHERE resolved=TRUE`).Scan(&resolvedEvents); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var resolutionCoverage *float64
	if events > 0 {
		value := round(float64(resolvedEvents)*100/float64(events), 1)
		resolutionCoverage = &value
	}
	avgMTTR, err := nullableFloat(ctx, server.pool, `
		SELECT AVG(EXTRACT(EPOCH FROM (resolved_at-opened_at)))
		FROM problems WHERE status='RESOLVED' AND resolved_at IS NOT NULL AND resolved_at>=opened_at`, 1)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	avgLatency, err := nullableFloat(ctx, server.pool, `
		SELECT AVG(EXTRACT(EPOCH FROM (sample.ingest_ts-sample.received_at))) FROM (
			SELECT event.ingest_ts,signal.received_at FROM events event
			JOIN signals signal ON signal.id=event.signal_id ORDER BY event.id DESC LIMIT 200
		) sample`, 3)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var openProblems, incidentCount, duplicates, hypotheses, aiSupplements int64
	if err := server.pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM problems WHERE status IN ('OPEN','FLAPPING')),
		       (SELECT COUNT(*) FROM incidents),
		       (SELECT COUNT(*) FROM problems WHERE duplicate_of_problem_id IS NOT NULL),
		       (SELECT COUNT(*) FROM problems WHERE ai_root_cause_hypothesis IS NOT NULL),
		       (SELECT COUNT(*) FROM notifications WHERE type='SUPPLEMENT' AND status='sent')`).Scan(
		&openProblems, &incidentCount, &duplicates, &hypotheses, &aiSupplements,
	); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"queue":        queue,
		"events":       map[string]any{"signals": signals, "events": events, "parse_failed": parseFailed, "parse_success_rate": parseSuccessRate},
		"delivery":     map[string]any{"total": deliveryTotal, "sent": deliverySent, "failed": deliveryFailed, "supplements_sent": supplementsSent, "delivered_pct": deliveredPercent},
		"top_symptoms": topSymptoms, "priority_distribution": priorityDistribution,
		"resolution_coverage_pct": resolutionCoverage, "avg_mttr_seconds": avgMTTR,
		"avg_ingest_latency_seconds": avgLatency,
		"incidents":                  map[string]any{"open_problems": openProblems, "incidents": incidentCount},
		"ai_scenarios":               map[string]any{"duplicates_detected": duplicates, "root_cause_hypotheses": hypotheses, "ai_supplements_sent": aiSupplements},
	})
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func nullableFloat(ctx context.Context, querier queryRower, query string, digits int) (*float64, error) {
	var value sql.NullFloat64
	if err := querier.QueryRow(ctx, query).Scan(&value); err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	rounded := round(value.Float64, digits)
	return &rounded, nil
}

func round(value float64, digits int) float64 {
	factor := math.Pow10(digits)
	return math.Round(value*factor) / factor
}

// listIncidents — вкладки Активные/Завершённые/Все (раздел «Инциденты» доп.
// ТЗ). Единственный источник правды жизненного цикла инцидента —
// incidents.closed_at (см. closeIncidentIfAllResolved в pipeline/state.go:
// закрывается только когда ВСЕ проблемы-участники реально RESOLVED).
// Раньше фильтр status= здесь ошибочно смотрел на root.status одной
// проблемы — инцидент с уже резолвнутым root, но ещё открытыми
// сиблингами, неверно попадал бы во вкладку «Завершённые», хотя
// closed_at ещё NULL. Фильтр теперь в SQL WHERE (не пост-фильтр после
// LIMIT), иначе limit+status вместе теряли записи.
func (server *Server) listIncidents(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	limit := queryInt(request, "limit", 100)
	statusFilter := request.URL.Query().Get("status")
	statusWhere := ""
	switch statusFilter {
	case "open":
		statusWhere = " WHERE incident.closed_at IS NULL"
	case "closed":
		statusWhere = " WHERE incident.closed_at IS NOT NULL"
	}
	rows, err := server.pool.Query(request.Context(), `
		SELECT incident.id,incident.priority,incident.opened_at,incident.closed_at,
		       root.object_id,root.symptom_class,root.status,
		       (SELECT COUNT(*) FROM incident_problems member WHERE member.incident_id=incident.id)
		FROM incidents incident
		LEFT JOIN problems root ON root.id=incident.root_problem_id`+statusWhere+`
		ORDER BY incident.id DESC LIMIT $1`, limit)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, memberCount int64
		var priority, objectID, symptomClass, status sql.NullString
		var openedAt time.Time
		var closedAt sql.NullTime
		if err := rows.Scan(&id, &priority, &openedAt, &closedAt, &objectID, &symptomClass, &status, &memberCount); err != nil {
			writeError(response, http.StatusInternalServerError, "scan incidents")
			return
		}
		items = append(items, map[string]any{
			"id": id, "priority": nullableString(priority), "opened_at": formatISO(openedAt),
			"closed_at": nullableISO(closedAt), "root_object_id": nullableString(objectID),
			"root_symptom_class": nullableString(symptomClass), "status": nullableStringValue(status, "?"),
			"member_count": memberCount,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load incidents")
		return
	}
	var activeCount, closedCount int64
	if err := server.pool.QueryRow(request.Context(), `
		SELECT count(*) FILTER (WHERE closed_at IS NULL), count(*) FILTER (WHERE closed_at IS NOT NULL)
		FROM incidents`).Scan(&activeCount, &closedCount); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"items": items,
		"counts": map[string]any{
			"active": activeCount, "closed": closedCount, "total": activeCount + closedCount,
		},
	})
}

func (server *Server) getIncident(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	id, ok := pathInt(normalizePath(request.URL.Path), "/api/incidents/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid incident id")
		return
	}
	var priority sql.NullString
	var openedAt time.Time
	var closedAt sql.NullTime
	var rootProblemID int64
	err := server.pool.QueryRow(request.Context(), `SELECT priority,opened_at,closed_at,root_problem_id FROM incidents WHERE id=$1`, id).Scan(&priority, &openedAt, &closedAt, &rootProblemID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Инцидент не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	rows, err := server.pool.Query(request.Context(), `
		SELECT problem.id,member.role,member.rule_id,problem.object_id,problem.symptom_class,
		       problem.status,problem.priority,problem.opened_at,problem.resolved_at,
		       problem.ai_root_cause_hypothesis,problem.acknowledged_at,problem.acknowledged_by
		FROM incident_problems member JOIN problems problem ON problem.id=member.problem_id
		WHERE member.incident_id=$1 ORDER BY member.id`, id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	members := make([]map[string]any, 0)
	for rows.Next() {
		var problemID int64
		var role, symptomClass, status string
		var ruleID, objectID, memberPriority, hypothesis sql.NullString
		var memberOpenedAt time.Time
		var resolvedAt, acknowledgedAt sql.NullTime
		var acknowledgedBy sql.NullString
		if err := rows.Scan(&problemID, &role, &ruleID, &objectID, &symptomClass, &status, &memberPriority, &memberOpenedAt, &resolvedAt, &hypothesis, &acknowledgedAt, &acknowledgedBy); err != nil {
			writeError(response, http.StatusInternalServerError, "scan incident members")
			return
		}
		members = append(members, map[string]any{
			"problem_id": problemID, "role": role, "rule_id": nullableString(ruleID),
			"object_id": nullableString(objectID), "symptom_class": symptomClass, "status": status,
			"priority": nullableString(memberPriority), "opened_at": formatISO(memberOpenedAt),
			"resolved_at": nullableISO(resolvedAt), "ai_root_cause_hypothesis": nullableString(hypothesis),
			"acknowledged_at": nullableISO(acknowledgedAt), "acknowledged_by": nullableString(acknowledgedBy),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load incident members")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"id": id, "priority": nullableString(priority), "opened_at": formatISO(openedAt),
		"closed_at": nullableISO(closedAt), "root_problem_id": rootProblemID, "members": members,
	})
}

func (server *Server) listAlerts(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	limit, offset := queryInt(request, "limit", 100), queryInt(request, "offset", 0)

	filterNode, err := resolveAlertFilter(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	args := make([]any, 0, 8)
	condition, err := compileFilterNode(filterNode, &args)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	where := make([]string, 0, 2)
	if condition != "" {
		where = append(where, condition)
	}
	if rng := parseOptionalRange(request); rng != nil {
		args = append(args, rng.From)
		where = append(where, fmt.Sprintf("event.occurred_at >= $%d", len(args)))
		args = append(args, rng.To)
		where = append(where, fmt.Sprintf("event.occurred_at < $%d", len(args)))
	}
	condition = ""
	if len(where) > 0 {
		condition = " WHERE " + strings.Join(where, " AND ")
	}
	var total int64
	if err := server.pool.QueryRow(request.Context(), `SELECT COUNT(*)`+alertFilterFrom+condition, args...).Scan(&total); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	args = append(args, limit, offset)
	query := `SELECT event.id,event.signal_id,signal.source_system,signal.source_instance,event.symptom_class,
	                 event.symptom_class_source,event.state,event.site,event.object_id,event.resolved,
	                 event.title,event.occurred_at,event.problem_id,
	                 problem.priority,problem.status,problem.acknowledged_at,problem.incident_id,cmdb.equipment_type` +
		alertFilterFrom + condition +
		fmt.Sprintf(" ORDER BY event.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := server.pool.Query(request.Context(), query, args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, signalID int64
		var sourceSystem, sourceInstance, symptomClass, state, title string
		var symptomSource, site, objectID, priority, status, equipmentType sql.NullString
		var resolved bool
		var occurredAt time.Time
		var problemID, incidentID sql.NullInt64
		var acknowledgedAt sql.NullTime
		if err := rows.Scan(&id, &signalID, &sourceSystem, &sourceInstance, &symptomClass, &symptomSource, &state,
			&site, &objectID, &resolved, &title, &occurredAt, &problemID,
			&priority, &status, &acknowledgedAt, &incidentID, &equipmentType); err != nil {
			writeError(response, http.StatusInternalServerError, "scan alerts")
			return
		}
		items = append(items, map[string]any{
			"id": id, "signal_id": signalID, "source_system": sourceSystem, "source_instance": sourceInstance,
			"symptom_class": symptomClass, "symptom_class_source": nullableString(symptomSource), "state": state,
			"site": nullableString(site), "object_id": nullableString(objectID),
			"equipment_type": nullableString(equipmentType), "resolved": resolved, "title": title,
			"occurred_at": formatISO(occurredAt), "problem_id": nullableInt64(problemID),
			"priority": nullableString(priority), "status": nullableString(status),
			"acknowledged_at": nullableISO(acknowledgedAt), "incident_id": nullableInt64(incidentID),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load alerts")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"total": total, "items": items})
}

func (server *Server) listEmployees(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT subscriber.id,subscriber.trueconf_username,subscriber.full_name,subscriber.phone,
		       subscriber.email,subscriber.position,subscriber.active,
		       (SELECT COUNT(*) FROM subscriptions subscription WHERE subscription.subscriber_id=subscriber.id)
		FROM subscribers subscriber ORDER BY subscriber.id`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	type employeeRow struct {
		id, subscriptionCount            int64
		username                         string
		fullName, phone, email, position sql.NullString
		active                           bool
	}
	var employeeRows []employeeRow
	ids := make([]int64, 0)
	for rows.Next() {
		var r employeeRow
		if err := rows.Scan(&r.id, &r.username, &r.fullName, &r.phone, &r.email, &r.position, &r.active, &r.subscriptionCount); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan employees")
			return
		}
		employeeRows = append(employeeRows, r)
		ids = append(ids, r.id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load employees")
		return
	}
	rows.Close()
	// Раньше "текущий статус" был просто последней по valid_from строкой
	// независимо от valid_until — отпуск, закончившийся неделю назад, до
	// сих пор показывался бы активным. availability.Resolve разрешает
	// пересечение интервалов по приоритету и честно проверяет истечение.
	statuses, err := availability.Resolve(request.Context(), server.pool, ids, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	items := make([]map[string]any, 0, len(employeeRows))
	for _, r := range employeeRows {
		var currentKind *string
		if kind := statuses[r.id].Kind; kind != "" {
			currentKind = &kind
		}
		items = append(items, map[string]any{
			"id": r.id, "trueconf_username": r.username, "full_name": nullableString(r.fullName),
			"phone": nullableString(r.phone), "email": nullableString(r.email), "position": nullableString(r.position),
			"active": r.active, "subscription_count": r.subscriptionCount, "availability_status": currentKind,
		})
	}
	writeJSON(response, http.StatusOK, items)
}

func (server *Server) getEmployee(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	id, ok := pathInt(normalizePath(request.URL.Path), "/api/employees/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee id")
		return
	}
	var username string
	var fullName, phone, email, position, competencies sql.NullString
	var active, trueconfEnabled, emailEnabled bool
	err := server.pool.QueryRow(request.Context(), `
		SELECT trueconf_username,full_name,phone,email,position,active,trueconf_enabled,email_enabled,competencies
		FROM subscribers WHERE id=$1`, id,
	).Scan(&username, &fullName, &phone, &email, &position, &active, &trueconfEnabled, &emailEnabled, &competencies)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Сотрудник не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	responsibility, err := server.employeeResponsibilityZones(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	subscriptions, err := server.loadSubscriptions(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	availability, err := server.loadAvailability(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	recentAlerts, err := server.loadRecentAlerts(request.Context(), username)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"id": id, "trueconf_username": username, "full_name": nullableString(fullName),
		"phone": nullableString(phone), "email": nullableString(email), "position": nullableString(position),
		"active": active, "subscriptions": subscriptions, "availability_history": availability,
		"recent_alerts": recentAlerts, "trueconf_enabled": trueconfEnabled, "email_enabled": emailEnabled,
		"competencies": nullableString(competencies), "responsibility_zones": responsibility,
	})
}

// employeeResponsibilityZones — «Ответственность» на карточке сотрудника
// (раздел VIII.34 ТЗ): реальные group_equipment_scope групп, в которых
// состоит сотрудник, а не свободный текст — то же самое, что уже
// использует equipmentResponsibleGroups, только в обратную сторону.
func (server *Server) employeeResponsibilityZones(ctx context.Context, subscriberID int64) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT DISTINCT g.name, scope.site, scope.equipment_type, scope.object_id
		FROM group_members member
		JOIN groups g ON g.id = member.group_id
		LEFT JOIN group_equipment_scope scope ON scope.group_id = g.id
		WHERE member.subscriber_id = $1
		ORDER BY g.name`, subscriberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var groupName string
		var site, equipmentType, objectID sql.NullString
		if err := rows.Scan(&groupName, &site, &equipmentType, &objectID); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"group": groupName, "site": nullableString(site),
			"equipment_type": nullableString(equipmentType), "object_id": nullableString(objectID),
		})
	}
	return items, rows.Err()
}

// loadRecentAlerts возвращает уведомления, реально доставленные этому
// сотруднику (join по notifications.recipient — стабильному trueconf-
// username, а не chat_id, который outbox.py перезаписывает реальным
// provider chat_id после отправки).
func (server *Server) loadRecentAlerts(ctx context.Context, username string) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT notification.id, notification.type, notification.status, notification.created_at, notification.sent_at,
		       problem.id, problem.object_id, problem.symptom_class, problem.site, problem.priority,
		       problem.status, problem.opened_at, problem.resolved_at
		FROM notifications notification
		JOIN problems problem ON problem.id = notification.problem_id
		WHERE notification.recipient = $1
		ORDER BY notification.created_at DESC
		LIMIT 50`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var notificationID, problemID int64
		var notificationType, notificationStatus, symptomClass, problemStatus string
		var objectID, site, priority sql.NullString
		var createdAt, openedAt time.Time
		var sentAt, resolvedAt sql.NullTime
		if err := rows.Scan(&notificationID, &notificationType, &notificationStatus, &createdAt, &sentAt,
			&problemID, &objectID, &symptomClass, &site, &priority, &problemStatus, &openedAt, &resolvedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"notification_id": notificationID, "type": notificationType, "status": notificationStatus,
			"created_at": formatISO(createdAt), "sent_at": nullableISO(sentAt),
			"problem_id": problemID, "object_id": nullableString(objectID), "symptom_class": symptomClass,
			"site": nullableString(site), "priority": nullableString(priority), "problem_status": problemStatus,
			"opened_at": formatISO(openedAt), "resolved_at": nullableISO(resolvedAt),
		})
	}
	return items, rows.Err()
}

func (server *Server) loadSubscriptions(ctx context.Context, employeeID int64) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `SELECT id,subsidiary,service_id,priority_threshold FROM subscriptions WHERE subscriber_id=$1 ORDER BY id`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var subsidiary, serviceID, priority sql.NullString
		if err := rows.Scan(&id, &subsidiary, &serviceID, &priority); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "subsidiary": nullableString(subsidiary), "service_id": nullableString(serviceID), "priority_threshold": nullableString(priority)})
	}
	return items, rows.Err()
}

func (server *Server) loadAvailability(ctx context.Context, employeeID int64) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `SELECT id,kind,valid_until,valid_from,delegate_to_subscriber_id,source,note FROM employee_availability WHERE subscriber_id=$1 ORDER BY valid_from DESC`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var kind, source string
		var validFrom time.Time
		var validUntil sql.NullTime
		var delegateTo sql.NullInt64
		var note sql.NullString
		if err := rows.Scan(&id, &kind, &validUntil, &validFrom, &delegateTo, &source, &note); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "kind": kind, "valid_from": formatISO(validFrom), "valid_until": nullableISO(validUntil),
			"delegate_to_subscriber_id": nullableInt64(delegateTo), "source": source, "note": nullableString(note),
		})
	}
	return items, rows.Err()
}

type availabilityRequest struct {
	Kind                   string  `json:"kind"`
	ValidUntil             *string `json:"valid_until"`
	Note                   *string `json:"note"`
	DelegateToSubscriberID *int64  `json:"delegate_to_subscriber_id"`
}

var validAvailabilityKinds = map[string]bool{
	"override_available": true, "override_unavailable": true, "sick_leave": true,
	"vacation": true, "delegation": true, "shift": true, "on_call": true,
	"unavailable": true, "available": true,
}

func (server *Server) setEmployeeAvailability(response http.ResponseWriter, request *http.Request, user map[string]any) {
	path := strings.TrimSuffix(normalizePath(request.URL.Path), "/availability")
	id, ok := pathInt(path, "/api/employees/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee id")
		return
	}
	var payload availabilityRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || !validAvailabilityKinds[payload.Kind] {
		writeError(response, http.StatusUnprocessableEntity, "invalid availability payload")
		return
	}
	if (payload.Kind == "delegation") != (payload.DelegateToSubscriberID != nil) {
		writeError(response, http.StatusUnprocessableEntity, "delegation requires delegate_to_subscriber_id, other kinds must not set it")
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
	// Быстрый тумблер означает «я вот это, с текущего момента, бессрочно»
	// — закрывает любой ранее открытый интервал. Календарное создание
	// произвольных интервалов (следующий этап) через этот путь не идёт —
	// там пересекающиеся интервалы обязаны сосуществовать и решаться
	// приоритетом при чтении (internal/availability), а не закрытием.
	if _, err = tx.Exec(ctx, `UPDATE employee_availability SET valid_until=$2 WHERE subscriber_id=$1 AND valid_until IS NULL AND valid_from<=$2`, id, now); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var availabilityID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO employee_availability(subscriber_id,kind,delegate_to_subscriber_id,valid_from,valid_until,source,note,created_at)
		VALUES($1,$2,$3,$4,$5,'manual',$6,$4) RETURNING id`,
		id, payload.Kind, payload.DelegateToSubscriberID, now, validUntil, payload.Note).Scan(&availabilityID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	actor, _ := user["username"].(string)
	_, err = tx.Exec(ctx, `INSERT INTO audit_log(actor,action,target,detail,created_at) VALUES($1,'set_availability',$2,$3,$4)`, actor, username, payload.Kind, now)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "subscriber.set_availability",
		ResourceType: "subscriber", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{"kind": payload.Kind, "valid_until": payload.ValidUntil, "note": payload.Note},
	}); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "id": availabilityID})
}

func queryInt(request *http.Request, name string, fallback int) int {
	raw := request.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func pathInt(path, prefix string) (int64, bool) {
	raw := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, map[string]any)

func (server *Server) withAuth(response http.ResponseWriter, request *http.Request, next authenticatedHandler) {
	if server.sessions == nil {
		writeError(response, http.StatusUnauthorized, "требуется вход")
		return
	}
	user, err := server.currentUserWithGrant(request)
	if err != nil {
		if errors.Is(err, errAccountInactive) {
			writeError(response, http.StatusForbidden, err.Error())
			return
		}
		writeError(response, http.StatusUnauthorized, err.Error())
		return
	}
	next(response, request, user)
}

func (server *Server) auditLoginFailure(ctx context.Context, username string) {
	if server.pool == nil {
		return
	}
	if username == "" {
		username = "?"
	}
	_, _ = server.pool.Exec(ctx, `INSERT INTO audit_log(actor,action,created_at) VALUES($1,'session_login_failed',$2)`, username, time.Now().UTC())
}

func (server *Server) proxyDemo(response http.ResponseWriter, request *http.Request, normalizedPath string) {
	clone := request.Clone(request.Context())
	clone.URL.Path = normalizedPath
	clone.URL.RawPath = ""
	server.demo.ServeHTTP(response, clone)
}

func normalizePath(path string) string {
	if path == "/console" {
		return "/"
	}
	if strings.HasPrefix(path, "/console/") {
		return strings.TrimPrefix(path, "/console")
	}
	return path
}

func (server *Server) serveSPA(response http.ResponseWriter, request *http.Request, normalizedPath string) {
	relative := strings.TrimPrefix(normalizedPath, "/app")
	if relative == "" || relative == "/" || filepath.Ext(relative) == "" {
		http.ServeFile(response, request, filepath.Join(server.staticDir, "index.html"))
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(relative, "/"))
	if clean == "." || strings.HasPrefix(clean, "..") {
		http.NotFound(response, request)
		return
	}
	path := filepath.Join(server.staticDir, clean)
	if _, err := os.Stat(path); err != nil {
		http.NotFound(response, request)
		return
	}
	http.ServeFile(response, request, path)
}

type equipmentListItem struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	EquipmentType  *string `json:"equipment_type"`
	Site           string  `json:"site"`
	Name           string  `json:"name"`
	FQDN           *string `json:"fqdn"`
	IP             *string `json:"ip"`
	ActiveProblems int     `json:"active_problems"`
	OpenIncidents  int     `json:"open_incidents"`
	Alerts24h      int     `json:"alerts_24h"`
	Alerts30d      int     `json:"alerts_30d"`
	LastEventAt    *string `json:"last_event_at"`
	WorstPriority  *string `json:"worst_priority"`
}

// listEquipment — GET /api/equipment[?site=&equipment_type=]. Лист конечного
// уровня drill-down (раздел I.7 ТЗ): каждая строка несёт собственные
// агрегаты (активные проблемы/открытые инциденты/алерты 24ч и 30д/статус),
// не только паспортные поля — чтобы состояние объекта было видно до
// перехода в карточку.
func (server *Server) listEquipment(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	query := `SELECT id,kind,equipment_type,site,name,fqdn,ip FROM cmdb_objects`
	conditions := []string{}
	args := []any{}
	if site := request.URL.Query().Get("site"); site != "" {
		args = append(args, site)
		conditions = append(conditions, fmt.Sprintf("site=$%d", len(args)))
	}
	if equipmentType := request.URL.Query().Get("equipment_type"); equipmentType != "" {
		args = append(args, equipmentType)
		conditions = append(conditions, fmt.Sprintf("equipment_type=$%d", len(args)))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += ` ORDER BY id`
	rows, err := server.pool.Query(request.Context(), query, args...)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	items := make([]equipmentListItem, 0)
	objectIDs := make([]string, 0)
	for rows.Next() {
		var item equipmentListItem
		var equipmentType, fqdn, ip sql.NullString
		if err := rows.Scan(&item.ID, &item.Kind, &equipmentType, &item.Site, &item.Name, &fqdn, &ip); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan equipment")
			return
		}
		item.EquipmentType, item.FQDN, item.IP = nullableString(equipmentType), nullableString(fqdn), nullableString(ip)
		items = append(items, item)
		objectIDs = append(objectIDs, item.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load equipment")
		return
	}
	rows.Close()

	stats, err := server.equipmentObjectStats(request.Context(), objectIDs)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for i := range items {
		if s, ok := stats[items[i].ID]; ok {
			items[i].ActiveProblems, items[i].OpenIncidents = s.active, s.openIncidents
			items[i].Alerts24h, items[i].Alerts30d = s.alerts24h, s.alerts30d
			items[i].LastEventAt, items[i].WorstPriority = s.lastEventAt, s.worstPriority
		}
	}
	writeJSON(response, http.StatusOK, items)
}

type equipmentObjectStat struct {
	active, openIncidents, alerts24h, alerts30d int
	lastEventAt, worstPriority                  *string
}

// equipmentObjectStats — по-объектные агрегаты для листинга/summary.
// Три отдельных запроса (проблемы/инциденты, события) вместо одного
// JOIN — намеренно: problems×events на один объект дало бы decartово
// произведение строк и задвоило бы счётчики.
func (server *Server) equipmentObjectStats(ctx context.Context, objectIDs []string) (map[string]equipmentObjectStat, error) {
	result := make(map[string]equipmentObjectStat, len(objectIDs))
	if len(objectIDs) == 0 {
		return result, nil
	}
	problemRows, err := server.pool.Query(ctx, `
		SELECT object_id,
			count(*) FILTER (WHERE status IN ('OPEN','FLAPPING')) AS active,
			count(DISTINCT incident_id) FILTER (WHERE incident_id IS NOT NULL AND status IN ('OPEN','FLAPPING')) AS open_incidents,
			min(priority) FILTER (WHERE status IN ('OPEN','FLAPPING')) AS worst_priority
		FROM problems WHERE object_id = ANY($1) GROUP BY object_id`, objectIDs)
	if err != nil {
		return nil, err
	}
	for problemRows.Next() {
		var objectID string
		var stat equipmentObjectStat
		var worstPriority sql.NullString
		if err := problemRows.Scan(&objectID, &stat.active, &stat.openIncidents, &worstPriority); err != nil {
			problemRows.Close()
			return nil, err
		}
		stat.worstPriority = nullableString(worstPriority)
		result[objectID] = stat
	}
	if err := problemRows.Err(); err != nil {
		problemRows.Close()
		return nil, err
	}
	problemRows.Close()

	eventRows, err := server.pool.Query(ctx, `
		SELECT object_id,
			count(*) FILTER (WHERE occurred_at >= now() - INTERVAL '24 hours') AS alerts24h,
			count(*) FILTER (WHERE occurred_at >= now() - INTERVAL '30 days') AS alerts30d,
			max(occurred_at) AS last_event_at
		FROM events WHERE object_id = ANY($1) GROUP BY object_id`, objectIDs)
	if err != nil {
		return nil, err
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var objectID string
		var alerts24h, alerts30d int
		var lastEventAt sql.NullTime
		if err := eventRows.Scan(&objectID, &alerts24h, &alerts30d, &lastEventAt); err != nil {
			return nil, err
		}
		stat := result[objectID]
		stat.alerts24h, stat.alerts30d = alerts24h, alerts30d
		if lastEventAt.Valid {
			formatted := formatISO(lastEventAt.Time)
			stat.lastEventAt = &formatted
		}
		result[objectID] = stat
	}
	return result, eventRows.Err()
}

type relatedProblem struct {
	ID                   int64   `json:"id"`
	SymptomClass         string  `json:"symptom_class"`
	Status               string  `json:"status"`
	Priority             *string `json:"priority"`
	OpenedAt             string  `json:"opened_at"`
	ResolvedAt           *string `json:"resolved_at"`
	IncidentID           *int64  `json:"incident_id"`
	DuplicateOfProblemID *int64  `json:"duplicate_of_problem_id"`
}

type equipmentDetail struct {
	equipmentListItem
	Subnet            *string          `json:"subnet"`
	InstallDate       *string          `json:"install_date"`
	SpecJSON          *string          `json:"spec_json"`
	RelatedProblems   []relatedProblem `json:"related_problems"`
	Interactions      map[string]any   `json:"interactions"`
	ResponsibleGroups []map[string]any `json:"responsible_groups"`
}

func (server *Server) getEquipment(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	normalized := normalizePath(request.URL.Path)
	rawID := strings.TrimPrefix(normalized, "/api/equipment/")
	objectID, err := url.PathUnescape(rawID)
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	var item equipmentDetail
	var equipmentType, fqdn, ip, subnet, specJSON sql.NullString
	var installDate sql.NullTime
	err = server.pool.QueryRow(request.Context(), `
		SELECT id,kind,equipment_type,site,name,fqdn,ip,subnet,install_date,spec_json
		FROM cmdb_objects WHERE id=$1`, objectID,
	).Scan(&item.ID, &item.Kind, &equipmentType, &item.Site, &item.Name, &fqdn, &ip, &subnet, &installDate, &specJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Объект не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	item.EquipmentType, item.FQDN, item.IP = nullableString(equipmentType), nullableString(fqdn), nullableString(ip)
	item.Subnet, item.SpecJSON = nullableString(subnet), nullableString(specJSON)
	if installDate.Valid {
		formatted := installDate.Time.Format("2006-01-02")
		item.InstallDate = &formatted
	}
	item.RelatedProblems = make([]relatedProblem, 0)
	rows, err := server.pool.Query(request.Context(), `
		SELECT id,symptom_class,status,priority,opened_at,resolved_at,incident_id,duplicate_of_problem_id
		FROM problems WHERE object_id=$1 ORDER BY opened_at DESC LIMIT 50`, objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var problem relatedProblem
		var priority sql.NullString
		var openedAt time.Time
		var resolvedAt sql.NullTime
		var incidentID, duplicateID sql.NullInt64
		if err := rows.Scan(&problem.ID, &problem.SymptomClass, &problem.Status, &priority, &openedAt, &resolvedAt, &incidentID, &duplicateID); err != nil {
			writeError(response, http.StatusInternalServerError, "scan problems")
			return
		}
		problem.Priority = nullableString(priority)
		problem.OpenedAt = formatISO(openedAt)
		if resolvedAt.Valid {
			formatted := formatISO(resolvedAt.Time)
			problem.ResolvedAt = &formatted
		}
		problem.IncidentID, problem.DuplicateOfProblemID = nullableInt64(incidentID), nullableInt64(duplicateID)
		item.RelatedProblems = append(item.RelatedProblems, problem)
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load problems")
		return
	}
	item.Interactions, err = server.equipmentInteractions(request.Context(), objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	item.ResponsibleGroups, err = server.equipmentResponsibleGroups(request.Context(), objectID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, item)
}

// equipmentResponsibleGroups — обратная сторона group_equipment_scope:
// какие группы отвечают за это оборудование (по точному объекту, его
// типу оборудования или площадке). Используется карточкой оборудования,
// чтобы связь «группа ↔ оборудование» была видна с обеих сторон.
func (server *Server) equipmentResponsibleGroups(ctx context.Context, objectID string) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT DISTINCT g.id, g.name
		FROM group_equipment_scope scope
		JOIN groups g ON g.id = scope.group_id
		JOIN cmdb_objects object ON object.id = $1
		WHERE (scope.object_id IS NULL OR scope.object_id = $1)
		  AND (scope.equipment_type IS NULL OR scope.equipment_type = object.equipment_type)
		  AND (scope.site IS NULL OR scope.site = object.site)
		ORDER BY g.name`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name})
	}
	return items, rows.Err()
}

func (server *Server) listScenarios(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `SELECT id,name,description,status,updated_at FROM scenarios ORDER BY updated_at DESC`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, status string
		var description sql.NullString
		var updatedAt time.Time
		if err := rows.Scan(&id, &name, &description, &status, &updatedAt); err != nil {
			writeError(response, http.StatusInternalServerError, "scan scenarios")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "description": nullableString(description), "status": status, "updated_at": formatISO(updatedAt)})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load scenarios")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (server *Server) listSLARules(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `SELECT id,name,priority,subsidiary,response_minutes,resolution_minutes FROM sla_rules ORDER BY id`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, priority string
		var subsidiary sql.NullString
		var responseMinutes, resolutionMinutes int
		if err := rows.Scan(&id, &name, &priority, &subsidiary, &responseMinutes, &resolutionMinutes); err != nil {
			writeError(response, http.StatusInternalServerError, "scan SLA rules")
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "priority": priority, "subsidiary": nullableString(subsidiary),
			"response_minutes": responseMinutes, "resolution_minutes": resolutionMinutes,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load SLA rules")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

// integrationsStatus — намеренно НЕ обращается к БД (проверено
// TestIntegrationsRequiresGoSession с server.pool=nil — эта ручка
// тестирует только auth/session-плумбинг). Живые числа по Email —
// GET /api/integrations/delivery-analytics ниже.
func (server *Server) integrationsStatus(response http.ResponseWriter, _ *http.Request, _ map[string]any) {
	writeJSON(response, http.StatusOK, []map[string]string{
		{"name": "TrueConf", "status": "active", "detail": "Основной канал доставки, раздел 9"},
		{"name": "Системы мониторинга (Zabbix/SolarWinds)", "status": "active", "detail": "Подключение источников — /sources/, без редеплоя"},
		{"name": "SMS", "status": "planned", "detail": "Опционально по кейсу — не реализовано в Этапе 1"},
		{"name": "E-mail", "status": "active", "detail": "Второй канал доставки через delivery-email (SMTP), раздел VII — реальные письма, не декоративная интеграция. Включается на карточке сотрудника, живые цифры — ниже"},
		{"name": "HR-система (доступность сотрудников)", "status": "open_question", "detail": "Источник данных не определён — требует уточнения у менторов"},
	})
}

// deliveryAnalytics — GET /api/integrations/delivery-analytics: доля
// доставленного/ошибок по каждому реальному каналу delivery_outbox, не
// только TrueConf (раздел VII.31 ТЗ). "sent" здесь означает, что канал
// принял сообщение (SMTP accepted / TrueConf API отправил) — не то же
// самое, что "получатель прочитал"; отдельно честно оговорено, что без
// корпоративного read-receipt механизма прочтение неизвестно.
func (server *Server) deliveryAnalytics(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	rows, err := server.pool.Query(request.Context(), `
		SELECT channel,
			count(*) AS total,
			count(*) FILTER (WHERE status='sent') AS sent,
			count(*) FILTER (WHERE status='failed') AS failed
		FROM delivery_outbox GROUP BY channel ORDER BY channel`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var channel string
		var total, sent, failed int
		if err := rows.Scan(&channel, &total, &sent, &failed); err != nil {
			writeError(response, http.StatusInternalServerError, "scan delivery analytics")
			return
		}
		var sentPct, failedPct *float64
		if total > 0 {
			s, f := float64(sent)/float64(total)*100, float64(failed)/float64(total)*100
			sentPct, failedPct = &s, &f
		}
		items = append(items, map[string]any{
			"channel": channel, "total": total, "sent": sent, "failed": failed,
			"sent_pct": sentPct, "failed_pct": failedPct,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load delivery analytics")
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableStringValue(value sql.NullString, fallback string) string {
	if !value.Valid {
		return fallback
	}
	return value.String
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullableISO(value sql.NullTime) *string {
	if !value.Valid {
		return nil
	}
	formatted := formatISO(value.Time)
	return &formatted
}

func formatISO(value time.Time) string {
	return value.Format("2006-01-02T15:04:05.999999999")
}

func parseISO(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO datetime")
}
