import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { api, IncidentListItem } from "../api";
import { EmptyState, PageHeader, PriorityBadge, StatusBadge } from "../components/ui";

export default function Incidents() {
  const [params, setParams] = useSearchParams();
  const status = params.get("status");

  const { data, isLoading } = useQuery<IncidentListItem[]>({
    queryKey: ["incidents", status],
    queryFn: () => api.get<IncidentListItem[]>(`/incidents${status ? `?status=${status}` : ""}`),
  });

  return (
    <div>
      <PageHeader title="Инциденты" subtitle="Текущие и завершённые инциденты — свёртка проблем по первопричине" />

      <div className="mb-4 flex gap-2">
        {[
          { label: "Все", value: null },
          { label: "Активные", value: "open" },
          { label: "Завершённые", value: "closed" },
        ].map((f) => (
          <button
            key={f.label}
            onClick={() => setParams(f.value ? { status: f.value } : {})}
            className={`rounded-md px-3 py-1.5 text-sm ${
              status === f.value ? "bg-accent text-white" : "bg-card text-muted hover:text-fg"
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && data?.length === 0 && <EmptyState>Инцидентов не найдено</EmptyState>}

      {!!data?.length && (
        <div className="overflow-hidden rounded-xl border border-border">
          <table className="w-full text-sm">
            <thead className="bg-card text-left text-xs text-muted">
              <tr>
                <th className="px-4 py-2">ID</th>
                <th className="px-4 py-2">Приоритет</th>
                <th className="px-4 py-2">Первопричина</th>
                <th className="px-4 py-2">Статус</th>
                <th className="px-4 py-2">Участников</th>
                <th className="px-4 py-2">Открыт</th>
              </tr>
            </thead>
            <tbody>
              {data.map((inc) => (
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
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
