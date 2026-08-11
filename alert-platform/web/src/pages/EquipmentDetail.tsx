import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import ReactFlow, { Background, Edge, MarkerType, Node } from "reactflow";
import "reactflow/dist/style.css";
import {
  AlertGraphEdge,
  AlertGraphNode,
  api,
  ChangeHistoryItem,
  EquipmentDetail as EquipmentDetailType,
  EquipmentIncidentItem,
  EquipmentSummary,
  TimelineEntry,
} from "../api";
import { Card, formatDuration, PageHeader, PriorityBadge, StatTile, StatusBadge, useNow } from "../components/ui";
import { useTheme } from "../theme";

const TABS = ["overview", "problems", "incidents", "history", "graph", "links", "changes"] as const;
type Tab = (typeof TABS)[number];
const TAB_LABEL: Record<Tab, string> = {
  overview: "Обзор", problems: "Текущие проблемы", incidents: "Инциденты",
  history: "История", graph: "Граф алертов", links: "Связи", changes: "Изменения",
};

function IncidentRow({ incident, now }: { incident: EquipmentIncidentItem; now: number }) {
  const isOpen = !incident.closed_at;
  return (
    <Card className="flex items-center justify-between">
      <div>
        <div className="text-sm font-medium">
          INC-{incident.id.toString().padStart(4, "0")} · {incident.symptom_class} · {incident.object_name}
        </div>
        <div className="mt-1 text-xs text-muted">
          Открыт {new Date(incident.opened_at).toLocaleString("ru-RU")} · {incident.member_count} связанных событий
        </div>
      </div>
      <div className="flex items-center gap-2">
        <PriorityBadge priority={incident.priority} />
        {isOpen ? (
          <span className="rounded bg-red-500/15 px-2 py-0.5 text-xs font-medium text-red-400">
            В работе · {formatDuration(incident.opened_at, now)}
          </span>
        ) : (
          <span className="rounded bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
            Закрыт · {formatDuration(incident.opened_at, new Date(incident.closed_at!).getTime())}
          </span>
        )}
      </div>
    </Card>
  );
}

const TIMELINE_KIND_LABEL: Record<string, string> = {
  problem_opened: "Открыта проблема", problem_acknowledged: "Подтверждена", problem_resolved: "Устранена",
  incident_created: "Создан инцидент", incident_closed: "Инцидент закрыт",
  sla_breach: "Нарушение SLA",
};

function timelineLabel(entry: TimelineEntry): string {
  if (entry.kind.startsWith("notification_")) return entry.title;
  if (entry.kind.startsWith("change_")) return entry.title;
  return TIMELINE_KIND_LABEL[entry.kind] ?? entry.title;
}

