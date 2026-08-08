import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import ReactFlow, { Background, Edge, MarkerType, Node } from "reactflow";
import "reactflow/dist/style.css";
import { api, EquipmentDetail as EquipmentDetailType } from "../api";
import { Card, PageHeader, PriorityBadge, StatusBadge } from "../components/ui";

// Раздел «Оборудование» кейса пользователя — карточка объекта с историей
// и графом связанных алертов, два режима анализа. "Текущий инцидент" —
// живая цепочка незакрытой проблемы этого объекта (по сырым Problem).
// "Исторический" режим — НЕ список отдельных Problem (их могут быть
// сотни за долгий период), а агрегированная частота реальных корреляций
// (services/api/metrics.py::equipment_interactions, раздел 6.4): с какими
// другими объектами этот чаще всего оказывался в одном инциденте и в
// какой роли (вызвал / был вызван), с числом повторений связи. ReactFlow
// здесь — режим ПРОСМОТРА (перетаскивание/редактирование выключено).

export default function EquipmentDetail() {
  const { id } = useParams();
  const [mode, setMode] = useState<"history" | "current">("history");

  const { data, isLoading } = useQuery<EquipmentDetailType>({
    queryKey: ["equipment", id],
    queryFn: () => api.get<EquipmentDetailType>(`/equipment/${encodeURIComponent(id ?? "")}`),
  });

  const problems = useMemo(() => {
    if (!data) return [];
    if (mode === "history") return data.related_problems;
    return data.related_problems.filter((p) => p.status === "OPEN" || p.status === "FLAPPING");
  }, [data, mode]);

  const { nodes, edges } = useMemo<{ nodes: Node[]; edges: Edge[] }>(() => {
    if (mode === "history") {
      if (!data) return { nodes: [], edges: [] };
      const { caused, caused_by } = data.interactions;
      const rows = Math.max(caused.length, caused_by.length, 1);
      const ns: Node[] = [{
        id: "center",
        position: { x: 280, y: ((rows - 1) * 100) / 2 },
        data: { label: `${data.name}\n(этот объект)` },
        style: {
          background: "#2563eb", color: "#fff", border: "1px solid #1d4ed8", borderRadius: 8,
          fontSize: 12, fontWeight: 600, whiteSpace: "pre-line", padding: 10, textAlign: "center",
        },
      }];
      const es: Edge[] = [];
      caused_by.forEach((item, i) => {
        const id = `by-${item.object_id}-${item.symptom_class}`;
        ns.push({
          id, position: { x: 0, y: i * 100 },
          data: { label: `${item.name}\n${item.symptom_class}` },
          style: { background: "#1e293b", color: "#e2e8f0", border: "1px solid #f97316",
                   borderRadius: 8, fontSize: 12, whiteSpace: "pre-line", padding: 8 },
        });
        es.push({
          id: `e-${id}`, source: id, target: "center", label: `${item.count}×`,
          markerEnd: { type: MarkerType.ArrowClosed, color: "#f97316" },
          style: { stroke: "#f97316", strokeWidth: Math.min(1 + item.count, 6) },
          labelStyle: { fill: "#f97316", fontSize: 11, fontWeight: 600 },
        });
      });
      caused.forEach((item, i) => {
        const id = `to-${item.object_id}-${item.symptom_class}`;
        ns.push({
          id, position: { x: 560, y: i * 100 },
          data: { label: `${item.name}\n${item.symptom_class}` },
          style: { background: "#1e293b", color: "#e2e8f0", border: "1px solid #3b82f6",
                   borderRadius: 8, fontSize: 12, whiteSpace: "pre-line", padding: 8 },
        });
        es.push({
          id: `e-${id}`, source: "center", target: id, label: `${item.count}×`,
          markerEnd: { type: MarkerType.ArrowClosed, color: "#3b82f6" },
          style: { stroke: "#3b82f6", strokeWidth: Math.min(1 + item.count, 6) },
          labelStyle: { fill: "#3b82f6", fontSize: 11, fontWeight: 600 },
        });
      });
      return { nodes: ns, edges: es };
    }

    // mode === "current" — живая цепочка сырых Problem этого объекта
    const sorted = [...problems].sort((a, b) => a.opened_at.localeCompare(b.opened_at));
    const ns: Node[] = sorted.map((p, i) => ({
      id: String(p.id),
      position: { x: (i % 4) * 220, y: Math.floor(i / 4) * 110 },
      data: { label: `${p.symptom_class}\n${p.status}${p.priority ? " · " + p.priority : ""}` },
      style: {
        background: p.status === "OPEN" || p.status === "FLAPPING" ? "#7f1d1d" : "#1e293b",
        color: "#e2e8f0",
        border: "1px solid #334155",
        borderRadius: 8,
        fontSize: 12,
        whiteSpace: "pre-line",
        padding: 8,
      },
    }));
    const es: Edge[] = sorted
      .filter((p) => p.duplicate_of_problem_id)
      .map((p) => ({
        id: `dup-${p.id}`,
        source: String(p.duplicate_of_problem_id),
        target: String(p.id),
        label: "дубль",
        style: { stroke: "#64748b" },
      }));
    return { nodes: ns, edges: es };
  }, [mode, data, problems]);

  const hasHistoryData = !!data && (data.interactions.caused.length > 0 || data.interactions.caused_by.length > 0);
  const graphHasContent = mode === "history" ? hasHistoryData : nodes.length > 0;

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  return (
    <div>
      <Link to="/equipment" className="text-sm text-accent">← к списку оборудования</Link>
      <PageHeader title={data.name} subtitle={`${data.equipment_type ?? data.kind} · ${data.site}`} />

      <div className="mb-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card><div className="text-xs text-muted">IP</div><div className="text-sm">{data.ip ?? "—"}</div></Card>
        <Card><div className="text-xs text-muted">FQDN</div><div className="text-sm">{data.fqdn ?? "—"}</div></Card>
        <Card><div className="text-xs text-muted">Подсеть</div><div className="text-sm">{data.subnet ?? "—"}</div></Card>
        <Card><div className="text-xs text-muted">Введено в эксплуатацию</div><div className="text-sm">{data.install_date ?? "—"}</div></Card>
      </div>

      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold">
          Граф связанных алертов
          {mode === "history"
            ? ` (${data.interactions.caused.length + data.interactions.caused_by.length} связей)`
            : ` (${problems.length})`}
        </h3>
        <div className="flex gap-2">
          <button
            onClick={() => setMode("history")}
            className={`rounded-md px-3 py-1.5 text-sm ${mode === "history" ? "bg-accent text-white" : "bg-card text-muted"}`}
          >
            Исторический режим
          </button>
          <button
            onClick={() => setMode("current")}
            className={`rounded-md px-3 py-1.5 text-sm ${mode === "current" ? "bg-accent text-white" : "bg-card text-muted"}`}
          >
            Текущий инцидент
          </button>
        </div>
      </div>

      {mode === "history" && hasHistoryData && (
        <p className="mb-2 text-xs text-muted">
          <span className="text-orange-400">← вызвано другим объектом</span> · <span className="text-blue-400">вызвал другой объект →</span> ·
          толщина линии и число — сколько раз эта связь повторялась в истории (раздел 6.4, реальные корреляции, не отдельные события)
        </p>
      )}

      <div className="h-96 rounded-xl border border-border bg-card">
        {graphHasContent ? (
          <ReactFlow nodes={nodes} edges={edges} fitView nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}>
            <Background color="#334155" gap={16} />
          </ReactFlow>
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted">
            {mode === "history" ? "Корреляций с другими объектами пока не зафиксировано" : "Нет данных для этого режима"}
          </div>
        )}
      </div>

      <h3 className="mb-2 mt-6 text-sm font-semibold">История событий</h3>
      <div className="space-y-2">
        {problems.map((p) => (
          <Card key={p.id} className="flex items-center justify-between">
            <div className="text-sm">{p.symptom_class} · {new Date(p.opened_at).toLocaleString("ru-RU")}</div>
            <div className="flex gap-2">
              <PriorityBadge priority={p.priority} />
              <StatusBadge status={p.status} />
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}
