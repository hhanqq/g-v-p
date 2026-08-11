import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import ReactFlow, { Background, Handle, Node, NodeProps, Position } from "reactflow";
import "reactflow/dist/style.css";
import { api, ScenarioDetail, ScenarioRunItem, ScenarioRunTrace, ScenarioStatsResponse } from "../api";
import { Card, PageHeader } from "../components/ui";
import { useTheme } from "../theme";

// Раздел «Аналитика исполнения сценариев» — read-only просмотр поверх
// того же графа, что редактируется в ScenarioEditor.tsx, но без своих
// специализированных компонентов узлов (там они заточены под
// редактирование — dropdown'ы, панель свойств). Здесь узлу достаточно
// показать тип + краткое описание данных + счётчик срабатываний.

interface GraphNodeData {
  kind?: string;
  [key: string]: unknown;
}

function describeNodeData(data: GraphNodeData): string {
  if (data.group_label) return `группа: ${data.group_label}`;
  if (data.employee_labels && Array.isArray(data.employee_labels) && data.employee_labels.length) {
    return `список: ${(data.employee_labels as string[]).join(", ")}`;
  }
  if (data.employee_label) return String(data.employee_label);
  if (data.minutes) return `${data.minutes} мин`;
  if (data.priority_min || data.symptom_class || data.subsidiary) {
    return [data.priority_min && `≤${data.priority_min}`, data.subsidiary, data.symptom_class].filter(Boolean).join(" · ");
  }
  return "";
}

const NODE_LABEL: Record<string, string> = {
  condition: "Условие",
  notify: "Уведомить",
  wait: "Подождать",
  ack_check: "Проверка реакции",
  subscription_check: "Проверка подписки",
  availability_check: "Проверка доступности",
};

