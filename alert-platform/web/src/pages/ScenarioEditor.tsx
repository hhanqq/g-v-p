import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import ReactFlow, {
  addEdge,
  Background,
  Connection,
  Edge,
  Handle,
  Node,
  NodeProps,
  Position,
  useEdgesState,
  useNodesState,
} from "reactflow";
import "reactflow/dist/style.css";
import { api, ApiError, EmployeeListItem, EquipmentListItem, GroupListItem, ScenarioDetail } from "../api";
import { Card, PageHeader } from "../components/ui";
import { useTheme } from "../theme";

// Раздел «Сценарии», Этап 3 — визуальный редактор графа (ReactFlow):
// свободное перетаскивание/соединение узлов, включая ветвление через
// узлы-развилки «Проверка реакции»/«Проверка подписки» (два исходящих
// ребра — Да/Нет). На ИСПОЛНЕНИЕ граф проверяется на отсутствие циклов
// (packages/scenarios/engine.py::parse_graph, DAG), узел «Условие»
// строго один на входе. Сервер (`/activate`) сам проверяет это и
// понятно отвечает, если граф не проходит валидацию — здесь мы просто
// даём инструмент рисовать, а не дублируем валидацию клиентской копией
// той же логики.

type PriorityMin = "" | "P0" | "P1" | "P2" | "P3";

interface ConditionData {
  kind: "condition";
  priority_min?: PriorityMin;
  subsidiary?: string;
  symptom_class?: string;
  object_id?: string;
  object_label?: string;
  equipment_type?: string;
}

interface NotifyData {
  kind: "notify";
  employee_id?: number;
  employee_label?: string;
  group_id?: number;
  group_label?: string;
}

interface WaitData {
  kind: "wait";
  minutes?: number;
}

interface AckCheckData {
  kind: "ack_check";
}

interface SubscriptionCheckData {
  kind: "subscription_check";
  employee_id?: number;
  employee_label?: string;
  group_id?: number;
  group_label?: string;
}

type StepData = ConditionData | NotifyData | WaitData | AckCheckData | SubscriptionCheckData;

function YesNoHandles() {
  return (
    <>
      <div className="mt-2 flex justify-between text-[10px] text-muted">
        <span>Нет ←</span>
        <span>→ Да</span>
      </div>
      <Handle type="source" position={Position.Bottom} id="no" style={{ left: "25%" }} />
      <Handle type="source" position={Position.Bottom} id="yes" style={{ left: "75%" }} />
    </>
  );
}

