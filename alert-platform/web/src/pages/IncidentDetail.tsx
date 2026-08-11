import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, IncidentDetail as IncidentDetailType, RoutingTraceItem } from "../api";
import { Card, formatDuration, PageHeader, PriorityBadge, StatusBadge, useNow } from "../components/ui";

function RoutingTraceCard({ problemId }: { problemId: number }) {
  const { data: trace } = useQuery<RoutingTraceItem[]>({
    queryKey: ["routing-trace", problemId],
    queryFn: () => api.get<RoutingTraceItem[]>(`/problems/${problemId}/routing-trace`),
  });
  const [expanded, setExpanded] = useState<number | null>(null);

  if (!trace || trace.length === 0) return null;

  return (
    <Card className="mb-4">
      <h3 className="mb-3 text-sm font-semibold">Маршрутизация</h3>
      <div className="space-y-1.5">
        {trace.map((item, idx) => {
          const recipient = item.source === "notification" ? item.recipient : item.trace.selected?.join(", ");
          const isOpen = expanded === idx;
          return (
            <div key={idx} className="rounded-md bg-bg px-3 py-2 text-xs">
              <button className="flex w-full items-center justify-between text-left" onClick={() => setExpanded(isOpen ? null : idx)}>
                <span>
                  {new Date(item.at).toLocaleString("ru-RU")} ·{" "}
                  {item.source === "notification" ? item.notification_type : `сценарий «${item.scenario_name}»`} →{" "}
                  <span className="font-medium text-fg">{recipient || "—"}</span>
                  {item.trace.delegated_from && <span className="text-muted"> (делегировано от {item.trace.delegated_from})</span>}
                </span>
                <span className="text-muted">{isOpen ? "▲" : "почему ▼"}</span>
              </button>
              {isOpen && (
                <div className="mt-2 space-y-1 border-t border-border pt-2 text-muted">
                  {item.source === "notification" && (
                    <div>
                      доступность: {item.trace.available === false ? "недоступен" : "доступен"}
                      {item.trace.kind ? ` (${item.trace.kind})` : ""}
                    </div>
                  )}
                  {item.source === "scenario" && (
                    <>
                      <div>узел: {item.node_id}{item.branch ? `, ветка: ${item.branch}` : ""}</div>
                      <div>причина: {item.trace.reason ?? "—"}</div>
                      {item.trace.candidates && item.trace.candidates.length > 0 && (
                        <ul className="ml-3 list-disc">
                          {item.trace.candidates.map((c) => (
                            <li key={c.username}>
                              {c.username} — {c.available ? "доступен" : "недоступен"}
                              {c.kind ? ` (${c.kind})` : ""}
                            </li>
                          ))}
                        </ul>
                      )}
                    </>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </Card>
  );
}

export default function IncidentDetail() {
  const { id } = useParams();
  const { data, isLoading } = useQuery<IncidentDetailType>({
    queryKey: ["incident", id],
    queryFn: () => api.get<IncidentDetailType>(`/incidents/${id}`),
  });
  const now = useNow();

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  const root = data.members.find((m) => m.role === "root");
  const symptoms = data.members.filter((m) => m.role !== "root");
  const duration = data.closed_at
    ? formatDuration(data.opened_at, new Date(data.closed_at).getTime())
    : formatDuration(data.opened_at, now);

  return (
    <div>
      <Link to="/incidents" className="text-sm text-accent">← к списку инцидентов</Link>
      <PageHeader
        title={`INC-${String(data.id).padStart(4, "0")}`}
        subtitle={`Открыт ${new Date(data.opened_at).toLocaleString("ru-RU")}${
          data.closed_at
            ? ` · закрыт ${new Date(data.closed_at).toLocaleString("ru-RU")} · длительность ${duration}`
            : ` · в работе ${duration}`
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

      <RoutingTraceCard problemId={data.root_problem_id} />

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
