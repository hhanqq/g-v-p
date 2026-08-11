// Единая точка входа для запросов к бэкенду. Абсолютный путь /console/api/...
// работает и в проде (nginx проксирует /console/* на admin-console как
// есть), и в dev-сервере Vite (см. vite.config.ts — тот же префикс
// проксируется локально с срезкой /console).
const BASE = "/console/api";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

// isGuestSession — обновляется из useCurrentUser() при каждом успешном
// /auth/me. Раздел 19 доп. ТЗ: гость должен видеть именно «Недоступно в
// гостевом режиме», а не сырое «недостаточно прав: sla.manage» — backend
// уже честно возвращает 403 в обоих случаях (реальное ограничение прав),
// здесь только косметика сообщения для конкретно гостевой сессии.
let isGuestSession = false;
export function setGuestSessionFlag(guest: boolean) {
  isGuestSession = guest;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    if (res.status === 403 && isGuestSession) {
      throw new ApiError(res.status, "Недоступно в гостевом режиме");
    }
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.detail ?? res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};

export interface CurrentUser {
  username: string;
  is_admin: boolean;
  guest: boolean;
  role: string;
  role_label: string;
  permissions: string[];
  scopes: { type: string; value: string }[];
  platform_user_id: number;
}

export function hasPermission(user: CurrentUser | undefined, permission: string): boolean {
  return !!user?.permissions?.includes(permission);
}

export interface PlatformUserListItem {
  id: number;
  username: string;
  role: string;
  role_label: string;
  active: boolean;
  override_count: number;
  scope_count: number;
}

export interface PlatformUserDetail {
  id: number;
  username: string;
  role: string;
  role_label: string;
  active: boolean;
  overrides: { permission: string; effect: "grant" | "deny" }[];
  scopes: { type: string; value: string }[];
  effective_permissions: string[];
}

export interface HomeOverview {
  kpis: { open_incidents: number; critical_active: number; no_reaction: number; sla_breaches_today: number };
  alerts_series: { bucket: string; priority: string; count: number }[];
  alerts_period: { from: string; to: string; granularity: "hour" | "6h" | "day" | "week" };
  needs_attention: { kind: string; text: string; detail: string; priority?: string | null; incident_id?: number; problem_id?: number }[];
  adp_health: ComponentStatus[];
  resources: {
    cpu_pct?: number; ram_pct?: number; disk_pct?: number;
    cpu_series: number[]; ram_series: number[];
    ai: PlatformHealth["ai"];
  };
  scenarios: { active_scenarios: number; runs_today: number; awaiting_reaction: number; escalations_today: number };
  coverage: { critical_total: number; critical_fully_covered: number; gaps_next_7d: number };
}

export interface ComponentStatus {
  name: string;
  status: "normal" | "degraded" | "unknown";
  detail?: string;
}

export interface PlatformHealth {
  components: ComponentStatus[];
  resources: {
    cpu_pct?: number;
    cpu_series: number[];
    ram_pct?: number;
    ram_used_gb?: number;
    ram_total_gb?: number;
    ram_series: number[];
    disk_pct?: number;
    disk_used_gb?: number;
    disk_total_gb?: number;
  };
  ai: {
    ollama_available: boolean;
    gpu: "unavailable" | { vram_used_gb: number; vram_total_gb: number };
    requests_per_min_last_hour?: number;
    inference_p95_seconds?: number | null;
  };
}

export interface RBACMeta {
  roles: { value: string; label: string }[];
  permissions: { value: string; label: string }[];
  role_permissions: Record<string, string[]>;
  scope_types: { value: string; label: string }[];
}

export interface HomeSummary {
  queue: Record<string, number>;
  events: { signals: number; events: number; parse_failed: number; parse_success_rate: number | null };
  delivery: { total: number; sent: number; failed: number; supplements_sent: number; delivered_pct: number | null };
  top_symptoms: [string, number][];
  priority_distribution: Record<string, number>;
  resolution_coverage_pct: number | null;
  avg_mttr_seconds: number | null;
  avg_ingest_latency_seconds: number | null;
  incidents: { open_problems: number; incidents: number };
  ai_scenarios: { duplicates_detected: number; root_cause_hypotheses: number; ai_supplements_sent: number };
}

export interface IncidentListItem {
  id: number;
  priority: string | null;
  opened_at: string;
  closed_at: string | null;
  root_object_id: string | null;
  root_symptom_class: string | null;
  status: string;
  member_count: number;
}

export interface IncidentListResponse {
  items: IncidentListItem[];
  counts: { active: number; closed: number; total: number };
}

