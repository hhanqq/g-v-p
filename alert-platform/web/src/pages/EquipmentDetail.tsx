import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import ReactFlow, { Background, Edge, Node } from "reactflow";
import "reactflow/dist/style.css";
import { api, EquipmentDetail as EquipmentDetailType } from "../api";
import { Card, PageHeader, PriorityBadge, StatusBadge } from "../components/ui";

// Раздел «Оборудование» кейса пользователя — карточка объекта с историей
// и графом связанных алертов, два режима анализа: "исторический"
// (вся статистика по объекту) и "текущий инцидент" (только цепочка
// незакрытой проблемы). ReactFlow здесь — режим ПРОСМОТРА (авто-раскладка
// по времени, перетаскивание/редактирование выключено): полноценный
// визуальный конструктор сценариев — отдельная, более крупная задача
// Этапа 2, здесь используется тот же движок для отрисовки графа.

export default function EquipmentDetail() {
  const { id } = useParams();
  const [mode, setMode] = useState<"history" | "current">("history");

  const { data, isLoading } = useQuery<EquipmentDetailType>({
    queryKey: ["equipment", id],
    queryFn: () => api.get<EquipmentDetailType>(`/equipment/${id}`),
  });

  const problems = useMemo(() => {
    if (!data) return [];
    if (mode === "history") return data.related_problems;
    return data.related_problems.filter((p) => p.status === "OPEN" || p.status === "FLAPPING");
  }, [data, mode]);

  const { nodes, edges } = useMemo<{ nodes: Node[]; edges: Edge[] }>(() => {
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
  }, [problems]);

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
        <h3 className="text-sm font-semibold">Граф связанных алертов ({problems.length})</h3>
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

      <div className="h-96 rounded-xl border border-border bg-card">
        {nodes.length > 0 ? (
          <ReactFlow nodes={nodes} edges={edges} fitView nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}>
            <Background color="#334155" gap={16} />
          </ReactFlow>
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted">Нет данных для этого режима</div>
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
