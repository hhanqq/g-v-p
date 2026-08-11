import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";
import { api, CoveragePolicy, EquipmentCoverage, EquipmentGroup, EquipmentListItem, GroupListItem, PolicyGapsResponse } from "../api";
import { Card, PageHeader } from "../components/ui";

function toISO(date: Date): string {
  return date.toISOString().slice(0, 19);
}

const AVAILABILITY_COLOR: Record<string, string> = {
  available: "bg-emerald-500",
  shift: "bg-sky-500",
  on_call: "bg-indigo-500",
  override_available: "bg-emerald-500",
  delegation: "bg-purple-500",
  vacation: "bg-amber-500",
  sick_leave: "bg-red-500",
  override_unavailable: "bg-red-500",
  unavailable: "bg-slate-500",
};

export default function Coverage() {
  const [tab, setTab] = useState<"equipment" | "policies">("equipment");
  return (
    <div>
      <PageHeader title="Покрытие" helpArticle="coverage" />
      <div className="mb-4 flex gap-1.5">
        {(["equipment", "policies"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium ${tab === t ? "bg-accent text-white" : "bg-card text-muted hover:bg-fg/5"}`}
          >
            {t === "equipment" ? "По оборудованию" : "Политики"}
          </button>
        ))}
      </div>
      {tab === "equipment" ? <EquipmentCoverageView /> : <PolicyManagement />}
    </div>
  );
}

// ── По оборудованию ──────────────────────────────────────────────────
// Отвечает на буквальный вопрос раздела 47 доп. ТЗ: «кто будет доступен
// для конкретного оборудования в конкретный момент времени». Источник
// доступности — internal/availability.Resolve через новый
// /api/equipment/{id}/coverage — тот же самый, которым пользуется
// маршрутизация уведомлений (раздел 56): что видно здесь, то и решает,
// кому реально уйдёт уведомление.

function EquipmentCoverageView() {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedLabel, setSelectedLabel] = useState<string>("");

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[320px_1fr]">
      <Card className="!p-2">
        <div className="mb-2 px-2 text-xs font-semibold uppercase tracking-wide text-muted">Оборудование</div>
        <EquipmentTreePicker selectedId={selectedId} onSelect={(id, label) => { setSelectedId(id); setSelectedLabel(label); }} />
      </Card>
      {selectedId ? (
        <CoveragePanel objectId={selectedId} objectLabel={selectedLabel} />
      ) : (
        <Card>
          <p className="text-sm text-muted">Выберите объект слева, чтобы увидеть календарь покрытия ответственных.</p>
        </Card>
      )}
    </div>
  );
}

function EquipmentTreePicker({ selectedId, onSelect }: { selectedId: string | null; onSelect: (id: string, label: string) => void }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const { data: sites, isLoading } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups-root"],
    queryFn: () => api.get("/equipment/groups"),
  });

  function toggle(key: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  if (isLoading) return <div className="px-2 py-1 text-xs text-muted">Загрузка…</div>;

  return (
    <div className="max-h-[70vh] overflow-y-auto text-sm">
      {sites?.map((site) => (
        <SiteNode key={site.key} site={site} expanded={expanded} onToggle={toggle} selectedId={selectedId} onSelect={onSelect} />
      ))}
    </div>
  );
}

function SiteNode({
  site, expanded, onToggle, selectedId, onSelect,
}: {
  site: EquipmentGroup; expanded: Set<string>; onToggle: (key: string) => void;
  selectedId: string | null; onSelect: (id: string, label: string) => void;
}) {
  const isOpen = expanded.has(site.key);
  const { data: categories } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups", site.key],
    queryFn: () => api.get(`/equipment/groups?site=${encodeURIComponent(site.key)}`),
    enabled: isOpen,
  });
  return (
    <div>
      <button onClick={() => onToggle(site.key)} className="flex w-full items-center gap-1 rounded px-2 py-1 text-left hover:bg-fg/5">
        <ChevronRight size={13} className={`shrink-0 transition-transform ${isOpen ? "rotate-90" : ""}`} />
        <span className="truncate">{site.label}</span>
      </button>
      {isOpen && (
        <div className="ml-3 border-l border-border pl-1">
          {categories?.map((cat) => (
            <CategoryNode
              key={cat.key} site={site.key} category={cat} expanded={expanded} onToggle={onToggle}
              selectedId={selectedId} onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function CategoryNode({
  site, category, expanded, onToggle, selectedId, onSelect,
}: {
  site: string; category: EquipmentGroup; expanded: Set<string>; onToggle: (key: string) => void;
  selectedId: string | null; onSelect: (id: string, label: string) => void;
}) {
  const nodeKey = `${site}|${category.key}`;
  const isOpen = expanded.has(nodeKey);
  const { data: objects } = useQuery<EquipmentListItem[]>({
    queryKey: ["equipment-list", site, category.key],
    queryFn: () => api.get(`/equipment?site=${encodeURIComponent(site)}&equipment_type=${encodeURIComponent(category.key)}`),
    enabled: isOpen,
  });
  return (
    <div>
      <button onClick={() => onToggle(nodeKey)} className="flex w-full items-center gap-1 rounded px-2 py-1 text-left text-xs hover:bg-fg/5">
        <ChevronRight size={12} className={`shrink-0 transition-transform ${isOpen ? "rotate-90" : ""}`} />
        <span className="truncate text-muted">{category.label}</span>
      </button>
      {isOpen && (
        <div className="ml-3 border-l border-border pl-1">
          {objects?.map((obj) => (
            <button
              key={obj.id}
              onClick={() => onSelect(obj.id, obj.name)}
              className={`block w-full truncate rounded px-2 py-1 text-left text-xs ${selectedId === obj.id ? "bg-accent/15 text-accent" : "hover:bg-fg/5"}`}
            >
              {obj.name}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

const RANGE_OPTIONS = [
  { value: "today", label: "Сегодня" },
  { value: "week", label: "Неделя" },
  { value: "30d", label: "30 дней" },
];

function rangeFor(value: string): { from: Date; to: Date } {
  const from = new Date();
  from.setUTCHours(0, 0, 0, 0);
  const to = new Date(from);
  if (value === "today") to.setUTCDate(to.getUTCDate() + 1);
  else if (value === "week") to.setUTCDate(to.getUTCDate() + 7);
  else to.setUTCDate(to.getUTCDate() + 30);
  return { from, to };
}

function CoveragePanel({ objectId, objectLabel }: { objectId: string; objectLabel: string }) {
  const [range, setRange] = useState("week");
  const { from, to } = useMemo(() => rangeFor(range), [range]);
  const queryClient = useQueryClient();
  const [assignTarget, setAssignTarget] = useState<{ from: string; to: string } | null>(null);
  const [assignMember, setAssignMember] = useState<number | null>(null);
  const [assignKind, setAssignKind] = useState("on_call");

  const { data, isLoading } = useQuery<EquipmentCoverage>({
    queryKey: ["equipment-coverage", objectId, range],
    queryFn: () => api.get(`/equipment/${encodeURIComponent(objectId)}/coverage?from=${toISO(from)}&to=${toISO(to)}`),
  });

  const assignMutation = useMutation({
    mutationFn: () => {
      if (!assignMember || !assignTarget) return Promise.reject();
      return api.post(`/employees/${assignMember}/availability/intervals`, {
        kind: assignKind, valid_from: assignTarget.from, valid_until: assignTarget.to,
        note: `Назначено вручную из «Покрытие» для ${objectLabel}`,
      });
    },
    onSuccess: () => {
      setAssignTarget(null);
      queryClient.invalidateQueries({ queryKey: ["equipment-coverage", objectId] });
    },
  });

  if (isLoading || !data) return <Card><div className="text-sm text-muted">Загрузка…</div></Card>;

  return (
    <div className="space-y-4">
      <Card>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div>
            <div className="text-sm font-semibold">{objectLabel}</div>
            <div className="text-xs text-muted">
              {data.responsible_groups.length > 0
                ? `Ответственные: ${data.responsible_groups.map((g) => g.name).join(", ")}`
                : "Ответственная группа не назначена (см. «Группы»)"}
            </div>
          </div>
          <div className="flex gap-1">
            {RANGE_OPTIONS.map((r) => (
              <button
                key={r.value}
                onClick={() => setRange(r.value)}
                className={`rounded-md px-2.5 py-1 text-xs font-medium ${range === r.value ? "bg-accent text-white" : "bg-bg text-muted"}`}
              >
                {r.label}
              </button>
            ))}
          </div>
        </div>

        {data.policy ? (
          <div className="mb-3 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            <div><div className="text-xs text-muted">Требуется</div><div className="font-medium">{data.policy.name}</div></div>
            <div><div className="text-xs text-muted">Минимум одновременно</div><div className="font-medium">{data.policy.min_available}</div></div>
            <div><div className="text-xs text-muted">Покрытие за период</div><div className="font-medium text-emerald-400">{data.coverage_pct != null ? `${data.coverage_pct}%` : "—"}</div></div>
            <div><div className="text-xs text-muted">Разрывов найдено</div><div className="font-medium">{data.gaps.length}</div></div>
          </div>
        ) : (
          <p className="mb-3 text-xs text-muted">
            Для этого объекта не задана политика покрытия (минимум одновременно доступных) — ниже показана
            фактическая доступность ответственных без порога/процента.
          </p>
        )}

        {data.members.length === 0 ? (
          <p className="text-sm text-muted">В ответственной группе нет сотрудников.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-xs">
              <tbody>
                {data.members.map((m) => (
                  <tr key={m.id}>
                    <td className="w-32 truncate py-0.5 pr-2 text-muted">{m.display_name}</td>
                    <td>
                      <div className="flex h-4 gap-px">
                        {(data.timeline.by_member[String(m.id)] ?? []).map((status, i) => (
                          <div
                            key={i}
                            title={`${data.timeline.buckets[i]}: ${status}`}
                            className={`flex-1 ${AVAILABILITY_COLOR[status] ?? "bg-slate-700"}`}
                          />
                        ))}
                      </div>
                    </td>
                  </tr>
                ))}
                <tr>
                  <td className="pr-2 pt-1 text-muted">Доступно, чел.</td>
                  <td>
                    <div className="flex h-4 gap-px pt-1">
                      {data.timeline.available_count.map((count, i) => (
                        <div
                          key={i}
                          title={`${data.timeline.buckets[i]}: доступно ${count}`}
                          className={`flex-1 ${data.policy && count < data.policy.min_available ? "bg-red-500/60" : "bg-fg/10"}`}
                        />
                      ))}
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card>
        <h3 className="mb-2 text-sm font-semibold">Ближайшие разрывы</h3>
        {data.gaps.length === 0 ? (
          <p className="text-sm text-emerald-400">Разрывов покрытия за выбранный период не найдено.</p>
        ) : (
          <ul className="space-y-1.5">
            {data.gaps.map((g, i) => (
              <li key={i} className="flex items-center justify-between rounded-md bg-amber-500/15 px-2.5 py-1.5 text-xs text-amber-400">
                <span className="flex items-center gap-1.5">
                  <TriangleAlert size={13} strokeWidth={1.75} />
                  {new Date(g.from).toLocaleString("ru-RU")} — {new Date(g.to).toLocaleString("ru-RU")} (доступно меньше {g.min_available})
                </span>
                <button
                  onClick={() => setAssignTarget({ from: g.from, to: g.to })}
                  className="rounded bg-bg px-2 py-1 text-[11px] font-medium text-fg hover:bg-fg/10"
                >
                  Назначить ответственного
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>

      {assignTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <Card className="w-full max-w-sm">
            <h3 className="mb-3 text-sm font-semibold">Назначить ответственного</h3>
            <p className="mb-3 text-xs text-muted">
              {new Date(assignTarget.from).toLocaleString("ru-RU")} — {new Date(assignTarget.to).toLocaleString("ru-RU")}
            </p>
            <label className="mb-2 block text-xs text-muted">
              Сотрудник
              <select value={assignMember ?? ""} onChange={(e) => setAssignMember(Number(e.target.value) || null)} className="mt-1 w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm">
                <option value="">выберите</option>
                {data.members.map((m) => <option key={m.id} value={m.id}>{m.display_name}</option>)}
              </select>
            </label>
            <label className="mb-3 block text-xs text-muted">
              Тип
              <select value={assignKind} onChange={(e) => setAssignKind(e.target.value)} className="mt-1 w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm">
                <option value="on_call">Дежурство</option>
                <option value="shift">Смена</option>
                <option value="override_available">Доступен (override)</option>
              </select>
            </label>
            <div className="flex justify-end gap-2">
              <button onClick={() => setAssignTarget(null)} className="rounded-md bg-bg px-3 py-1.5 text-xs text-muted">Отмена</button>
              <button
                disabled={!assignMember || assignMutation.isPending}
                onClick={() => assignMutation.mutate()}
                className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50"
              >
                Сохранить
              </button>
            </div>
          </Card>
        </div>
      )}
    </div>
  );
}

// ── Политики ──────────────────────────────────────────────────────────
// Административная настройка порога min_available по группе — тот же
// движок (internal/coverage.Sweep), что и вкладка «По оборудованию»
// выше; здесь — точка входа для конфигурации, там — точка входа для
// вопроса «кто доступен сейчас».

function PolicyManagement() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [groupID, setGroupID] = useState<number | null>(null);
  const [minAvailable, setMinAvailable] = useState(1);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const from = useMemo(() => new Date(), []);
  const to = useMemo(() => new Date(from.getTime() + 30 * 86400000), [from]);

  const { data: policies } = useQuery<CoveragePolicy[]>({
    queryKey: ["coverage-policies"],
    queryFn: () => api.get<CoveragePolicy[]>("/coverage/policies"),
  });
  const { data: groups } = useQuery<GroupListItem[]>({
    queryKey: ["groups"],
    queryFn: () => api.get<GroupListItem[]>("/groups"),
  });
  const { data: gapsByPolicy } = useQuery<PolicyGapsResponse[]>({
    queryKey: ["coverage-gaps", from.toISOString(), to.toISOString()],
    queryFn: () => api.get<PolicyGapsResponse[]>(`/coverage/gaps?from=${toISO(from)}&to=${toISO(to)}`),
    refetchInterval: 30000,
  });

  async function createPolicy() {
    if (!name.trim() || !groupID) {
      setError("Укажите название и группу");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await api.post("/coverage/policies", { name, group_id: groupID, min_available: minAvailable });
      await queryClient.invalidateQueries({ queryKey: ["coverage-policies"] });
      setName("");
      setGroupID(null);
      setMinAvailable(1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Не удалось создать политику");
    } finally {
      setSaving(false);
    }
  }

  async function deletePolicy(id: number) {
    await api.delete(`/coverage/policies/${id}`);
    await queryClient.invalidateQueries({ queryKey: ["coverage-policies"] });
  }

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <Card className="lg:col-span-2">
        <h3 className="mb-3 text-sm font-semibold">Политики и пробелы (ближайшие 30 дней)</h3>
        <div className="space-y-3">
          {policies?.length ? (
            policies.map((p) => {
              const gaps = gapsByPolicy?.find((g) => g.policy_id === p.id)?.gaps ?? [];
              return (
                <div key={p.id} className="rounded-md border border-border p-3 text-sm">
                  <div className="flex items-center justify-between">
                    <div>
                      <span className="font-medium text-fg">{p.name}</span>
                      <span className="ml-2 text-xs text-muted">
                        группа «{p.group_name}» · минимум {p.min_available}
                      </span>
                    </div>
                    <button onClick={() => deletePolicy(p.id)} className="text-xs text-red-400 hover:underline">
                      удалить
                    </button>
                  </div>
                  {gaps.length > 0 ? (
                    <ul className="mt-2 space-y-1 text-xs">
                      {gaps.map((g, i) => (
                        <li key={i} className="flex items-center gap-1.5 rounded-md bg-amber-500/15 px-2 py-1 text-amber-400">
                          <TriangleAlert size={13} strokeWidth={1.75} className="shrink-0" />
                          {new Date(g.from).toLocaleString("ru-RU")} — {new Date(g.to).toLocaleString("ru-RU")} (доступно: {g.min_available})
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <div className="mt-2 text-xs text-emerald-400">пробелов не найдено</div>
                  )}
                </div>
              );
            })
          ) : (
            <p className="text-sm text-muted">Политик покрытия пока нет — создайте первую.</p>
          )}
        </div>
      </Card>

      <Card>
        <h3 className="mb-3 text-sm font-semibold">Новая политика</h3>
        <div className="space-y-3">
          <div>
            <label className="mb-1 block text-xs text-muted">Название</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="например, дежурство по буровой"
              className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">Группа</label>
            <select
              value={groupID ?? ""}
              onChange={(e) => setGroupID(e.target.value ? Number(e.target.value) : null)}
              className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
            >
              <option value="">не выбрана</option>
              {groups?.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">Минимум одновременно доступных</label>
            <input
              type="number"
              min={1}
              value={minAvailable}
              onChange={(e) => setMinAvailable(Math.max(1, Number(e.target.value) || 1))}
              className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
            />
          </div>
          {error && <div className="rounded-md bg-red-500/15 px-3 py-2 text-xs text-red-400">{error}</div>}
          <button
            disabled={saving}
            onClick={createPolicy}
            className="w-full rounded-md bg-accent px-3 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
          >
            {saving ? "Сохранение…" : "Создать политику"}
          </button>
          <p className="text-xs text-muted">
            Пробелы пересчитываются по требованию (не хранятся) — та же логика, что использует dry-run на
            карточке доступности сотрудника при создании интервала.
          </p>
        </div>
      </Card>
    </div>
  );
}
