import { useQuery } from "@tanstack/react-query";
import { api, HomeSummary } from "../api";
import { Card, PageHeader, StatTile } from "../components/ui";

export default function Home() {
  const { data, isLoading } = useQuery<HomeSummary>({
    queryKey: ["home-summary"],
    queryFn: () => api.get<HomeSummary>("/home/summary"),
    refetchInterval: 15000,
  });

  if (isLoading || !data) {
    return <div className="text-sm text-muted">Загрузка…</div>;
  }

  const pending = (data.queue.pending ?? 0) + (data.queue.processing ?? 0);

  return (
    <div>
      <PageHeader title="Главная" subtitle="Состояние системы прямо сейчас — обновляется каждые 15 секунд" />

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        <StatTile label="Очередь сейчас" value={pending} />
        <StatTile label="Сигналов принято" value={data.events.signals} />
        <StatTile label="Открытых проблем" value={data.incidents.open_problems} />
        <StatTile label="Инцидентов" value={data.incidents.incidents} />
        <StatTile label="Доставлено, %" value={data.delivery.delivered_pct ?? 0} />
      </div>

      <div className="mt-4 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        <StatTile label="Разбор, %" value={data.events.parse_success_rate ?? 0} />
        <StatTile label="Резолвинг, %" value={data.resolution_coverage_pct ?? 0} />
        <StatTile label="ИИ: дублей найдено" value={data.ai_scenarios.duplicates_detected} />
        <StatTile label="ИИ: гипотез первопричины" value={data.ai_scenarios.root_cause_hypotheses} />
        <StatTile label="ИИ-сообщений отправлено" value={data.ai_scenarios.ai_supplements_sent} />
      </div>

      <div className="mt-6 grid gap-4 lg:grid-cols-2">
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Частые типы событий</h3>
          <ul className="space-y-2">
            {data.top_symptoms.map(([cls, count]) => (
              <li key={cls} className="flex items-center justify-between text-sm">
                <span className="text-muted">{cls}</span>
                <span className="tabular-nums">{count}</span>
              </li>
            ))}
          </ul>
        </Card>
        <Card>
          <h3 className="mb-3 text-sm font-semibold">Приоритеты</h3>
          <ul className="space-y-2">
            {["P0", "P1", "P2", "P3"].map((p) => (
              <li key={p} className="flex items-center justify-between text-sm">
                <span className="text-muted">{p}</span>
                <span className="tabular-nums">{data.priority_distribution[p] ?? 0}</span>
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </div>
  );
}