function TimelineList({ entries }: { entries: TimelineEntry[] }) {
  const byDay = useMemo(() => {
    const groups: { day: string; items: TimelineEntry[] }[] = [];
    for (const entry of entries) {
      const day = new Date(entry.at).toLocaleDateString("ru-RU", { day: "numeric", month: "long" });
      const last = groups[groups.length - 1];
      if (last && last.day === day) last.items.push(entry);
      else groups.push({ day, items: [entry] });
    }
    return groups;
  }, [entries]);

  if (entries.length === 0) return <p className="text-sm text-muted">История пока пуста.</p>;

  return (
    <div className="space-y-5">
      {byDay.map((group) => (
        <div key={group.day}>
          <div className="mb-2 text-xs font-semibold uppercase text-muted">{group.day}</div>
          <div className="space-y-1.5">
            {group.items.map((entry, i) => (
              <div key={i} className="flex items-start gap-3 text-sm">
                <span className="mt-0.5 w-14 shrink-0 tabular-nums text-xs text-muted">
                  {new Date(entry.at).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })}
                </span>
                <span>
                  <span className="font-medium">{timelineLabel(entry)}</span>
                  {entry.detail && <span className="text-muted"> · {entry.detail}</span>}
                </span>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function AlertGraphView({ objectID }: { objectID: string }) {
  const { theme } = useTheme();
  const [scope, setScope] = useState<"historical" | "incident">("historical");
  const [window_, setWindow] = useState("30d");
  const [incidentID, setIncidentID] = useState<number | null>(null);

  const { data: incidents } = useQuery<EquipmentIncidentItem[]>({
    queryKey: ["equipment-incidents", objectID],
    queryFn: () => api.get<EquipmentIncidentItem[]>(`/equipment/${encodeURIComponent(objectID)}/incidents`),
  });

  const activeIncidentID = incidentID ?? incidents?.[0]?.id ?? null;
  const query = scope === "incident" && activeIncidentID
    ? `scope=incident&incident_id=${activeIncidentID}`
    : `scope=historical&window=${window_}`;

  const { data: graph } = useQuery<{ nodes: AlertGraphNode[]; edges: AlertGraphEdge[] }>({
    queryKey: ["equipment-graph", objectID, query],
    queryFn: () => api.get(`/equipment/${encodeURIComponent(objectID)}/graph?${query}`),
    enabled: scope === "historical" || !!activeIncidentID,
  });

  const { nodes, edges } = useMemo<{ nodes: Node[]; edges: Edge[] }>(() => {
    if (!graph) return { nodes: [], edges: [] };
    const byIncidentRow = new Map<number, number>();
    const ns: Node[] = graph.nodes.map((n) => {
      const row = byIncidentRow.get(n.incident_id) ?? 0;
      byIncidentRow.set(n.incident_id, row + 1);
      const col = n.role === "root" ? 0 : 1;
      return {
        id: n.id,
        position: { x: col * 280, y: row * 90 },
        data: { label: `${n.object_name}\n${n.symptom_class}${n.priority ? " · " + n.priority : ""}` },
        style: {
          background: n.role === "root" ? "#7f1d1d" : "#1e293b", color: "#e2e8f0",
          border: n.status === "OPEN" || n.status === "FLAPPING" ? "1px solid #ef4444" : "1px solid #334155",
          borderRadius: 8, fontSize: 12, whiteSpace: "pre-line", padding: 8,
        },
      };
    });
    const es: Edge[] = graph.edges.map((e, i) => ({
      id: `e${i}`, source: e.from, target: e.to, label: e.rule_id ?? undefined,
      markerEnd: { type: MarkerType.ArrowClosed, color: "#64748b" },
      style: { stroke: "#64748b" }, labelStyle: { fill: "#94a3b8", fontSize: 11 },
    }));
    return { nodes: ns, edges: es };
  }, [graph]);

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <button onClick={() => setScope("historical")}
          className={`rounded-md px-3 py-1.5 text-sm ${scope === "historical" ? "bg-accent text-white" : "bg-card text-muted"}`}>
          Исторический
        </button>
        <button onClick={() => setScope("incident")}
          className={`rounded-md px-3 py-1.5 text-sm ${scope === "incident" ? "bg-accent text-white" : "bg-card text-muted"}`}>
          Текущий инцидент
        </button>
        {scope === "historical" && (
          <select value={window_} onChange={(e) => setWindow(e.target.value)}
            className="rounded-md border border-border bg-bg px-2 py-1.5 text-sm">
            <option value="24h">24 часа</option>
            <option value="7d">7 дней</option>
            <option value="30d">30 дней</option>
            <option value="90d">90 дней</option>
          </select>
        )}
        {scope === "incident" && (
          <select value={activeIncidentID ?? ""} onChange={(e) => setIncidentID(Number(e.target.value))}
            className="rounded-md border border-border bg-bg px-2 py-1.5 text-sm">
            {incidents?.map((inc) => (
              <option key={inc.id} value={inc.id}>
                INC-{inc.id.toString().padStart(4, "0")} · {inc.symptom_class} {inc.closed_at ? "(закрыт)" : "(в работе)"}
              </option>
            ))}
          </select>
        )}
      </div>
      <div className="h-96 rounded-xl border border-border bg-card">
        {nodes.length > 0 ? (
          <ReactFlow nodes={nodes} edges={edges} fitView nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}>
            <Background color={theme === "dark" ? "#334155" : "#cbd5e1"} gap={16} />
          </ReactFlow>
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted">Нет данных для этого режима</div>
        )}
      </div>
    </div>
  );
}

export default function EquipmentDetail() {
  const { id } = useParams();
  const objectID = id ?? "";
  const now = useNow();
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<Tab>("overview");
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({ name: "", site: "", ip: "", fqdn: "", subnet: "", equipment_type: "", install_date: "" });

  const { data, isLoading } = useQuery<EquipmentDetailType>({
    queryKey: ["equipment", objectID],
    queryFn: () => api.get<EquipmentDetailType>(`/equipment/${encodeURIComponent(objectID)}`),
  });
  const { data: summary } = useQuery<EquipmentSummary>({
    queryKey: ["equipment-summary", objectID],
    queryFn: () => api.get<EquipmentSummary>(`/equipment/${encodeURIComponent(objectID)}/summary`),
    refetchInterval: 30000,
  });
  const { data: history } = useQuery<ChangeHistoryItem[]>({
    queryKey: ["equipment-history", objectID],
    queryFn: () => api.get<ChangeHistoryItem[]>(`/equipment/${encodeURIComponent(objectID)}/history`),
    enabled: tab === "changes",
  });
  const { data: incidents } = useQuery<EquipmentIncidentItem[]>({
    queryKey: ["equipment-incidents", objectID],
    queryFn: () => api.get<EquipmentIncidentItem[]>(`/equipment/${encodeURIComponent(objectID)}/incidents`),
    enabled: tab === "incidents",
  });
  const { data: timeline } = useQuery<TimelineEntry[]>({
    queryKey: ["equipment-timeline", objectID],
    queryFn: () => api.get<TimelineEntry[]>(`/equipment/${encodeURIComponent(objectID)}/timeline`),
    enabled: tab === "history",
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
      await api.put(`/equipment/${encodeURIComponent(objectID)}`, form);
      await queryClient.invalidateQueries({ queryKey: ["equipment", objectID] });
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  const openProblems = useMemo(
    () => data?.related_problems.filter((p) => p.status === "OPEN" || p.status === "FLAPPING") ?? [],
    [data]
  );

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  return (
    <div>
      <div className="mb-2 flex items-center gap-2 text-sm text-muted">
        <Link to="/equipment" className="hover:text-accent">Оборудование</Link>
        <span>/</span>
        <Link to={`/equipment?site=${encodeURIComponent(data.site)}`} className="hover:text-accent">{data.site}</Link>
        <span>/</span>
        <span className="text-fg">{data.name}</span>
      </div>

      <div className="flex items-start justify-between">
        <PageHeader title={data.name} subtitle={`${data.equipment_type ?? data.kind} · ${data.site}`} />
        {!editing && (
          <button onClick={startEditing}
            className="mt-1 shrink-0 rounded-md border border-border bg-card px-3 py-1.5 text-sm hover:bg-accent hover:text-white">
            Редактировать
          </button>
        )}
      </div>

      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-5">
        <StatTile label="Активных проблем" value={summary?.active_problems ?? "—"} />
        <StatTile label="Открытых инцидентов" value={summary?.open_incidents ?? "—"} />
        <StatTile label="Алертов за 24ч" value={summary?.alerts_24h ?? "—"} />
        <StatTile label="Алертов за 30д" value={summary?.alerts_30d ?? "—"} />
        <StatTile
          label="MTTR 30д"
          value={summary?.avg_mttr_minutes_30d != null ? `${Math.round(summary.avg_mttr_minutes_30d)} мин` : "—"}
        />
      </div>

      <div className="mb-4 flex gap-1 overflow-x-auto border-b border-border">
        {TABS.map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`shrink-0 border-b-2 px-3 py-2 text-sm ${tab === t ? "border-accent font-medium text-fg" : "border-transparent text-muted"}`}>
            {TAB_LABEL[t]}
          </button>
        ))}
      </div>

      {tab === "overview" && (
        <div>
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
            <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
              <Card><div className="text-xs text-muted">IP</div><div className="text-sm">{data.ip ?? "—"}</div></Card>
              <Card><div className="text-xs text-muted">FQDN</div><div className="text-sm">{data.fqdn ?? "—"}</div></Card>
              <Card><div className="text-xs text-muted">Подсеть</div><div className="text-sm">{data.subnet ?? "—"}</div></Card>
              <Card><div className="text-xs text-muted">Введено в эксплуатацию</div><div className="text-sm">{data.install_date ?? "—"}</div></Card>
            </div>
          )}
        </div>
      )}

      {tab === "problems" && (
        <div className="space-y-2">
          {openProblems.length === 0 && <p className="text-sm text-muted">Активных проблем нет.</p>}
          {openProblems.map((p) => (
            <Card key={p.id} className="flex items-center justify-between">
              <div>
                <div className="text-sm font-medium">{p.symptom_class}</div>
                <div className="mt-1 text-xs text-muted">
                  Открыта {new Date(p.opened_at).toLocaleString("ru-RU")} · длительность {formatDuration(p.opened_at, now)}
                  {p.incident_id && (
                    <> · <Link to={`/incidents/${p.incident_id}`} className="text-accent">INC-{p.incident_id.toString().padStart(4, "0")}</Link></>
                  )}
                </div>
              </div>
              <div className="flex gap-2">
                <PriorityBadge priority={p.priority} />
                <StatusBadge status={p.status} />
              </div>
            </Card>
          ))}
        </div>
      )}

      {tab === "incidents" && (
        <div className="space-y-2">
          {(!incidents || incidents.length === 0) && <p className="text-sm text-muted">Инцидентов, связанных с этим объектом, не найдено.</p>}
          {incidents?.map((inc) => <IncidentRow key={inc.id} incident={inc} now={now} />)}
        </div>
      )}

      {tab === "history" && <TimelineList entries={timeline ?? []} />}

      {tab === "graph" && <AlertGraphView objectID={objectID} />}

      {tab === "links" && (
        <div>
          <Card className="mb-4">
            <div className="text-xs text-muted">Ответственные группы</div>
            {data.responsible_groups.length === 0 ? (
              <div className="mt-1 text-sm text-muted">
                Не назначены — настройте в разделе <Link to="/groups" className="text-accent">«Группы»</Link>.
              </div>
            ) : (
              <div className="mt-1 flex flex-wrap gap-2">
                {data.responsible_groups.map((g) => (
                  <Link key={g.id} to="/groups" className="rounded bg-accent/15 px-2 py-0.5 text-xs font-medium text-accent">
                    {g.name}
                  </Link>
                ))}
              </div>
            )}
          </Card>
          <h3 className="mb-2 text-sm font-semibold">Частые корреляции с другими объектами</h3>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <div className="mb-1 text-xs text-muted">Вызывал проблемы у других объектов</div>
              {data.interactions.caused.length === 0 && <p className="text-sm text-muted">Не зафиксировано.</p>}
              {data.interactions.caused.map((c, i) => (
                <div key={i} className="text-sm">{c.name} · {c.symptom_class} · {c.count}×</div>
              ))}
            </div>
            <div>
              <div className="mb-1 text-xs text-muted">Проблемы у этого объекта вызывали другие</div>
              {data.interactions.caused_by.length === 0 && <p className="text-sm text-muted">Не зафиксировано.</p>}
              {data.interactions.caused_by.map((c, i) => (
                <div key={i} className="text-sm">{c.name} · {c.symptom_class} · {c.count}×</div>
              ))}
            </div>
          </div>
        </div>
      )}

      {tab === "changes" && (
        <div>
          <p className="mb-3 text-xs text-muted">Правки самой карточки объекта — кто, что и когда менял.</p>
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
      )}
    </div>
  );
}