function ConditionNode({ data, selected }: NodeProps<ConditionData>) {
  return (
    <div className={`rounded-lg border bg-card px-3 py-2 text-xs ${selected ? "border-accent" : "border-border"}`}>
      <div className="mb-1 font-semibold text-fg">Условие</div>
      <div className="text-muted">
        {data.priority_min ? `приоритет ≤ ${data.priority_min}` : "любой приоритет"}
        {data.subsidiary ? ` · ${data.subsidiary}` : ""}
        {data.symptom_class ? ` · ${data.symptom_class}` : ""}
        {data.object_label ? ` · ${data.object_label}` : ""}
        {data.equipment_type ? ` · тип: ${data.equipment_type}` : ""}
      </div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

function NotifyNode({ data, selected }: NodeProps<NotifyData>) {
  return (
    <div className={`rounded-lg border bg-card px-3 py-2 text-xs ${selected ? "border-accent" : "border-border"}`}>
      <Handle type="target" position={Position.Top} />
      <div className="mb-1 font-semibold text-fg">Уведомить</div>
      <div className="text-muted">{data.group_label ? `группа: ${data.group_label}` : data.employee_label ?? "получатель не выбран"}</div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

function WaitNode({ data, selected }: NodeProps<WaitData>) {
  return (
    <div className={`rounded-lg border bg-card px-3 py-2 text-xs ${selected ? "border-accent" : "border-border"}`}>
      <Handle type="target" position={Position.Top} />
      <div className="mb-1 font-semibold text-fg">Подождать</div>
      <div className="text-muted">{data.minutes ? `${data.minutes} мин` : "минуты не заданы"}</div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

function AckCheckNode({ selected }: NodeProps<AckCheckData>) {
  return (
    <div className={`rounded-lg border bg-card px-3 py-2 text-xs ${selected ? "border-accent" : "border-border"}`}>
      <Handle type="target" position={Position.Top} />
      <div className="mb-1 font-semibold text-fg">Проверка реакции</div>
      <div className="text-muted">был ли ответ в TrueConf на уведомление</div>
      <YesNoHandles />
    </div>
  );
}

function SubscriptionCheckNode({ data, selected }: NodeProps<SubscriptionCheckData>) {
  return (
    <div className={`rounded-lg border bg-card px-3 py-2 text-xs ${selected ? "border-accent" : "border-border"}`}>
      <Handle type="target" position={Position.Top} />
      <div className="mb-1 font-semibold text-fg">Проверка подписки</div>
      <div className="text-muted">{data.group_label ? `группа: ${data.group_label}` : data.employee_label ?? "есть хоть кто-то доступный"}</div>
      <YesNoHandles />
    </div>
  );
}

const NODE_TYPES = {
  condition: ConditionNode,
  notify: NotifyNode,
  wait: WaitNode,
  ack_check: AckCheckNode,
  subscription_check: SubscriptionCheckNode,
};

let idCounter = 1;
function nextId(): string {
  idCounter += 1;
  return `n${Date.now()}-${idCounter}`;
}

function defaultDataFor(kind: StepData["kind"]): StepData {
  if (kind === "condition") return { kind: "condition" };
  if (kind === "notify") return { kind: "notify" };
  if (kind === "ack_check") return { kind: "ack_check" };
  if (kind === "subscription_check") return { kind: "subscription_check" };
  return { kind: "wait", minutes: 30 };
}

const EMPTY_GRAPH_NODES: Node<StepData>[] = [
  { id: "cond-1", type: "condition", position: { x: 40, y: 40 }, data: { kind: "condition" } },
];

export default function ScenarioEditor() {
  const { id } = useParams();
  const isNew = !id;
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { theme } = useTheme();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState<string>("draft");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loadedGraph, setLoadedGraph] = useState(false);

  const [nodes, setNodes, onNodesChange] = useNodesState<StepData>(isNew ? EMPTY_GRAPH_NODES : []);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const { data: scenario } = useQuery<ScenarioDetail>({
    queryKey: ["scenario", id],
    queryFn: () => api.get<ScenarioDetail>(`/scenarios/${id}`),
    enabled: !isNew,
  });

  const { data: employees } = useQuery<EmployeeListItem[]>({
    queryKey: ["employees"],
    queryFn: () => api.get<EmployeeListItem[]>("/employees"),
  });
  const { data: groups } = useQuery<GroupListItem[]>({
    queryKey: ["groups"],
    queryFn: () => api.get<GroupListItem[]>("/groups"),
  });
  const { data: equipment } = useQuery<EquipmentListItem[]>({
    queryKey: ["equipment"],
    queryFn: () => api.get<EquipmentListItem[]>("/equipment"),
  });

  useEffect(() => {
    if (isNew || !scenario || loadedGraph) return;
    setName(scenario.name);
    setDescription(scenario.description ?? "");
    setStatus(scenario.status);
    try {
      const graph = JSON.parse(scenario.graph_json) as { nodes: Node<StepData>[]; edges: Edge[] };
      setNodes(graph.nodes ?? []);
      setEdges(graph.edges ?? []);
    } catch {
      setNodes(EMPTY_GRAPH_NODES);
      setEdges([]);
    }
    setLoadedGraph(true);
  }, [isNew, scenario, loadedGraph, setNodes, setEdges]);

  const onConnect = useCallback(
    (params: Connection) => setEdges((eds) => addEdge(params, eds)),
    [setEdges],
  );

  const selectedNode = useMemo(() => nodes.find((n) => n.id === selectedId) ?? null, [nodes, selectedId]);

  function addNode(kind: StepData["kind"]) {
    const y = 40 + nodes.length * 110;
    const node: Node<StepData> = { id: nextId(), type: kind, position: { x: 40, y }, data: defaultDataFor(kind) };
    setNodes((ns) => [...ns, node]);
    setSelectedId(node.id);
  }

  function updateSelectedData(patch: Partial<StepData>) {
    if (!selectedId) return;
    setNodes((ns) =>
      ns.map((n) => (n.id === selectedId ? { ...n, data: { ...n.data, ...patch } as StepData } : n)),
    );
  }

  function deleteSelected() {
    if (!selectedId) return;
    setNodes((ns) => ns.filter((n) => n.id !== selectedId));
    setEdges((es) => es.filter((e) => e.source !== selectedId && e.target !== selectedId));
    setSelectedId(null);
  }

  async function save(): Promise<number | null> {
    setError(null);
    if (!name.trim()) {
      setError("Укажите название сценария");
      return null;
    }
    setSaving(true);
    try {
      const graph_json = JSON.stringify({ nodes, edges });
      if (isNew) {
        const created = await api.post<{ id: number }>("/scenarios", { name, description, graph_json });
        await queryClient.invalidateQueries({ queryKey: ["scenarios"] });
        navigate(`/scenarios/${created.id}/edit`, { replace: true });
        return created.id;
      }
      await api.put(`/scenarios/${id}`, { name, description, graph_json });
      await queryClient.invalidateQueries({ queryKey: ["scenario", id] });
      await queryClient.invalidateQueries({ queryKey: ["scenarios"] });
      setStatus("draft"); // раздел И5 — сервер сам снимает активный статус при правке графа
      return Number(id);
    } finally {
      setSaving(false);
    }
  }

  async function saveAndActivate() {
    const savedId = await save();
    if (!savedId) return;
    setError(null);
    try {
      await api.post(`/scenarios/${savedId}/activate`);
      setStatus("active");
      await queryClient.invalidateQueries({ queryKey: ["scenario", savedId] });
      await queryClient.invalidateQueries({ queryKey: ["scenarios"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Не удалось активировать сценарий");
    }
  }

  async function deactivate() {
    if (isNew) return;
    await api.post(`/scenarios/${id}/deactivate`);
    setStatus("draft");
    await queryClient.invalidateQueries({ queryKey: ["scenario", id] });
    await queryClient.invalidateQueries({ queryKey: ["scenarios"] });
  }

  return (
    <div>
      <Link to="/scenarios" className="text-sm text-accent">← к списку сценариев</Link>
      <PageHeader
        title={isNew ? "Новый сценарий" : "Редактирование сценария"}
        subtitle="Условие на входе, дальше — уведомления, ожидание и развилки (проверка реакции/подписки) с ветками Да/Нет; циклы не допускаются"
      />

      <div className="mb-4 grid grid-cols-1 gap-3 lg:grid-cols-3">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Название сценария"
          className="rounded-md border border-border bg-card px-3 py-2 text-sm lg:col-span-1"
        />
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Описание (необязательно)"
          className="rounded-md border border-border bg-card px-3 py-2 text-sm lg:col-span-2"
        />
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted">Добавить узел:</span>
        <button onClick={() => addNode("notify")} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-accent hover:text-white">
          + Уведомить
        </button>
        <button onClick={() => addNode("wait")} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-accent hover:text-white">
          + Подождать
        </button>
        <button onClick={() => addNode("ack_check")} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-accent hover:text-white">
          + Проверка реакции
        </button>
        <button onClick={() => addNode("subscription_check")} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-accent hover:text-white">
          + Проверка подписки
        </button>
        <span className="mx-2 h-4 w-px bg-border" />
        <span className={`rounded px-2 py-0.5 text-xs ${status === "active" ? "bg-emerald-500/15 text-emerald-400" : "bg-fg/10 text-muted"}`}>
          {status === "active" ? "активен" : "черновик"}
        </span>
        <span className="flex-1" />
        {status === "active" && !isNew && (
          <button onClick={deactivate} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-fg/10">
            Деактивировать
          </button>
        )}
        <button disabled={saving} onClick={() => save()} className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-fg/10 disabled:opacity-50">
          Сохранить
        </button>
        <button disabled={saving} onClick={saveAndActivate} className="rounded-md bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-50">
          Сохранить и активировать
        </button>
      </div>

      {error && <div className="mb-3 rounded-md bg-red-500/15 px-3 py-2 text-sm text-red-400">{error}</div>}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-4">
        <div className="h-[520px] rounded-xl border border-border bg-card lg:col-span-3">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={NODE_TYPES}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={(_, node) => setSelectedId(node.id)}
            onPaneClick={() => setSelectedId(null)}
            fitView
          >
            <Background color={theme === "dark" ? "#334155" : "#cbd5e1"} gap={16} />
          </ReactFlow>
        </div>

        <Card>
          <h3 className="mb-3 text-sm font-semibold">Свойства узла</h3>
          {!selectedNode && <p className="text-sm text-muted">Выберите узел на графе.</p>}

          {selectedNode?.data.kind === "condition" && (
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs text-muted">Приоритет не ниже</label>
                <select
                  value={selectedNode.data.priority_min ?? ""}
                  onChange={(e) => updateSelectedData({ priority_min: e.target.value as PriorityMin })}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">любой</option>
                  <option value="P0">P0</option>
                  <option value="P1">P1</option>
                  <option value="P2">P2</option>
                  <option value="P3">P3</option>
                </select>
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">Филиал (subsidiary)</label>
                <input
                  value={selectedNode.data.subsidiary ?? ""}
                  onChange={(e) => updateSelectedData({ subsidiary: e.target.value })}
                  placeholder="например, gpn-noyabrsk"
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">Класс симптома</label>
                <input
                  value={selectedNode.data.symptom_class ?? ""}
                  onChange={(e) => updateSelectedData({ symptom_class: e.target.value })}
                  placeholder="например, node_down"
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">Оборудование (конкретный объект)</label>
                <select
                  value={selectedNode.data.object_id ?? ""}
                  onChange={(e) => {
                    const eq = equipment?.find((x) => x.id === e.target.value);
                    updateSelectedData({ object_id: eq?.id, object_label: eq ? `${eq.name} (${eq.site})` : undefined });
                  }}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">любое</option>
                  {equipment?.map((eq) => (
                    <option key={eq.id} value={eq.id}>
                      {eq.name} ({eq.site})
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">Тип оборудования</label>
                <input
                  value={selectedNode.data.equipment_type ?? ""}
                  onChange={(e) => updateSelectedData({ equipment_type: e.target.value })}
                  placeholder="например, plc_controller"
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                />
              </div>
              <p className="text-xs text-muted">Незаполненное поле не сужает условие — та же логика, что и у подписок (раздел 8).</p>
            </div>
          )}

          {selectedNode?.data.kind === "notify" && (
            <div className="space-y-3">
              <div className="flex gap-3 text-xs">
                <label className="flex items-center gap-1.5">
                  <input
                    type="radio"
                    checked={!selectedNode.data.group_id}
                    onChange={() => updateSelectedData({ group_id: undefined, group_label: undefined })}
                  />
                  Сотрудник
                </label>
                <label className="flex items-center gap-1.5">
                  <input
                    type="radio"
                    checked={!!selectedNode.data.group_id}
                    onChange={() => updateSelectedData({ employee_id: undefined, employee_label: undefined })}
                  />
                  Группа
                </label>
              </div>
              {!selectedNode.data.group_id ? (
                <select
                  value={selectedNode.data.employee_id ?? ""}
                  onChange={(e) => {
                    const emp = employees?.find((x) => x.id === Number(e.target.value));
                    updateSelectedData({
                      employee_id: emp?.id,
                      employee_label: emp ? emp.full_name ?? emp.trueconf_username : undefined,
                    });
                  }}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">не выбран</option>
                  {employees?.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.full_name ?? e.trueconf_username}
                    </option>
                  ))}
                </select>
              ) : (
                <select
                  value={selectedNode.data.group_id ?? ""}
                  onChange={(e) => {
                    const grp = groups?.find((x) => x.id === Number(e.target.value));
                    updateSelectedData({ group_id: grp?.id, group_label: grp?.name });
                  }}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">не выбрана</option>
                  {groups?.map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </select>
              )}
              <p className="text-xs text-muted">При выборе группы уведомление уходит всем её активным участникам.</p>
            </div>
          )}

          {selectedNode?.data.kind === "wait" && (
            <div className="space-y-3">
              <label className="mb-1 block text-xs text-muted">Минут ожидания</label>
              <input
                type="number"
                min={1}
                value={selectedNode.data.minutes ?? ""}
                onChange={(e) => updateSelectedData({ minutes: Number(e.target.value) || undefined })}
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
              />
            </div>
          )}

          {selectedNode?.data.kind === "ack_check" && (
            <div className="space-y-2">
              <p className="text-sm text-muted">
                Развилка: «Да» — на уведомление по этому инциденту уже пришёл ответ в TrueConf (любой reply),
                «Нет» — реакции пока не было. Настраиваемых полей нет — соедините исходящие рёбра «Да»/«Нет»
                с нужными следующими узлами.
              </p>
            </div>
          )}

          {selectedNode?.data.kind === "subscription_check" && (
            <div className="space-y-3">
              <div className="flex gap-3 text-xs">
                <label className="flex items-center gap-1.5">
                  <input
                    type="radio"
                    checked={!selectedNode.data.group_id}
                    onChange={() => updateSelectedData({ group_id: undefined, group_label: undefined })}
                  />
                  Сотрудник
                </label>
                <label className="flex items-center gap-1.5">
                  <input
                    type="radio"
                    checked={!!selectedNode.data.group_id}
                    onChange={() => updateSelectedData({ employee_id: undefined, employee_label: undefined })}
                  />
                  Группа
                </label>
              </div>
              {!selectedNode.data.group_id ? (
                <select
                  value={selectedNode.data.employee_id ?? ""}
                  onChange={(e) => {
                    const emp = employees?.find((x) => x.id === Number(e.target.value));
                    updateSelectedData({
                      employee_id: emp?.id,
                      employee_label: emp ? emp.full_name ?? emp.trueconf_username : undefined,
                    });
                  }}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">любой доступный (есть хоть кто-то подписан)</option>
                  {employees?.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.full_name ?? e.trueconf_username}
                    </option>
                  ))}
                </select>
              ) : (
                <select
                  value={selectedNode.data.group_id ?? ""}
                  onChange={(e) => {
                    const grp = groups?.find((x) => x.id === Number(e.target.value));
                    updateSelectedData({ group_id: grp?.id, group_label: grp?.name });
                  }}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">не выбрана (проверка «есть хоть кто-то в группе»)</option>
                  {groups?.map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </select>
              )}
              <p className="text-xs text-muted">
                «Да» — у выбранного сотрудника/группы (или хоть кого-то, если не выбран) реально сработала бы
                доставка по текущим подпискам (раздел 8); «Нет» — иначе.
              </p>
            </div>
          )}

          {selectedNode && selectedNode.data.kind !== "condition" && (
            <button onClick={deleteSelected} className="mt-4 w-full rounded-md bg-red-500/15 px-3 py-1.5 text-sm text-red-400 hover:bg-red-500/25">
              Удалить узел
            </button>
          )}
        </Card>
      </div>
    </div>
  );
}