export interface IncidentMember {
  problem_id: number;
  role: string;
  rule_id: string | null;
  object_id: string | null;
  symptom_class: string;
  status: string;
  priority: string | null;
  opened_at: string;
  resolved_at: string | null;
  ai_root_cause_hypothesis: string | null;
  acknowledged_at: string | null;
  acknowledged_by: string | null;
}

export interface IncidentDetail {
  id: number;
  priority: string | null;
  opened_at: string;
  closed_at: string | null;
  root_problem_id: number;
  members: IncidentMember[];
}

export interface RoutingTraceItem {
  source: "notification" | "scenario";
  at: string;
  // source === "notification"
  notification_type?: string;
  recipient?: string;
  // source === "scenario"
  scenario_id?: number;
  scenario_name?: string;
  node_id?: string;
  branch?: string | null;
  trace: {
    reason?: string;
    selected?: string[];
    available?: boolean;
    kind?: string;
    delegated_from?: string | null;
    candidates?: { username: string; available: boolean; kind: string }[];
  };
}

export interface AlertItem {
  id: number;
  signal_id: number;
  source_system: string;
  source_instance: string;
  symptom_class: string;
  symptom_class_source: string | null;
  state: string;
  site: string | null;
  object_id: string | null;
  equipment_type: string | null;
  resolved: boolean;
  title: string;
  occurred_at: string;
  problem_id: number | null;
  priority: string | null;
  status: string | null;
  acknowledged_at: string | null;
  incident_id: number | null;
}

// FilterNode — зеркало Go internal/adminapi.FilterNode: и панель быстрых
// фильтров, и Query Builder собирают ровно эту структуру, единая точка
// компиляции в SQL — на backend (см. alert_filter.go).
export interface FilterNode {
  match?: "all" | "any";
  conditions?: FilterNode[];
  field?: string;
  op?: string;
  value?: string | string[] | number | boolean;
}

export interface AlertFilterOptions {
  priorities: string[];
  sources: string[];
  statuses: { value: string; label: string }[];
  reactions: { value: string; label: string }[];
}

export interface EquipmentListItem {
  id: string;
  kind: string;
  equipment_type: string | null;
  site: string;
  name: string;
  fqdn: string | null;
  ip: string | null;
  active_problems: number;
  open_incidents: number;
  alerts_24h: number;
  alerts_30d: number;
  last_event_at: string | null;
  worst_priority: string | null;
}

export interface EquipmentGroup {
  key: string;
  label: string;
  object_count: number;
  active_problems: number;
  p0_active: number;
  p1_active: number;
  open_incidents: number;
  alerts_24h: number;
  alerts_30d: number;
  avg_mttr_minutes: number | null;
  worst_priority: string | null;
}

export interface EquipmentSearchResult {
  id: string;
  name: string;
  site: string;
  site_label: string;
  equipment_type: string | null;
  category_label: string | null;
}

export interface EquipmentCoverage {
  responsible_groups: { id: number; name: string }[];
  members: { id: number; username: string; display_name: string }[];
  policy: { id: number; name: string; min_available: number; group_id: number } | null;
  granularity: "hour" | "day";
  timeline: { buckets: string[]; by_member: Record<string, string[]>; available_count: number[] };
  gaps: { from: string; to: string; min_available: number }[];
  coverage_pct: number | null;
}

export interface EquipmentSummary {
  active_problems: number;
  open_incidents: number;
  alerts_24h: number;
  alerts_30d: number;
  last_event_at: string | null;
  worst_priority: string | null;
  avg_mttr_minutes_30d: number | null;
}

export interface EquipmentIncidentItem {
  id: number;
  root_problem_id: number;
  priority: string | null;
  opened_at: string;
  closed_at: string | null;
  member_count: number;
  symptom_class: string;
  object_name: string;
}

export interface TimelineEntry {
  at: string;
  kind: string;
  title: string;
  detail: string;
}

export interface AlertGraphNode {
  id: string;
  problem_id: number;
  incident_id: number;
  object_id: string | null;
  object_name: string;
  symptom_class: string;
  priority: string | null;
  status: string;
  opened_at: string;
  role: string;
}

export interface AlertGraphEdge {
  from: string;
  to: string;
  rule_id: string | null;
}

export interface EquipmentInteraction {
  object_id: string;
  name: string;
  symptom_class: string;
  count: number;
}

