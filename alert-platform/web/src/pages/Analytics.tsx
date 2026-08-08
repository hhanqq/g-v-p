import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, AnalyticsSummary } from "../api";
import { Card, PageHeader, PriorityBadge, StatTile } from "../components/ui";

function formatDuration(seconds: number | null): string {
  if (seconds == null) return "—";
  const s = Math.round(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h) return `${h} ч ${m} мин`;
  if (m) return `${m} мин`;
  return `${s} с`;
}

function Sparkline({ series }: { series: [string, number][] }) {
  const max = Math.max(1, ...series.map(([, count]) => count));
  return (
    <div className="flex h-32 items-end gap-1">
      {series.map(([date, count]) => (
        <div key={date} className="group relative flex-1">
          <div
            className="rounded-t bg-accent/70 transition-colors group-hover:bg-accent"
            style={{ height: `${Math.max(2, (count / max) * 100)}%` }}
          />
          <div className="pointer-events-none absolute -top-6 left-1/2 hidden -translate-x-1/2 whitespace-nowrap rounded bg-bg px-1.5 py-0.5 text-[10px] text-fg group-hover:block">
            {date.slice(5)} · {count}
          </div>
        </div>
      ))}
    </div>
  );
}

export default function Analytics() {
  const { data, isLoading } = useQuery<AnalyticsSummary>({
    queryKey: ["analytics-summary"],
    queryFn: () => api.get<AnalyticsSummary>("/analytics/summary"),
  });

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  return (
    <div>
      <PageHeader title="Аналитика" subtitle="Историческая статистика по алертам, инцидентам и SLA" />

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <StatTile label="Среднее устранение (MTTR)" value={formatDuration(data.avg_mttr_seconds)} />
        <StatTile label="Резолвинг объектов, %" value={data.resolution_coverage_pct ?? "—"} />
        <StatTile label="Открытых проблем" value={data.incidents.open_problems} />
        <StatTile label="Нарушений SLA всего" value={data.sla_breach.total} />
      </div>

      <Card className="mt-4">
        <h3 className="mb-3 text-sm font-semibold">Алерты за последние 14 дней</h3>
        <Sparkline series={data.alerts_over_time} />
      </Card>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Самые проблемные объекты</h3>
          {data.top_problem_objects.length === 0 && <p className="text-sm text-muted">Данных пока нет.</p>}
          <ul className="space-y-2">
            {data.top_problem_objects.map((o) => (
              <li key={o.object_id} className="flex items-center justify-between text-sm">
                <Link to={`/equipment/${o.object_id}`} className="text-accent hover:underline">
                  {o.name}{o.site ? ` · ${o.site}` : ""}
                </Link>
                <span className="tabular-nums text-muted">{o.count}</span>
              </li>
            ))}
          </ul>
        </Card>

        <Card>
          <h3 className="mb-3 text-sm font-semibold">Частые типы событий</h3>
          <ul className="space-y-2">
            {data.top_symptoms.map(([cls, count]) => (
              <li key={cls} className="flex items-center justify-between text-sm">
                <span className="text-muted">{cls}</span>
                <span className="tabular-nums">{count}</span>
              </li>
            ))}
            {data.top_symptoms.length === 0 && <p className="text-sm text-muted">Данных пока нет.</p>}
          </ul>
        </Card>
      </div>

      <Card className="mt-4">
        <h3 className="mb-3 text-sm font-semibold">Нарушения SLA по приоритету</h3>
        {data.sla_breach.total === 0 ? (
          <p className="text-sm text-muted">Нарушений не зафиксировано.</p>
        ) : (
          <div className="flex flex-wrap gap-3">
            {Object.entries(data.sla_breach.by_priority).map(([priority, count]) => (
              <div key={priority} className="flex items-center gap-2 rounded-md bg-bg px-3 py-1.5">
                <PriorityBadge priority={priority === "?" ? null : priority} />
                <span className="text-sm tabular-nums">{count}</span>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