function StatsNode({ data }: NodeProps<{ kind: string; raw: GraphNodeData; count: number; highlighted: boolean }>) {
  return (
    <div className={`rounded-lg border bg-card px-3 py-2 text-xs ${data.highlighted ? "border-accent ring-1 ring-accent" : "border-border"}`}>
      <Handle type="target" position={Position.Top} />
      <div className="flex items-center justify-between gap-2">
        <span className="font-semibold text-fg">{NODE_LABEL[data.kind] ?? data.kind}</span>
        {data.count > 0 && (
          <span className="rounded-full bg-accent/15 px-1.5 py-0.5 text-[10px] font-bold text-accent tabular-nums">{data.count}</span>
        )}
      </div>
      <div className="text-muted">{describeNodeData(data.raw) || "—"}</div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

const NODE_TYPES = { statsNode: StatsNode };

export default function ScenarioStats() {
  const { id } = useParams();
  const scenarioID = Number(id);
  const { theme } = useTheme();
  const [selectedRunID, setSelectedRunID] = useState<number | null>(null);
  const [liveOnly, setLiveOnly] = useState(true);

  const { data: scenario } = useQuery<ScenarioDetail>({
    queryKey: ["scenario", id],
    queryFn: () => api.get<ScenarioDetail>(`/scenarios/${id}`),
  });
  const { data: stats } = useQuery<ScenarioStatsResponse>({
    queryKey: ["scenario-stats", id],
    queryFn: () => api.get<ScenarioStatsResponse>(`/scenarios/${id}/stats`),
    refetchInterval: 15000,
  });
  const { data: runs } = useQuery<ScenarioRunItem[]>({
    queryKey: ["scenario-runs", id, liveOnly],
    queryFn: () => api.get<ScenarioRunItem[]>(`/scenarios/${id}/runs${liveOnly ? "?status=running" : ""}`),
    refetchInterval: 15000,
  });
  const { data: trace } = useQuery<ScenarioRunTrace>({
    queryKey: ["scenario-run-trace", id, selectedRunID],
    queryFn: () => api.get<ScenarioRunTrace>(`/scenarios/${id}/runs/${selectedRunID}/trace`),
    enabled: selectedRunID !== null,
  });

  const graphJSON = trace?.graph_json ?? scenario?.graph_json;
  const highlightedNodeIDs = useMemo(() => new Set((trace?.steps ?? []).map((s) => s.node_id)), [trace]);

  const countByNode = useMemo(() => {
    const totals = new Map<string, number>();
    for (const c of stats?.counters ?? []) {
      totals.set(c.node_id, (totals.get(c.node_id) ?? 0) + c.count);
    }
    return totals;
  }, [stats]);

  const { nodes, edges } = useMemo(() => {
    if (!graphJSON) return { nodes: [] as Node[], edges: [] };
    try {
      const parsed = JSON.parse(graphJSON) as { nodes: Node<GraphNodeData>[]; edges: { source: string; target: string; sourceHandle?: string | null }[] };
      const nodes: Node[] = (parsed.nodes ?? []).map((n) => ({
        id: n.id,
        type: "statsNode",
        position: n.position,
        data: { kind: n.data?.kind ?? "condition", raw: n.data ?? {}, count: countByNode.get(n.id) ?? 0, highlighted: highlightedNodeIDs.has(n.id) },
      }));
      const edges = (parsed.edges ?? []).map((e, idx) => ({
        id: `e${idx}`,
        source: e.source,
        target: e.target,
        sourceHandle: e.sourceHandle ?? undefined,
        animated: highlightedNodeIDs.has(e.source) && highlightedNodeIDs.has(e.target),
      }));
      return { nodes, edges };
    } catch {
      return { nodes: [] as Node[], edges: [] };
    }
  }, [graphJSON, countByNode, highlightedNodeIDs]);

  return (
    <div>
      <Link to="/scenarios" className="text-sm text-accent">← к списку сценариев</Link>
      <div className="mb-4 flex items-start justify-between">
        <PageHeader title={`Аналитика: ${scenario?.name ?? "…"}`} />
        {scenario && (
          <Link to={`/scenarios/${scenario.id}/edit`} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-fg/10">
            К редактору
          </Link>
        )}
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-4">
        <div className="h-[520px] rounded-xl border border-border bg-card lg:col-span-3">
          <ReactFlow nodes={nodes} edges={edges} nodeTypes={NODE_TYPES} nodesDraggable={false} nodesConnectable={false} fitView>
            <Background color={theme === "dark" ? "#334155" : "#cbd5e1"} gap={16} />
          </ReactFlow>
        </div>

        <Card>
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold">Живой режим</h3>
            <label className="flex items-center gap-1.5 text-xs text-muted">
              <input type="checkbox" checked={liveOnly} onChange={(e) => setLiveOnly(e.target.checked)} />
              только активные
            </label>
          </div>
          <div className="max-h-[440px] space-y-1.5 overflow-y-auto">
            {runs?.length ? (
              runs.map((r) => (
                <button
                  key={r.run_id}
                  onClick={() => setSelectedRunID(r.run_id === selectedRunID ? null : r.run_id)}
                  className={`block w-full rounded-md px-2 py-1.5 text-left text-xs ${
                    selectedRunID === r.run_id ? "bg-accent text-white" : "bg-bg hover:bg-fg/10"
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <span>проблема #{r.problem_id}</span>
                    <span className={selectedRunID === r.run_id ? "" : "text-muted"}>{r.status}</span>
                  </div>
                  <div className={selectedRunID === r.run_id ? "text-white/80" : "text-muted"}>
                    {r.symptom_class} · узел: {r.current_node_id || "—"}
                  </div>
                </button>
              ))
            ) : (
              <p className="text-xs text-muted">{liveOnly ? "Активных прогонов нет." : "Прогонов пока не было."}</p>
            )}
          </div>

          {selectedRunID !== null && trace && (
            <div className="mt-4 border-t border-border pt-3">
              <h4 className="mb-2 text-xs font-semibold text-fg">
                Трасса прогона #{selectedRunID} (версия {trace.version})
              </h4>
              <ol className="space-y-1.5 text-xs">
                {trace.steps.map((step, idx) => (
                  <li key={idx} className="rounded-md bg-bg px-2 py-1.5">
                    <div className="flex items-center justify-between">
                      <span className="font-medium text-fg">
                        {idx + 1}. {NODE_LABEL[step.node_type] ?? step.node_type}
                      </span>
                      <span className="text-muted">{new Date(step.entered_at).toLocaleTimeString("ru-RU")}</span>
                    </div>
                    {step.branch && <div className="text-muted">ветка: {step.branch}</div>}
                    {step.recipients_json && (
                      <div className="text-muted">
                        {(() => {
                          try {
                            const parsed = JSON.parse(step.recipients_json) as { selected?: string[]; reason?: string };
                            return `${parsed.selected?.join(", ") || "никто"} (${parsed.reason ?? "—"})`;
                          } catch {
                            return step.recipients_json;
                          }
                        })()}
                      </div>
                    )}
                  </li>
                ))}
                {trace.steps.length === 0 && <li className="text-muted">Прогон ещё ни разу не продвигался.</li>}
              </ol>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