export interface EquipmentDetail extends EquipmentListItem {
  subnet: string | null;
  install_date: string | null;
  spec_json: string | null;
  interactions: { caused: EquipmentInteraction[]; caused_by: EquipmentInteraction[] };
  related_problems: {
    id: number;
    symptom_class: string;
    status: string;
    priority: string | null;
    opened_at: string;
    resolved_at: string | null;
    incident_id: number | null;
    duplicate_of_problem_id: number | null;
  }[];
  responsible_groups: { id: number; name: string }[];
}

export interface GroupListItem {
  id: number;
  name: string;
  description: string | null;
  created_at: string;
  member_count: number;
  equipment_scope_count: number;
}

export interface GroupMember {
  subscriber_id: number;
  trueconf_username: string;
  full_name: string | null;
}

export interface GroupEquipmentScope {
  id: number;
  object_id: string | null;
  object_name: string | null;
  equipment_type: string | null;
  site: string | null;
}

export interface GroupDetail {
  id: number;
  name: string;
  description: string | null;
  created_at: string;
  members: GroupMember[];
  equipment_scope: GroupEquipmentScope[];
}

export interface EmployeeListItem {
  id: number;
  trueconf_username: string;
  full_name: string | null;
  phone: string | null;
  email: string | null;
  position: string | null;
  active: boolean;
  subscription_count: number;
  availability_status: string | null;
}

export interface EmployeeRecentAlert {
  notification_id: number;
  type: string;
  status: string;
  created_at: string;
  sent_at: string | null;
  problem_id: number;
  object_id: string | null;
  symptom_class: string;
  site: string | null;
  priority: string | null;
  problem_status: string;
  opened_at: string;
  resolved_at: string | null;
}

export interface EmployeeDetail extends EmployeeListItem {
  subscriptions: { id: number; subsidiary: string | null; service_id: string | null; priority_threshold: string | null }[];
  availability_history: {
    id: number;
    kind: string;
    valid_from: string;
    valid_until: string | null;
    delegate_to_subscriber_id: number | null;
    source: string;
    note: string | null;
  }[];
  recent_alerts: EmployeeRecentAlert[];
  trueconf_enabled: boolean;
  email_enabled: boolean;
  competencies: string | null;
  responsibility_zones: { group: string; site: string | null; equipment_type: string | null; object_id: string | null }[];
}

export interface ScenarioListItem {
  id: number;
  name: string;
  description: string | null;
  status: string;
  updated_at: string;
}

export interface ScenarioDetail extends ScenarioListItem {
  graph_json: string;
  created_by: string | null;
}

export interface CoveragePolicy {
  id: number;
  name: string;
  group_id: number;
  group_name: string;
  min_available: number;
  object_id: string | null;
  equipment_type: string | null;
  site: string | null;
  active: boolean;
}

export interface CoverageGap {
  from: string;
  to: string;
  min_available: number;
}

export interface PolicyGapsResponse {
  policy_id: number;
  policy_name: string;
  gaps: CoverageGap[];
}

export interface AvailabilityInterval {
  id: number;
  kind: string;
  valid_from: string;
  valid_until: string | null;
  delegate_to_subscriber_id: number | null;
  note: string | null;
}

export interface AvailabilityCalendarResponse {
  days: { date: string; available: boolean; kind: string }[];
  intervals: AvailabilityInterval[];
}

export interface DryRunResponse {
  warnings: string[];
}

export interface ScenarioStatsResponse {
  scenario_id: number;
  version: number;
  counters: { node_id: string; node_type: string; branch: string; count: number }[];
}

export interface ScenarioRunItem {
  run_id: number;
  problem_id: number;
  current_node_id: string;
  status: string;
  step_entered_at: string;
  notified_count: number;
  version: number;
  symptom_class: string;
  object_id: string | null;
  priority: string | null;
  problem_status: string;
}

export interface ScenarioRunTrace {
  run_id: number;
  problem_id: number;
  version: number;
  graph_json: string;
  steps: { node_id: string; node_type: string; branch: string; recipients_json: string | null; entered_at: string }[];
}

export interface SlaRuleItem {
  id: number;
  name: string;
  priority: string;
  subsidiary: string | null;
  service_id: string | null;
  response_minutes: number;
  resolution_minutes: number;
}

export interface AnalyticsSummary {
  alerts_over_time: [string, number][];
  top_problem_objects: {
    object_id: string; count: number; name: string; site: string | null; equipment_type: string | null;
  }[];
  top_symptoms: [string, number][];
  priority_distribution: Record<string, number>;
  sla_breach: { total: number; by_priority: Record<string, number> };
  avg_mttr_seconds: number | null;
  resolution_coverage_pct: number | null;
  incidents: { open_problems: number; incidents: number };
}

