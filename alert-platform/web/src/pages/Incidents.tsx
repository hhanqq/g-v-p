import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { api, IncidentListResponse } from "../api";
import { EmptyState, formatDuration, PageHeader, PriorityBadge, StatusBadge, useNow } from "../components/ui";

// Вкладки Активные/Завершённые/Все (раздел «Инциденты» доп. ТЗ). Единственный
// источник правды жизненного цикла инцидента — incidents.closed_at
// (см. комментарий в go-platform/internal/adminapi/server.go::listIncidents):
// закрывается только когда ВСЕ проблемы-участники реально RESOLVED, поэтому
// это не то же самое, что status первопричины (root.status).
// value — то, что уходит в URL и в ?status= на бэкенд; "all" на бэкенд не
// передаётся (означает «без фильтра»). Дефолт при отсутствии параметра —
// "open" (раздел «Инциденты» доп. ТЗ: вкладка по умолчанию — Активные).
const TABS = [
  { label: "Активные", value: "open" },
  { label: "Завершённые", value: "closed" },
  { label: "Все", value: "all" },
] as const;

export default function Incidents() {
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "open";
  const now = useNow();

  const { data, isLoading } = useQuery<IncidentListResponse>({
    queryKey: ["incidents", status],
    queryFn: () => api.get<IncidentListResponse>(`/incidents${status !== "all" ? `?status=${status}` : ""}`),
  });
  const counts = data?.counts;
  const items = data?.items ?? [];

  return (
    <div>
      <PageHeader title="Инциденты" />

      <div className="mb-4 flex gap-2">
        {TABS.map((tab) => {
          const count =
            tab.value === "open" ? counts?.active : tab.value === "closed" ? counts?.closed : counts?.total;
          return (
            <button
              key={tab.label}
              onClick={() => setParams({ status: tab.value })}
              className={`rounded-md px-3 py-1.5 text-sm tabular-nums ${
                status === tab.value ? "bg-accent text-white" : "bg-card text-muted hover:text-fg"
              }`}
            >
              {tab.label}
              {count !== undefined ? ` (${count})` : ""}
            </button>
          );
        })}
      </div>

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && items.length === 0 && <EmptyState>Инцидентов не найдено</EmptyState>}

      {!!items.length && (
        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full min-w-[820px] text-sm">
            <thead className="bg-card text-left text-xs text-muted">
              <tr>
                <th className="px-4 py-2">ID</th>
                <th className="px-4 py-2">Приоритет</th>
                <th className="px-4 py-2">Первопричина</th>
                <th className="px-4 py-2">Статус</th>
                <th className="px-4 py-2">Участников</th>
                <th className="px-4 py-2">Открыт</th>
                <th className="px-4 py-2">Длительность</th>
              </tr>
            </thead>
            <tbody>
              {items.map((inc) => (
                <tr key={inc.id} className="border-t border-border hover:bg-fg/5">
                  <td className="px-4 py-2">
                    <Link to={`/incidents/${inc.id}`} className="text-accent">
                      INC-{String(inc.id).padStart(4, "0")}
                    </Link>
                  </td>
                  <td className="px-4 py-2"><PriorityBadge priority={inc.priority} /></td>
                  <td className="px-4 py-2 text-muted">
                    {inc.root_object_id} · {inc.root_symptom_class}
                  </td>
                  <td className="px-4 py-2"><StatusBadge status={inc.status} /></td>
                  <td className="px-4 py-2 tabular-nums">{inc.member_count}</td>
                  <td className="px-4 py-2 text-muted">{new Date(inc.opened_at).toLocaleString("ru-RU")}</td>
                  <td className="px-4 py-2 tabular-nums text-muted">
                    {inc.closed_at
                      ? formatDuration(inc.opened_at, new Date(inc.closed_at).getTime())
                      : formatDuration(inc.opened_at, now)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
