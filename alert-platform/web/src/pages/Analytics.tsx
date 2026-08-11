import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, Line, LineChart, Pie, PieChart,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";
import {
  AlertsTimeseriesResponse, AnalyticsOverview, api, DeliveryAnalytics, EquipmentGroup, EquipmentTopAnalytics,
  IncidentsTimeseriesResponse, ScenarioAnalytics, SLAAnalytics,
} from "../api";
import { Card, PageHeader } from "../components/ui";

const PERIODS = [
  { value: "today", label: "Сегодня" }, { value: "24h", label: "24 часа" }, { value: "7d", label: "7 дней" },
  { value: "14d", label: "14 дней" }, { value: "30d", label: "30 дней" }, { value: "90d", label: "90 дней" },
  { value: "custom", label: "Произвольный" },
];

function todayISO(): string {
  return new Date().toISOString().slice(0, 10);
}

const PRIORITY_COLOR: Record<string, string> = { P0: "#ef4444", P1: "#f97316", P2: "#eab308", P3: "#94a3b8", unknown: "#64748b" };
const SOURCE_COLOR: Record<string, string> = { zabbix: "#dc2626", solarwinds: "#facc15", other: "#64748b" };
const ACCENT = "#6366f1";

function fmtDuration(seconds: number | null): string {
  if (seconds == null) return "—";
  const s = Math.round(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h) return `${h} ч ${m} мин`;
  if (m) return `${m} мин ${sec} с`;
  return `${sec} с`;
}
function pct(value: number | null): string {
  return value == null ? "—" : `${value}%`;
}

function Kpi({ label, value }: { label: string; value: string | number }) {
  return (
    <Card>
      <div className="text-2xl font-semibold tabular-nums">{value}</div>
      <div className="mt-1 text-xs text-muted">{label}</div>
    </Card>
  );
}

function ChartCard({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <Card>
      <h3 className="text-sm font-semibold">{title}</h3>
      {subtitle && <p className="mb-2 text-xs text-muted">{subtitle}</p>}
      <div className="mt-2">{children}</div>
    </Card>
  );
}

