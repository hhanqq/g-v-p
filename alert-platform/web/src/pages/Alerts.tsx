import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, AlertItem } from "../api";
import { EmptyState, PageHeader } from "../components/ui";

export default function Alerts() {
  const [offset, setOffset] = useState(0);
  const limit = 50;

  const { data, isLoading } = useQuery<{ total: number; items: AlertItem[] }>({
    queryKey: ["alerts", offset],
    queryFn: () => api.get(`/alerts?limit=${limit}&offset=${offset}`),
  });

  return (
    <div>
      <PageHeader title="Алерты" />

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && data?.items.length === 0 && <EmptyState>Событий пока нет</EmptyState>}

      {!!data?.items.length && (
        <>
          <div className="overflow-hidden rounded-xl border border-border">
            <table className="w-full text-sm">
              <thead className="bg-card text-left text-xs text-muted">
                <tr>
                  <th className="px-4 py-2">Время</th>
                  <th className="px-4 py-2">Источник</th>
                  <th className="px-4 py-2">Объект</th>
                  <th className="px-4 py-2">Симптом</th>
                  <th className="px-4 py-2">Состояние</th>
                  <th className="px-4 py-2">Резолвинг</th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((a) => (
                  <tr key={a.id} className="border-t border-border hover:bg-fg/5">
                    <td className="px-4 py-2 text-muted">{new Date(a.occurred_at).toLocaleString("ru-RU")}</td>
                    <td className="px-4 py-2">{a.source_system} · {a.source_instance}</td>
                    <td className="px-4 py-2">{a.object_id ?? "—"}</td>
                    <td className="px-4 py-2">
                      {a.symptom_class}
                      {a.symptom_class_source === "ai" && (
                        <span className="ml-1 rounded bg-accent/15 px-1 text-[10px] text-accent">ИИ</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-muted">{a.state}</td>
                    <td className="px-4 py-2">
                      {a.resolved ? (
                        <span className="rounded bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
                          разрешён
                        </span>
                      ) : (
                        <span className="rounded bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-400">
                          карантин
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-3 flex items-center justify-between text-sm text-muted">
            <span>Всего: {data.total}</span>
            <div className="flex gap-2">
              <button
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - limit))}
                className="rounded-md bg-card px-3 py-1 disabled:opacity-40"
              >
                ← назад
              </button>
              <button
                disabled={offset + limit >= data.total}
                onClick={() => setOffset(offset + limit)}
                className="rounded-md bg-card px-3 py-1 disabled:opacity-40"
              >
                вперёд →
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
