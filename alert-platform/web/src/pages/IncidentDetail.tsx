import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api, IncidentDetail as IncidentDetailType } from "../api";
import { Card, PageHeader, PriorityBadge, StatusBadge } from "../components/ui";

export default function IncidentDetail() {
  const { id } = useParams();
  const { data, isLoading } = useQuery<IncidentDetailType>({
    queryKey: ["incident", id],
    queryFn: () => api.get<IncidentDetailType>(`/incidents/${id}`),
  });

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  const root = data.members.find((m) => m.role === "root");
  const symptoms = data.members.filter((m) => m.role !== "root");

  return (
    <div>
      <Link to="/incidents" className="text-sm text-accent">← к списку инцидентов</Link>
      <PageHeader
        title={`INC-${String(data.id).padStart(4, "0")}`}
        subtitle={`Открыт ${new Date(data.opened_at).toLocaleString("ru-RU")}${
          data.closed_at ? ` · закрыт ${new Date(data.closed_at).toLocaleString("ru-RU")}` : ""
        }`}
      />

      {root && (
        <Card className="mb-4">
          <div className="mb-2 flex items-center gap-2">
            <span className="text-xs uppercase text-muted">Первопричина</span>
            <PriorityBadge priority={root.priority} />
            <StatusBadge status={root.status} />
          </div>
          <div className="text-sm">{root.object_id} · {root.symptom_class}</div>
          {root.acknowledged_at && (
            <div className="mt-1 text-xs text-emerald-400">
              отреагировал: {root.acknowledged_by}, {new Date(root.acknowledged_at).toLocaleString("ru-RU")}
            </div>
          )}
          {root.ai_root_cause_hypothesis && (
            <div className="mt-3 rounded-md bg-accent/10 p-3 text-sm italic text-fg">
              Гипотеза ИИ: {root.ai_root_cause_hypothesis}
            </div>
          )}
        </Card>
      )}

      <h3 className="mb-2 text-sm font-semibold">Связанные события ({symptoms.length})</h3>
      <div className="space-y-2">
        {symptoms.map((m) => (
          <Card key={m.problem_id} className="flex items-center justify-between">
            <div>
              <div className="text-sm">{m.object_id} · {m.symptom_class}</div>
              {m.rule_id && <div className="text-xs text-muted">по правилу {m.rule_id}</div>}
              {m.acknowledged_at && (
                <div className="text-xs text-emerald-400">
                  отреагировал: {m.acknowledged_by}, {new Date(m.acknowledged_at).toLocaleString("ru-RU")}
                </div>
              )}
            </div>
            <div className="flex items-center gap-2">
              <PriorityBadge priority={m.priority} />
              <StatusBadge status={m.status} />
            </div>
          </Card>
        ))}
        {symptoms.length === 0 && <p className="text-sm text-muted">Дополнительных событий нет.</p>}
      </div>
    </div>
  );
}
