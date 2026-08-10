import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import ReactFlow, { Background, Edge, MarkerType, Node } from "reactflow";
import "reactflow/dist/style.css";
import { api, ChangeHistoryItem, EquipmentDetail as EquipmentDetailType } from "../api";
import { Card, PageHeader, PriorityBadge, StatusBadge } from "../components/ui";
import { useTheme } from "../theme";

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
  const { theme } = useTheme();
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<"history" | "current">("history");
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({ name: "", site: "", ip: "", fqdn: "", subnet: "", equipment_type: "", install_date: "" });

  const { data, isLoading } = useQuery<EquipmentDetailType>({
    queryKey: ["equipment", id],
    queryFn: () => api.get<EquipmentDetailType>(`/equipment/${encodeURIComponent(id ?? "")}`),
  });

  const { data: history } = useQuery<ChangeHistoryItem[]>({
    queryKey: ["equipment-history", id],
    queryFn: () => api.get<ChangeHistoryItem[]>(`/equipment/${encodeURIComponent(id ?? "")}/history`),
  });

  function startEditing() {
    if (!data) return;
    setForm({
      name: data.name, site: data.site, ip: data.ip ?? "", fqdn: data.fqdn ?? "",
      subnet: data.subnet ?? "", equipment_type: data.equipment_type ?? "", install_date: data.install_date ?? "",
    });
    setEditing(true);
  }

  async function saveEquipment() {
    setSaving(true);
    try {
      await api.put(`/equipment/${encodeURIComponent(id ?? "")}`, form);
      await queryClient.invalidateQueries({ queryKey: ["equipment", id] });
      await queryClient.invalidateQueries({ queryKey: ["equipment"] });
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

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
      <div className="flex items-start justify-between">
        <PageHeader title={data.name} subtitle={`${data.equipment_type ?? data.kind} · ${data.site}`} />
        {!editing && (
          <button
            onClick={startEditing}
            className="mt-1 shrink-0 rounded-md border border-border bg-card px-3 py-1.5 text-sm hover:bg-accent hover:text-white"
          >
            Редактировать
          </button>
        )}
      </div>

      {editing ? (
        <Card className="mb-4">
          <h3 className="mb-3 text-sm font-semibold">Редактирование объекта</h3>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="Название" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.site} onChange={(e) => setForm({ ...form, site: e.target.value })}
              placeholder="Площадка" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.ip} onChange={(e) => setForm({ ...form, ip: e.target.value })}
              placeholder="IP" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.fqdn} onChange={(e) => setForm({ ...form, fqdn: e.target.value })}
              placeholder="FQDN" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.subnet} onChange={(e) => setForm({ ...form, subnet: e.target.value })}
              placeholder="Подсеть" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.equipment_type} onChange={(e) => setForm({ ...form, equipment_type: e.target.value })}
              placeholder="Тип оборудования" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input type="date" value={form.install_date} onChange={(e) => setForm({ ...form, install_date: e.target.value })}
              className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
          </div>
          <div className="mt-3 flex gap-2">
            <button onClick={saveEquipment} disabled={saving}
              className="rounded-md bg-accent px-4 py-2 text-sm text-white disabled:opacity-50">
              {saving ? "Сохранение…" : "Сохранить"}
            </button>
            <button onClick={() => setEditing(false)} disabled={saving}
              className="rounded-md border border-border px-4 py-2 text-sm hover:bg-bg">
              Отмена
            </button>
          </div>
        </Card>
      ) : (
        <div className="mb-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
          <Card><div className="text-xs text-muted">IP</div><div className="text-sm">{data.ip ?? "—"}</div></Card>
          <Card><div className="text-xs text-muted">FQDN</div><div className="text-sm">{data.fqdn ?? "—"}</div></Card>
          <Card><div className="text-xs text-muted">Подсеть</div><div className="text-sm">{data.subnet ?? "—"}</div></Card>
          <Card><div className="text-xs text-muted">Введено в эксплуатацию</div><div className="text-sm">{data.install_date ?? "—"}</div></Card>
        </div>
      )}

      <Card className="mb-4">
        <div className="text-xs text-muted">Ответственные группы</div>
        {data.responsible_groups.length === 0 ? (
          <div className="mt-1 text-sm text-muted">
            Не назначены — настройте в разделе{" "}
            <Link to="/groups" className="text-accent">
              «Группы»
            </Link>
            .
          </div>
        ) : (
          <div className="mt-1 flex flex-wrap gap-2">
            {data.responsible_groups.map((g) => (
              <Link
                key={g.id}
                to="/groups"
                className="rounded bg-accent/15 px-2 py-0.5 text-xs font-medium text-accent"
              >
                {g.name}
              </Link>
            ))}
          </div>
        )}
      </Card>

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
            <Background color={theme === "dark" ? "#334155" : "#cbd5e1"} gap={16} />
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

      <h3 className="mb-2 mt-6 text-sm font-semibold">История изменений</h3>
      <p className="mb-2 text-xs text-muted">Правки самой карточки объекта — кто, что и когда менял (не путать с историей событий выше).</p>
      {(!history || history.length === 0) ? (
        <p className="text-sm text-muted">Изменений записи пока не было.</p>
      ) : (
        <div className="space-y-2">
          {history.map((h) => (
            <Card key={h.id} className="text-sm">
              <div className="flex items-center justify-between">
                <span>{h.actor} <span className="text-muted">({h.actor_role})</span> · {h.action}</span>
                <span className="text-xs text-muted">{new Date(h.occurred_at).toLocaleString("ru-RU")}</span>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
