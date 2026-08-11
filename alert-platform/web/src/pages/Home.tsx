import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api, HomeOverview } from "../api";
import { Card, PageHeader, StatTile } from "../components/ui";
import Sparkline from "../components/Sparkline";

const PRIORITY_COLOR: Record<string, string> = { P0: "#ef4444", P1: "#f97316", P2: "#eab308", P3: "#94a3b8" };
const STATUS_CLASS: Record<string, string> = {
  normal: "bg-emerald-500/15 text-emerald-400",
  degraded: "bg-amber-500/15 text-amber-400",
  unknown: "bg-slate-500/15 text-muted",
};
const NEEDS_ATTENTION_ICON: Record<string, string> = {
  no_reaction: "Нет реакции",
  coverage_gap: "Покрытие",
  delivery_backlog: "Доставка",
};

// Селектор периода графика «Алерты» (раздел «Главная» доп. ТЗ): гранулярность
// бакетов считает бэкенд по факту длительности периода (parseHomePeriod/
// bucketPlan в go-platform), фронтенд только выбирает from/to и отражает
// выбор в URL — тысячи сырых точек в браузер не попадают ни при каком period.
const PERIOD_TABS = [
  { label: "24ч", value: "24h" },
  { label: "7д", value: "7d" },
  { label: "30д", value: "30d" },
] as const;