export interface AnalyticsOverview {
  alerts_total: number;
  incidents_total: number;
  mtta_seconds: number | null;
  mttr_seconds: number | null;
  ack_rate_pct: number | null;
  noise_reduction_pct: number | null;
  noise_funnel: {
    raw_events: number; deduplicated: number; problems_total: number;
    incidents: number; notifications_sent: number; folded_into_existing: number;
  };
  priority_distribution: Record<string, number>;
  source_distribution: { source_system: string; count: number }[];
}

export interface AlertsTimeseriesPoint { day: string; key: string; count: number }
export interface AlertsTimeseriesResponse { series: AlertsTimeseriesPoint[]; groupby: "priority" | "source" }

export interface IncidentsTimeseriesResponse {
  series: { day: string; created: number; closed: number }[];
  open_vs_closed: { open: number; in_progress: number; closed: number };
}

export interface DeliveryAnalytics {
  trueconf: {
    created: number; sent: number; failed: number; success_rate_pct: number | null;
    requiring_ack: number; acknowledged: number; ack_rate_pct: number | null;
    mtta_seconds: number | null; escalations: number;
  };
  email: {
    created: number; sent: number; failed: number; opened: number; clicked: number;
    open_rate_pct: number | null; ctr_pct: number | null; ctor_pct: number | null;
  };
  ack_rate_by_priority: { priority: string; ack_rate_pct: number | null; total: number }[];
  mtta_distribution: { bucket: string; count: number }[];
  ack_rate_series: { day: string; ack_rate_pct: number | null }[];
}

export interface SLAAnalytics {
  applicable: number;
  breached: number;
  compliance_pct: number | null;
  breach_series: { day: string; breaches: number }[];
}

export interface EquipmentTopAnalytics {
  top_objects: { object_id: string; count: number; name: string; site: string | null; equipment_type: string | null }[];
  top_symptoms: { symptom_class: string; count: number }[];
}

export interface ScenarioAnalytics {
  total_runs: number;
  done_runs: number;
  no_recipient_runs: number;
  escalated_runs: number;
  avg_steps: number | null;
  resolved_without_escalation_pct: number | null;
  top_scenarios: { name: string; runs: number }[];
}

export interface IntegrationStatus {
  name: string;
  status: "active" | "planned" | "open_question";
  detail: string;
}

export interface BIServiceAccount {
  id: number;
  name: string;
  token_prefix: string;
  active: boolean;
  created_by: string | null;
  created_at: string;
  last_used_at?: string;
  scope_count: number;
}

export interface DeliveryChannelAnalytics {
  channel: string;
  total: number;
  sent: number;
  failed: number;
  sent_pct: number | null;
  failed_pct: number | null;
}

export interface SourceItem {
  id: number;
  instance: string;
  system: string;
  site: string;
  api_token: string | null;
  created_at: string;
}

export interface SourceCatalog {
  items: SourceItem[];
  systems: string[];
  sites: string[];
}

export interface AuditItem {
  id: number;
  actor: string;
  action: string;
  target: string | null;
  detail: string | null;
  created_at: string;
}

// Раздел «История изменений» — структурированный before/after поверх
// audit_log (internal/changelog, go-platform). Карта объекта/сотрудника
// читает Postgres напрямую (ChangeHistoryItem); кросс-сущностный
// low-code поиск идёт через ClickHouse (ChangeHistorySearchResult).
export interface ChangeHistoryItem {
  id: number;
  occurred_at: string;
  actor: string;
  actor_role: string;
  action: string;
  result: string;
  before_json: string;
  after_json: string;
}

// Раздел «Использование ИИ» — умная маршрутизация на основе истории:
// подсказка по подписке для сотрудника без подписок, построенная на
// реальном паттерне подписок коллег из тех же групп.
export interface SubscriptionSuggestion {
  subsidiary: string | null;
  service_id: string | null;
  priority_threshold: string | null;
  peer_count: number;
  explanation: string;
}

export interface ChangeHistoryField {
  label: string;
  kind: "string" | "datetime";
  ops: string[];
}

export interface ChangeHistoryCondition {
  field: string;
  op: string;
  value: string;
}

export interface ChangeHistorySearchRequest {
  match: "all" | "any";
  conditions: ChangeHistoryCondition[];
  limit?: number;
}

export interface ChangeHistorySearchResult {
  event_id: string;
  occurred_at: string;
  actor: string;
  actor_role: string;
  action: string;
  resource_type: string;
  resource_id: string;
  result: string;
  detail: string;
  before_json: string;
  after_json: string;
}

export interface DemoScenario {
  name: string;
  description: string;
}
