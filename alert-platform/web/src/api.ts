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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
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
};

export interface CurrentUser {
  username: string;
  is_admin: boolean;
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
}

export interface IncidentDetail {
  id: number;
  priority: string | null;
  opened_at: string;
  closed_at: string | null;
  root_problem_id: number;
  members: IncidentMember[];
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
  resolved: boolean;
  title: string;
  occurred_at: string;
  problem_id: number | null;
}

export interface EquipmentListItem {
  id: string;
  kind: string;
  equipment_type: string | null;
  site: string;
  name: string;
  fqdn: string | null;
  ip: string | null;
}

export interface EquipmentDetail extends EquipmentListItem {
  subnet: string | null;
  install_date: string | null;
  spec_json: string | null;
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

export interface EmployeeDetail extends EmployeeListItem {
  subscriptions: { id: number; subsidiary: string | null; service_id: string | null; priority_threshold: string | null }[];
  availability_history: { id: number; status: string; valid_from: string; valid_until: string | null; source: string; note: string | null }[];
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

export interface SlaRuleItem {
  id: number;
  name: string;
  priority: string;
  subsidiary: string | null;
  service_id: string | null;
  response_minutes: number;
  resolution_minutes: number;
}

export interface IntegrationStatus {
  name: string;
  status: "active" | "planned" | "open_question";
  detail: string;
}