export default function Home() {
  const [params, setParams] = useSearchParams();
  const period = params.get("period") ?? "24h";
  const customFrom = params.get("from") ?? "";
  const customTo = params.get("to") ?? "";

  const query = new URLSearchParams({ period });
  if (period === "custom") {
    if (customFrom) query.set("from", new Date(customFrom).toISOString());
    if (customTo) query.set("to", new Date(customTo).toISOString());
  }

  const { data, isLoading } = useQuery<HomeOverview>({
    queryKey: ["home-overview", period, customFrom, customTo],
    queryFn: () => api.get<HomeOverview>(`/home/overview?${query.toString()}`),
    refetchInterval: 30000,
  });

  const chartData = useMemo(() => {
    if (!data) return [];
    const byBucket = new Map<string, Record<string, number | string>>();
    for (const point of data.alerts_series) {
      if (!byBucket.has(point.bucket)) byBucket.set(point.bucket, { bucket: point.bucket, P0: 0, P1: 0, P2: 0, P3: 0 });
      const row = byBucket.get(point.bucket)!;
      if (point.priority in PRIORITY_COLOR) row[point.priority] = point.count;
    }
    return Array.from(byBucket.values());
  }, [data]);

  if (isLoading || !data) {
    return <div className="text-sm text-muted">Загрузка…</div>;
  }

  return (
    <div>
      <PageHeader title="Главная" />

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile label="Открытых инцидентов" value={data.kpis.open_incidents} />
        <StatTile label="Критических P0/P1" value={data.kpis.critical_active} />
        <StatTile label="Без реакции" value={data.kpis.no_reaction} />
        <StatTile label="Нарушений SLA сегодня" value={data.kpis.sla_breaches_today} />
      </div>

      <Card className="mt-4">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-sm font-semibold">Алерты за период</h3>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex gap-1">
              {PERIOD_TABS.map((tab) => (
                <button
                  key={tab.value}
                  onClick={() => setParams({ period: tab.value })}
                  className={`rounded-md px-2.5 py-1 text-xs ${
                    period === tab.value ? "bg-accent text-white" : "bg-bg text-muted hover:text-fg"
                  }`}
                >
                  {tab.label}
                </button>
              ))}
              <button
                onClick={() => setParams({ period: "custom", from: customFrom, to: customTo })}
                className={`rounded-md px-2.5 py-1 text-xs ${
                  period === "custom" ? "bg-accent text-white" : "bg-bg text-muted hover:text-fg"
                }`}
              >
                Произвольный
              </button>
            </div>
            {period === "custom" && (
              <div className="flex items-center gap-1.5 text-xs">
                <input
                  type="date"
                  value={customFrom}
                  onChange={(e) => setParams({ period: "custom", from: e.target.value, to: customTo })}
                  className="rounded-md border border-border bg-bg px-2 py-1"
                />
                <span className="text-muted">—</span>
                <input
                  type="date"
                  value={customTo}
                  onChange={(e) => setParams({ period: "custom", from: customFrom, to: e.target.value })}
                  className="rounded-md border border-border bg-bg px-2 py-1"
                />
              </div>
            )}
          </div>
        </div>
        {chartData.length === 0 ? (
          <div className="py-8 text-center text-sm text-muted">Событий за выбранный период нет</div>
        ) : (
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} opacity={0.2} />
              <XAxis dataKey="bucket" tick={{ fontSize: 11 }} />
              <YAxis tick={{ fontSize: 11 }} allowDecimals={false} />
              <Tooltip />
              <Legend wrapperStyle={{ fontSize: 11 }} />
              {(["P0", "P1", "P2", "P3"] as const).map((p) => (
                <Bar key={p} dataKey={p} stackId="a" fill={PRIORITY_COLOR[p]} name={p} />
              ))}
            </BarChart>
          </ResponsiveContainer>
        )}
      </Card>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Требует внимания</h3>
          {data.needs_attention.length === 0 ? (
            <div className="text-sm text-muted">Ничего срочного — открытых проблем без реакции и разрывов покрытия не найдено.</div>
          ) : (
            <ul className="space-y-2">
              {data.needs_attention.map((item, i) => (
                <li key={i} className="rounded-lg border border-border p-2.5 text-sm">
                  <div className="flex items-center justify-between">
                    <span className="font-medium">
                      {item.priority && <span className="mr-1.5 rounded bg-red-500/15 px-1.5 py-0.5 text-[10px] text-red-400">{item.priority}</span>}
                      {item.text}
                    </span>
                    <span className="text-[10px] uppercase text-muted">{NEEDS_ATTENTION_ICON[item.kind] ?? item.kind}</span>
                  </div>
                  <div className="text-muted">{item.detail}</div>
                  {item.incident_id && (
                    <Link to={`/incidents/${item.incident_id}`} className="text-xs text-accent hover:underline">
                      INC-{String(item.incident_id).padStart(4, "0")}
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card>
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold">Состояние ADP</h3>
            <Link to="/platform-health" className="text-xs text-accent hover:underline">Подробнее</Link>
          </div>
          <table className="w-full text-sm">
            <tbody>
              {data.adp_health.map((c) => (
                <tr key={c.name} className="border-t border-border first:border-t-0">
                  <td className="py-1.5">{c.name}</td>
                  <td className="py-1.5 text-right">
                    <span className={`rounded px-2 py-0.5 text-xs font-medium ${STATUS_CLASS[c.status]}`}>
                      {c.status === "normal" ? "Normal" : c.status === "degraded" ? "Degraded" : "Нет данных"}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Покрытие</h3>
          <div className="text-2xl font-semibold tabular-nums">
            {data.coverage.critical_fully_covered} / {data.coverage.critical_total}
          </div>
          <div className="text-xs text-muted">критичных объектов имеют полное покрытие прямо сейчас</div>
          {data.coverage.gaps_next_7d > 0 && (
            <div className="mt-2 text-sm text-amber-400">{data.coverage.gaps_next_7d} имеют разрывы на ближайшие 7 дней</div>
          )}
        </Card>
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Сценарии</h3>
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div><div className="text-lg font-semibold tabular-nums">{data.scenarios.active_scenarios}</div><div className="text-xs text-muted">Активных</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{data.scenarios.runs_today}</div><div className="text-xs text-muted">Запусков сегодня</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{data.scenarios.awaiting_reaction}</div><div className="text-xs text-muted">Ожидают реакции</div></div>
            <div><div className="text-lg font-semibold tabular-nums">{data.scenarios.escalations_today}</div><div className="text-xs text-muted">Эскалаций сегодня</div></div>
          </div>
        </Card>
      </div>

      <Card className="mt-4">
        <h3 className="mb-3 text-sm font-semibold">Нагрузка / AI</h3>
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <ResourceStat label="CPU" pct={data.resources.cpu_pct} series={data.resources.cpu_series} />
          <ResourceStat label="RAM" pct={data.resources.ram_pct} series={data.resources.ram_series} />
          <ResourceStat label="Disk" pct={data.resources.disk_pct} series={[]} />
          <div>
            <div className="text-xs text-muted">GPU (VRAM)</div>
            <div className="text-lg font-semibold tabular-nums">
              {data.resources.ai.gpu === "unavailable" ? "Not available" : `${data.resources.ai.gpu.vram_used_gb}/${data.resources.ai.gpu.vram_total_gb} GB`}
            </div>
          </div>
        </div>
      </Card>
    </div>
  );
}

function ResourceStat({ label, pct, series }: { label: string; pct?: number; series: number[] }) {
  return (
    <div>
      <div className="text-xs text-muted">{label}</div>
      <div className="flex items-center gap-2">
        <div className="text-lg font-semibold tabular-nums">{pct != null ? `${pct}%` : "—"}</div>
        <Sparkline values={series} />
      </div>
    </div>
  );
}
