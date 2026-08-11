import { useQuery } from "@tanstack/react-query";
import { api, AuditItem } from "../api";
import { EmptyState, PageHeader } from "../components/ui";

export default function Audit() {
  const { data } = useQuery<AuditItem[]>({ queryKey: ["audit"], queryFn: () => api.get("/audit?limit=200") });
  return (
    <div>
      <PageHeader title="Аудит действий" />
      {data?.length === 0 && <EmptyState>Журнал пока пуст</EmptyState>}
      {!!data?.length && <div className="overflow-x-auto rounded-xl border border-border"><table className="w-full min-w-[640px] text-sm">
        <thead className="bg-card text-left text-xs text-muted"><tr><th className="px-4 py-2">Время</th><th className="px-4 py-2">Пользователь</th><th className="px-4 py-2">Действие</th><th className="px-4 py-2">Объект / детали</th></tr></thead>
        <tbody>{data.map((row) => <tr key={row.id} className="border-t border-border"><td className="px-4 py-2 text-muted">{new Date(row.created_at).toLocaleString("ru-RU")}</td><td className="px-4 py-2">{row.actor}</td><td className="px-4 py-2">{row.action}</td><td className="px-4 py-2 text-muted">{[row.target, row.detail].filter(Boolean).join(" — ") || "—"}</td></tr>)}</tbody>
      </table></div>}
    </div>
  );
}