function AlertsChart({ period, site }: { period: string; site: string }) {
  const [groupBy, setGroupBy] = useState<"priority" | "source">("priority");
  const { data } = useQuery<AlertsTimeseriesResponse>({
    queryKey: ["analytics-alerts-ts", period, site, groupBy],
    queryFn: () => api.get<AlertsTimeseriesResponse>(`/analytics/alerts-timeseries?${period}&groupby=${groupBy}`),
  });

  const { rows, keys } = useMemo(() => {
    const byDay = new Map<string, Record<string, number>>();
    const keySet = new Set<string>();
    for (const point of data?.series ?? []) {
      if (!byDay.has(point.day)) byDay.set(point.day, {});
      byDay.get(point.day)![point.key] = point.count;
      keySet.add(point.key);
    }
    return {
      rows: Array.from(byDay.entries()).map(([day, values]) => ({ day, ...values })),
      keys: Array.from(keySet),
    };
  }, [data]);
  const colors = groupBy === "priority" ? PRIORITY_COLOR : SOURCE_COLOR;

  return (
    <Card>
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold">Алерты за период</h3>
        <div className="flex gap-1">
          {(["priority", "source"] as const).map((g) => (
            <button
              key={g}
              onClick={() => setGroupBy(g)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium ${groupBy === g ? "bg-accent text-white" : "bg-bg text-muted"}`}
            >
              {g === "priority" ? "По приоритетам" : "По источникам"}
            </button>
          ))}
        </div>
      </div>
      <ResponsiveContainer width="100%" height={280}>
        <BarChart data={rows}>
          <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
          <XAxis dataKey="day" tick={{ fontSize: 11 }} tickFormatter={(d) => d.slice(5)} />
          <YAxis tick={{ fontSize: 11 }} />
          <Tooltip contentStyle={{ fontSize: 12 }} />
          {keys.map((k) => (
            <Bar key={k} dataKey={k} stackId="a" fill={colors[k] ?? ACCENT} />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </Card>
  );
}

function IncidentsSection({ period }: { period: string }) {
  const { data } = useQuery<IncidentsTimeseriesResponse>({
    queryKey: ["analytics-incidents-ts", period],
    queryFn: () => api.get<IncidentsTimeseriesResponse>(`/analytics/incidents-timeseries?${period}`),
  });
  const donut = data
    ? [
        { name: "Открыты", value: data.open_vs_closed.open, color: "#ef4444" },
        { name: "В работе", value: data.open_vs_closed.in_progress, color: "#f59e0b" },
        { name: "Закрыты", value: data.open_vs_closed.closed, color: "#22c55e" },
      ]
    : [];

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <ChartCard title="Инциденты: создано / закрыто">
        <ResponsiveContainer width="100%" height={220}>
          <BarChart data={data?.series ?? []}>
            <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
            <XAxis dataKey="day" tick={{ fontSize: 11 }} tickFormatter={(d) => d.slice(5)} />
            <YAxis tick={{ fontSize: 11 }} />
            <Tooltip contentStyle={{ fontSize: 12 }} />
            <Bar dataKey="created" fill={ACCENT} name="Создано" />
            <Bar dataKey="closed" fill="#22c55e" name="Закрыто" />
          </BarChart>
        </ResponsiveContainer>
      </ChartCard>
      <ChartCard title="Открыты / В работе / Закрыты" subtitle="Снимок на текущий момент, по факту acknowledged_at/closed_at">
        <div className="flex items-center gap-6">
          <ResponsiveContainer width={160} height={160}>
            <PieChart>
              <Pie data={donut} dataKey="value" innerRadius={40} outerRadius={70}>
                {donut.map((d) => <Cell key={d.name} fill={d.color} />)}
              </Pie>
              <Tooltip contentStyle={{ fontSize: 12 }} />
            </PieChart>
          </ResponsiveContainer>
          <div className="space-y-1.5 text-sm">
            {donut.map((d) => (
              <div key={d.name} className="flex items-center gap-2">
                <span className="h-2.5 w-2.5 rounded-sm" style={{ background: d.color }} />
                {d.name}: <span className="font-medium tabular-nums">{d.value}</span>
              </div>
            ))}
          </div>
        </div>
      </ChartCard>
    </div>
  );
}

function DeliverySection({ period }: { period: string }) {
  const { data } = useQuery<DeliveryAnalytics>({
    queryKey: ["analytics-delivery", period],
    queryFn: () => api.get<DeliveryAnalytics>(`/analytics/delivery?${period}`),
  });
  if (!data) return null;
  const { trueconf, email } = data;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <h3 className="mb-3 text-sm font-semibold">TrueConf</h3>
          <div className="grid grid-cols-3 gap-3 text-sm">
            <div><div className="text-lg font-semibold tabular-nums">{trueconf.sent}</div><div className="text-xs text-muted">Отправлено</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{pct(trueconf.ack_rate_pct)}</div><div className="text-xs text-muted">ACK rate</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{fmtDuration(trueconf.mtta_seconds)}</div><div className="text-xs text-muted">Среднее время ACK</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{pct(trueconf.success_rate_pct)}</div><div className="text-xs text-muted">Успешно</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{trueconf.acknowledged}</div><div className="text-xs text-muted">Подтверждено</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{trueconf.escalations}</div><div className="text-xs text-muted">Эскалации</div></div>
          </div>
        </Card>
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Email</h3>
          <p className="mb-2 text-xs text-muted">
            «Отправлено» — SMTP принял письмо. Открытие — по загрузке трекинг-пикселя (не абсолютно точно:
            блокировка картинок/прокси/кеш почтового клиента), клик — надёжнее.
          </p>
          <div className="grid grid-cols-3 gap-3 text-sm">
            <div><div className="text-lg font-semibold tabular-nums">{email.sent}</div><div className="text-xs text-muted">Отправлено</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{pct(email.open_rate_pct)}</div><div className="text-xs text-muted">Open rate</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{pct(email.ctr_pct)}</div><div className="text-xs text-muted">CTR</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{email.opened}</div><div className="text-xs text-muted">Открыто</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{email.clicked}</div><div className="text-xs text-muted">Кликнули</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{pct(email.ctor_pct)}</div><div className="text-xs text-muted">CTOR</div></div>
          </div>
        </Card>
      </div>

      <Card>
        <h3 className="mb-2 text-sm font-semibold">Сравнение каналов</h3>
        <p className="mb-2 text-xs text-muted">ACK (TrueConf) и Click (Email) — разные действия, не сравниваем как одну метрику.</p>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted">
              <tr><th className="py-1">Канал</th><th className="text-right">Отправлено</th><th className="text-right">Успешно</th><th className="text-right">Interaction</th></tr>
            </thead>
            <tbody>
              <tr className="border-t border-border">
                <td className="py-1.5">TrueConf</td>
                <td className="text-right tabular-nums">{trueconf.sent}</td>
                <td className="text-right tabular-nums">{pct(trueconf.success_rate_pct)}</td>
                <td className="text-right tabular-nums">ACK {pct(trueconf.ack_rate_pct)}</td>
              </tr>
              <tr className="border-t border-border">
                <td className="py-1.5">Email</td>
                <td className="text-right tabular-nums">{email.sent}</td>
                <td className="text-right tabular-nums">{email.created > 0 ? `${Math.round((email.sent / email.created) * 100)}%` : "—"}</td>
                <td className="text-right tabular-nums">Click {pct(email.ctr_pct)}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </Card>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <ChartCard title="ACK rate во времени">
          <ResponsiveContainer width="100%" height={180}>
            <LineChart data={data.ack_rate_series}>
              <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
              <XAxis dataKey="day" tick={{ fontSize: 10 }} tickFormatter={(d) => d.slice(5)} />
              <YAxis tick={{ fontSize: 10 }} domain={[0, 100]} />
              <Tooltip contentStyle={{ fontSize: 12 }} />
              <Line type="monotone" dataKey="ack_rate_pct" stroke={ACCENT} dot={false} connectNulls />
            </LineChart>
          </ResponsiveContainer>
        </ChartCard>
        <ChartCard title="ACK rate по приоритету">
          <ResponsiveContainer width="100%" height={180}>
            <BarChart data={data.ack_rate_by_priority} layout="vertical">
              <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
              <XAxis type="number" domain={[0, 100]} tick={{ fontSize: 10 }} />
              <YAxis type="category" dataKey="priority" tick={{ fontSize: 11 }} width={30} />
              <Tooltip contentStyle={{ fontSize: 12 }} />
              <Bar dataKey="ack_rate_pct">
                {data.ack_rate_by_priority.map((p) => <Cell key={p.priority} fill={PRIORITY_COLOR[p.priority] ?? ACCENT} />)}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </ChartCard>
        <ChartCard title="Распределение MTTA">
          <ResponsiveContainer width="100%" height={180}>
            <BarChart data={data.mtta_distribution}>
              <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
              <XAxis dataKey="bucket" tick={{ fontSize: 9 }} interval={0} angle={-20} textAnchor="end" height={50} />
              <YAxis tick={{ fontSize: 10 }} />
              <Tooltip contentStyle={{ fontSize: 12 }} />
              <Bar dataKey="count" fill={ACCENT} />
            </BarChart>
          </ResponsiveContainer>
        </ChartCard>
      </div>
    </div>
  );
}

function SLASection({ period }: { period: string }) {
  const { data } = useQuery<SLAAnalytics>({
    queryKey: ["analytics-sla", period],
    queryFn: () => api.get<SLAAnalytics>(`/analytics/sla?${period}`),
  });
  if (!data) return null;
  return (
    <ChartCard title="SLA" subtitle={`Применимо: ${data.applicable} · Нарушено: ${data.breached}`}>
      <div className="mb-3 flex items-center gap-4">
        <div className="text-3xl font-semibold tabular-nums text-emerald-400">{pct(data.compliance_pct)}</div>
        <div className="text-sm text-muted">SLA соблюдён</div>
      </div>
      <ResponsiveContainer width="100%" height={140}>
        <AreaChart data={data.breach_series}>
          <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
          <XAxis dataKey="day" tick={{ fontSize: 10 }} tickFormatter={(d) => d.slice(5)} />
          <YAxis tick={{ fontSize: 10 }} />
          <Tooltip contentStyle={{ fontSize: 12 }} />
          <Area type="monotone" dataKey="breaches" stroke="#ef4444" fill="#ef444433" name="Нарушения" />
        </AreaChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

function EquipmentTopSection({ period }: { period: string }) {
  const { data } = useQuery<EquipmentTopAnalytics>({
    queryKey: ["analytics-equipment-top", period],
    queryFn: () => api.get<EquipmentTopAnalytics>(`/analytics/equipment-top?${period}`),
  });
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <ChartCard title="Топ проблемного оборудования">
        <ResponsiveContainer width="100%" height={220}>
          <BarChart data={data?.top_objects ?? []} layout="vertical" margin={{ left: 24 }}>
            <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
            <XAxis type="number" tick={{ fontSize: 10 }} />
            <YAxis type="category" dataKey="name" tick={{ fontSize: 10 }} width={110} />
            <Tooltip contentStyle={{ fontSize: 12 }} />
            <Bar dataKey="count" fill={ACCENT} />
          </BarChart>
        </ResponsiveContainer>
        <div className="mt-1 flex flex-wrap gap-2">
          {data?.top_objects.slice(0, 5).map((o) => (
            <Link key={o.object_id} to={`/equipment/${encodeURIComponent(o.object_id)}`} className="text-xs text-accent hover:underline">
              {o.name} →
            </Link>
          ))}
        </div>
      </ChartCard>
      <ChartCard title="Частые типы событий (symptom_class)">
        <ResponsiveContainer width="100%" height={220}>
          <BarChart data={data?.top_symptoms ?? []} layout="vertical" margin={{ left: 24 }}>
            <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
            <XAxis type="number" tick={{ fontSize: 10 }} />
            <YAxis type="category" dataKey="symptom_class" tick={{ fontSize: 10 }} width={110} />
            <Tooltip contentStyle={{ fontSize: 12 }} />
            <Bar dataKey="count" fill="#8b5cf6" />
          </BarChart>
        </ResponsiveContainer>
      </ChartCard>
    </div>
  );
}

function ScenariosSection({ period }: { period: string }) {
  const { data } = useQuery<ScenarioAnalytics>({
    queryKey: ["analytics-scenarios", period],
    queryFn: () => api.get<ScenarioAnalytics>(`/analytics/scenarios?${period}`),
  });
  if (!data) return null;
  return (
    <ChartCard title="Сценарии">
      <div className="mb-3 grid grid-cols-2 gap-3 sm:grid-cols-5 text-sm">
        <div><div className="text-lg font-semibold tabular-nums">{data.total_runs}</div><div className="text-xs text-muted">Запусков</div></div>
        <div><div className="text-lg font-semibold tabular-nums">{data.done_runs}</div><div className="text-xs text-muted">Завершено</div></div>
        <div><div className="text-lg font-semibold tabular-nums">{data.escalated_runs}</div><div className="text-xs text-muted">С эскалацией</div></div>
        <div><div className="text-lg font-semibold tabular-nums">{data.avg_steps != null ? data.avg_steps.toFixed(1) : "—"}</div><div className="text-xs text-muted">Средних шагов</div></div>
        <div><div className="text-lg font-semibold tabular-nums">{pct(data.resolved_without_escalation_pct)}</div><div className="text-xs text-muted">Без эскалации</div></div>
      </div>
      {data.top_scenarios.length > 0 && (
        <ResponsiveContainer width="100%" height={Math.max(120, data.top_scenarios.length * 32)}>
          <BarChart data={data.top_scenarios} layout="vertical" margin={{ left: 24 }}>
            <CartesianGrid strokeDasharray="3 3" opacity={0.15} />
            <XAxis type="number" tick={{ fontSize: 10 }} />
            <YAxis type="category" dataKey="name" tick={{ fontSize: 10 }} width={160} />
            <Tooltip contentStyle={{ fontSize: 12 }} />
            <Bar dataKey="runs" fill={ACCENT} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </ChartCard>
  );
}

function NoiseReductionSection({ overview }: { overview?: AnalyticsOverview }) {
  if (!overview) return null;
  const f = overview.noise_funnel;
  const steps = [
    { label: "Исходных событий", value: f.raw_events },
    { label: "После дедупликации", value: f.deduplicated },
    { label: "Сформировано инцидентов", value: f.incidents },
    { label: "Уведомлений отправлено", value: f.notifications_sent },
  ];
  const max = Math.max(1, ...steps.map((s) => s.value));
  return (
    <ChartCard title="Снижение шума" subtitle="События не теряются — объединяются в уже существующие Problem (repeat_count), не отбрасываются.">
      <div className="space-y-2">
        {steps.map((s) => (
          <div key={s.label} className="flex items-center gap-3">
            <div className="w-40 shrink-0 text-xs text-muted">{s.label}</div>
            <div className="h-5 flex-1 rounded bg-bg">
              <div className="h-5 rounded bg-accent" style={{ width: `${Math.max(2, (s.value / max) * 100)}%` }} />
            </div>
            <div className="w-16 shrink-0 text-right text-sm font-medium tabular-nums">{s.value}</div>
          </div>
        ))}
      </div>
      <p className="mt-3 text-xs text-muted">
        {f.folded_into_existing} повторных событий объединено в уже открытые проблемы (не породили новых уведомлений).
      </p>
    </ChartCard>
  );
}

export default function Analytics() {
  const [params, setParams] = useSearchParams();
  const period = params.get("period") ?? "14d";
  const from = params.get("from") ?? todayISO();
  const to = params.get("to") ?? todayISO();
  const site = params.get("site") ?? "";

  function setPeriod(next: string) {
    const nextParams = new URLSearchParams(params);
    nextParams.set("period", next);
    if (next === "custom") {
      if (!params.get("from")) nextParams.set("from", from);
      if (!params.get("to")) nextParams.set("to", to);
    } else {
      nextParams.delete("from");
      nextParams.delete("to");
    }
    setParams(nextParams, { replace: true });
  }
  function setRange(nextFrom: string, nextTo: string) {
    const nextParams = new URLSearchParams(params);
    nextParams.set("period", "custom");
    nextParams.set("from", nextFrom);
    nextParams.set("to", nextTo);
    setParams(nextParams, { replace: true });
  }
  function setSite(next: string) {
    const nextParams = new URLSearchParams(params);
    if (next) nextParams.set("site", next);
    else nextParams.delete("site");
    setParams(nextParams, { replace: true });
  }

  // periodQuery — единая точка формирования параметров периода для ВСЕХ
  // секций ниже (раздел 38 доп. ТЗ: не должно быть графика, который
  // продолжает показывать старый период после смены календаря сверху).
  const periodQuery = period === "custom" ? `period=custom&from=${from}&to=${to}` : `period=${period}`;

  const { data: overview } = useQuery<AnalyticsOverview>({
    queryKey: ["analytics-overview", periodQuery, site],
    queryFn: () => api.get<AnalyticsOverview>(`/analytics/overview?${periodQuery}${site ? `&site=${site}` : ""}`),
  });
  const { data: sites } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups", "root"],
    queryFn: () => api.get<EquipmentGroup[]>("/equipment/groups"),
  });

  return (
    <div>
      <PageHeader title="Аналитика" helpArticle="analytics" />

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="flex flex-wrap gap-1.5">
          {PERIODS.map((p) => (
            <button
              key={p.value}
              onClick={() => setPeriod(p.value)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium ${period === p.value ? "bg-accent text-white" : "bg-card text-muted hover:bg-fg/5"}`}
            >
              {p.label}
            </button>
          ))}
        </div>
        {period === "custom" && (
          <div className="flex items-center gap-1 text-xs">
            <input type="date" value={from} max={to} onChange={(e) => setRange(e.target.value, to)} className="rounded-md border border-border bg-bg px-2 py-1" />
            <span className="text-muted">—</span>
            <input type="date" value={to} min={from} max={todayISO()} onChange={(e) => setRange(from, e.target.value)} className="rounded-md border border-border bg-bg px-2 py-1" />
          </div>
        )}
        <select
          value={site}
          onChange={(e) => setSite(e.target.value)}
          className="rounded-md border border-border bg-bg px-2 py-1 text-xs"
        >
          <option value="">Все филиалы</option>
          {sites?.map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
        </select>
        {site && (
          <p className="text-xs text-muted">
            Фильтр по филиалу пока применяется к верхним KPI; графики ниже показывают все филиалы.
          </p>
        )}
      </div>

      <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Kpi label="Алертов" value={overview?.alerts_total ?? "—"} />
        <Kpi label="Инцидентов" value={overview?.incidents_total ?? "—"} />
        <Kpi label="Снижение шума" value={pct(overview?.noise_reduction_pct ?? null)} />
        <Kpi label="MTTA" value={fmtDuration(overview?.mtta_seconds ?? null)} />
        <Kpi label="MTTR" value={fmtDuration(overview?.mttr_seconds ?? null)} />
        <Kpi label="ACK" value={pct(overview?.ack_rate_pct ?? null)} />
      </div>

      <div className="space-y-4">
        <AlertsChart period={periodQuery} site={site} />
        <IncidentsSection period={periodQuery} />
        <DeliverySection period={periodQuery} />
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <SLASection period={periodQuery} />
          <ScenariosSection period={periodQuery} />
        </div>
        <EquipmentTopSection period={periodQuery} />
        <NoiseReductionSection overview={overview} />
      </div>
    </div>
  );
}
