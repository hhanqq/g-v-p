import { useQuery } from "@tanstack/react-query";
import { ReactNode, useEffect, useMemo, useState } from "react";
import { api, AlertFilterOptions, AlertItem, EquipmentGroup, EquipmentSearchResult, FilterNode } from "../api";
import { EmptyState, PageHeader, PriorityBadge } from "../components/ui";
import QueryBuilder, { FieldDef } from "../components/QueryBuilder";

const PERIODS: { value: string; label: string }[] = [
  { value: "", label: "Всё время" },
  { value: "today", label: "Сегодня" },
  { value: "24h", label: "24 часа" },
  { value: "7d", label: "7 дней" },
  { value: "14d", label: "14 дней" },
  { value: "30d", label: "30 дней" },
  { value: "custom", label: "Произвольный" },
];

interface SimpleState {
  period: string;
  from: string;
  to: string;
  priorities: string[];
  statuses: string[];
  sources: string[];
  equipmentMode: "none" | "branch" | "object";
  site: string;
  equipmentType: string;
  objectId: string | null;
  objectLabel: string;
  incidentMode: "any" | "has" | "none" | "specific";
  incidentNumber: string;
  reactions: string[];
}

const EMPTY_STATE: SimpleState = {
  period: "",
  from: "",
  to: "",
  priorities: [],
  statuses: [],
  sources: [],
  equipmentMode: "none",
  site: "",
  equipmentType: "",
  objectId: null,
  objectLabel: "",
  incidentMode: "any",
  incidentNumber: "",
  reactions: [],
};

function buildFilterFromSimple(s: SimpleState): FilterNode {
  const conditions: FilterNode[] = [];
  if (s.priorities.length) conditions.push({ field: "priority", op: "in", value: s.priorities });
  if (s.statuses.length) conditions.push({ field: "status", op: "in", value: s.statuses });
  if (s.sources.length) conditions.push({ field: "source", op: "in", value: s.sources });
  if (s.equipmentMode === "object" && s.objectId) conditions.push({ field: "object_id", op: "eq", value: s.objectId });
  if (s.equipmentMode === "branch") {
    if (s.site) conditions.push({ field: "site", op: "eq", value: s.site });
    if (s.equipmentType) conditions.push({ field: "equipment_type", op: "eq", value: s.equipmentType });
  }
  if (s.incidentMode === "has") conditions.push({ field: "has_incident", op: "eq", value: true });
  if (s.incidentMode === "none") conditions.push({ field: "has_incident", op: "eq", value: false });
  if (s.incidentMode === "specific" && s.incidentNumber) {
    conditions.push({ field: "incident_id", op: "eq", value: Number(s.incidentNumber) });
  }
  if (s.reactions.length) conditions.push({ field: "reaction", op: "in", value: s.reactions });
  return { match: "all", conditions };
}

function isEmptyFilter(node: FilterNode): boolean {
  return !node.field && (!node.conditions || node.conditions.length === 0);
}

export default function Alerts() {
  const [offset, setOffset] = useState(0);
  const [simple, setSimple] = useState<SimpleState>(EMPTY_STATE);
  const [advancedFilter, setAdvancedFilter] = useState<FilterNode | null>(null);
  const [builderOpen, setBuilderOpen] = useState(false);
  const [draftFilter, setDraftFilter] = useState<FilterNode>({ match: "all", conditions: [] });
  const limit = 50;

  const { data: filterOptions } = useQuery<AlertFilterOptions>({
    queryKey: ["alerts-filter-options"],
    queryFn: () => api.get("/alerts/filter-options"),
    staleTime: 60_000,
  });
  const { data: sites } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups-root"],
    queryFn: () => api.get("/equipment/groups"),
  });
  const { data: categories } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups", simple.site],
    queryFn: () => api.get(`/equipment/groups?site=${encodeURIComponent(simple.site)}`),
    enabled: simple.equipmentMode === "branch" && !!simple.site,
  });

  const [objectQuery, setObjectQuery] = useState("");
  const [objectResults, setObjectResults] = useState<EquipmentSearchResult[] | null>(null);
  useEffect(() => {
    if (simple.equipmentMode !== "object" || objectQuery.trim().length < 2) {
      setObjectResults(null);
      return;
    }
    const handle = setTimeout(async () => {
      const results = await api.get<EquipmentSearchResult[]>(`/equipment/search?q=${encodeURIComponent(objectQuery.trim())}`);
      setObjectResults(results);
    }, 300);
    return () => clearTimeout(handle);
  }, [objectQuery, simple.equipmentMode]);

  const fieldDefs: FieldDef[] = useMemo(() => {
    if (!filterOptions) return [];
    return [
      { field: "priority", label: "Приоритет", kind: "enum", op: "in", options: filterOptions.priorities.map((p) => ({ value: p, label: p })) },
      { field: "status", label: "Статус", kind: "enum", op: "in", options: filterOptions.statuses },
      { field: "source", label: "Источник", kind: "enum", op: "in", options: filterOptions.sources.map((s) => ({ value: s, label: s })) },
      { field: "reaction", label: "Реакция", kind: "enum", op: "in", options: filterOptions.reactions },
      { field: "site", label: "Филиал (код объекта)", kind: "string", op: "eq" },
      { field: "equipment_type", label: "Категория оборудования", kind: "string", op: "eq" },
      { field: "object_id", label: "Оборудование (ID)", kind: "string", op: "eq" },
      { field: "has_incident", label: "Входит в инцидент", kind: "bool", op: "eq" },
      { field: "incident_id", label: "Номер инцидента", kind: "int", op: "eq" },
      { field: "sla_breached", label: "SLA нарушен", kind: "bool", op: "eq" },
    ];
  }, [filterOptions]);

  const activeFilter = advancedFilter ?? buildFilterFromSimple(simple);

  const queryString = useMemo(() => {
    const params = new URLSearchParams();
    if (!isEmptyFilter(activeFilter)) params.set("filter", JSON.stringify(activeFilter));
    if (simple.period) {
      params.set("period", simple.period);
      if (simple.period === "custom" && simple.from && simple.to) {
        params.set("from", simple.from);
        params.set("to", simple.to);
      }
    }
    params.set("limit", String(limit));
    params.set("offset", String(offset));
    return params.toString();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeFilter, simple.period, simple.from, simple.to, offset]);

  const { data, isLoading } = useQuery<{ total: number; items: AlertItem[] }>({
    queryKey: ["alerts", queryString],
    queryFn: () => api.get(`/alerts?${queryString}`),
  });

  function resetAll() {
    setSimple(EMPTY_STATE);
    setAdvancedFilter(null);
    setOffset(0);
  }
  function applyPreset(name: "critical" | "no_reaction" | "open" | "sla" | "today") {
    setAdvancedFilter(null);
    setOffset(0);
    if (name === "critical") setSimple({ ...EMPTY_STATE, priorities: ["P0", "P1"] });
    else if (name === "no_reaction") setSimple({ ...EMPTY_STATE, reactions: ["no_reaction"] });
    else if (name === "open") setSimple({ ...EMPTY_STATE, statuses: ["open", "flapping", "acknowledged"] });
    else if (name === "today") setSimple({ ...EMPTY_STATE, period: "today" });
    else if (name === "sla") {
      setSimple(EMPTY_STATE);
      setAdvancedFilter({ field: "sla_breached", op: "eq", value: true });
    }
  }

  function toggleIn<T>(list: T[], value: T): T[] {
    return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
  }

  return (
    <div>
      <PageHeader title="Алерты" />

      <div className="mb-3 flex flex-wrap gap-2">
        <PresetButton onClick={resetAll}>Все</PresetButton>
        <PresetButton onClick={() => applyPreset("critical")}>Критические</PresetButton>
        <PresetButton onClick={() => applyPreset("no_reaction")}>Без реакции</PresetButton>
        <PresetButton onClick={() => applyPreset("open")}>Открытые</PresetButton>
        <PresetButton onClick={() => applyPreset("sla")}>Нарушен SLA</PresetButton>
        <PresetButton onClick={() => applyPreset("today")}>Сегодня</PresetButton>
      </div>

      <div className="mb-4 rounded-xl border border-border bg-card p-3">
        {advancedFilter ? (
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted">Активен расширенный фильтр (Query Builder)</span>
            <button onClick={resetAll} className="rounded-md bg-bg px-2 py-1 text-xs text-muted hover:text-fg">
              Сбросить и вернуться к обычным фильтрам
            </button>
          </div>
        ) : (
          <div className="flex flex-wrap items-end gap-3 text-xs">
            <FilterField label="Период">
              <select
                value={simple.period}
                onChange={(e) => { setSimple({ ...simple, period: e.target.value }); setOffset(0); }}
                className="rounded-md border border-border bg-bg px-2 py-1"
              >
                {PERIODS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
              </select>
              {simple.period === "custom" && (
                <div className="mt-1 flex gap-1">
                  <input type="date" value={simple.from} onChange={(e) => setSimple({ ...simple, from: e.target.value })} className="rounded-md border border-border bg-bg px-1 py-1" />
                  <input type="date" value={simple.to} onChange={(e) => setSimple({ ...simple, to: e.target.value })} className="rounded-md border border-border bg-bg px-1 py-1" />
                </div>
              )}
            </FilterField>

            <FilterField label="Приоритет">
              <PillMultiSelect
                options={(filterOptions?.priorities ?? []).map((p) => ({ value: p, label: p }))}
                selected={simple.priorities}
                onToggle={(v) => { setSimple({ ...simple, priorities: toggleIn(simple.priorities, v) }); setOffset(0); }}
              />
            </FilterField>

            <FilterField label="Статус">
              <PillMultiSelect
                options={filterOptions?.statuses ?? []}
                selected={simple.statuses}
                onToggle={(v) => { setSimple({ ...simple, statuses: toggleIn(simple.statuses, v) }); setOffset(0); }}
              />
            </FilterField>

            <FilterField label="Источник">
              <PillMultiSelect
                options={(filterOptions?.sources ?? []).map((s) => ({ value: s, label: s }))}
                selected={simple.sources}
                onToggle={(v) => { setSimple({ ...simple, sources: toggleIn(simple.sources, v) }); setOffset(0); }}
              />
            </FilterField>

            <FilterField label="Оборудование">
              <div className="flex flex-col gap-1">
                <select
                  value={simple.equipmentMode}
                  onChange={(e) => setSimple({ ...simple, equipmentMode: e.target.value as SimpleState["equipmentMode"], site: "", equipmentType: "", objectId: null, objectLabel: "" })}
                  className="rounded-md border border-border bg-bg px-2 py-1"
                >
                  <option value="none">Любое</option>
                  <option value="branch">Ветка (филиал/категория)</option>
                  <option value="object">Конкретный объект</option>
                </select>
                {simple.equipmentMode === "branch" && (
                  <div className="flex gap-1">
                    <select value={simple.site} onChange={(e) => setSimple({ ...simple, site: e.target.value, equipmentType: "" })} className="rounded-md border border-border bg-bg px-2 py-1">
                      <option value="">Филиал</option>
                      {(sites ?? []).map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
                    </select>
                    <select value={simple.equipmentType} onChange={(e) => setSimple({ ...simple, equipmentType: e.target.value })} disabled={!simple.site} className="rounded-md border border-border bg-bg px-2 py-1 disabled:opacity-40">
                      <option value="">Вся категория</option>
                      {(categories ?? []).map((c) => <option key={c.key} value={c.key}>{c.label}</option>)}
                    </select>
                  </div>
                )}
                {simple.equipmentMode === "object" && (
                  <div className="relative">
                    <input
                      value={simple.objectLabel || objectQuery}
                      onChange={(e) => { setObjectQuery(e.target.value); setSimple({ ...simple, objectId: null, objectLabel: "" }); }}
                      placeholder="Поиск по имени/IP…"
                      className="w-48 rounded-md border border-border bg-bg px-2 py-1"
                    />
                    {objectResults && objectResults.length > 0 && !simple.objectId && (
                      <div className="absolute z-10 mt-1 max-h-48 w-64 overflow-y-auto rounded-md border border-border bg-card shadow-lg">
                        {objectResults.map((r) => (
                          <button
                            key={r.id}
                            onClick={() => { setSimple({ ...simple, objectId: r.id, objectLabel: r.name }); setObjectResults(null); }}
                            className="block w-full px-2 py-1 text-left hover:bg-fg/5"
                          >
                            {r.name} <span className="text-muted">· {r.site_label}</span>
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </FilterField>

            <FilterField label="Инцидент">
              <div className="flex flex-col gap-1">
                <select value={simple.incidentMode} onChange={(e) => setSimple({ ...simple, incidentMode: e.target.value as SimpleState["incidentMode"] })} className="rounded-md border border-border bg-bg px-2 py-1">
                  <option value="any">Не важно</option>
                  <option value="has">Входит в инцидент</option>
                  <option value="none">Не входит</option>
                  <option value="specific">Конкретный INC-…</option>
                </select>
                {simple.incidentMode === "specific" && (
                  <input
                    value={simple.incidentNumber}
                    onChange={(e) => setSimple({ ...simple, incidentNumber: e.target.value.replace(/\D/g, "") })}
                    placeholder="0051"
                    className="w-20 rounded-md border border-border bg-bg px-2 py-1"
                  />
                )}
              </div>
            </FilterField>

            <FilterField label="Реакция">
              <PillMultiSelect
                options={filterOptions?.reactions ?? []}
                selected={simple.reactions}
                onToggle={(v) => { setSimple({ ...simple, reactions: toggleIn(simple.reactions, v) }); setOffset(0); }}
              />
            </FilterField>

            <button onClick={resetAll} className="rounded-md bg-bg px-2 py-1.5 text-muted hover:text-fg">Сбросить</button>
            <button
              onClick={() => { setDraftFilter(activeFilter); setBuilderOpen(true); }}
              className="rounded-md border border-accent px-2 py-1.5 text-accent hover:bg-accent/10"
            >
              Расширенный фильтр
            </button>
          </div>
        )}
      </div>

      {builderOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-border bg-card p-5">
            <h2 className="mb-3 text-sm font-semibold">Расширенный фильтр</h2>
            {fieldDefs.length > 0 && <QueryBuilder value={draftFilter} onChange={setDraftFilter} fieldDefs={fieldDefs} />}
            <div className="mt-4 flex justify-end gap-2">
              <button onClick={() => setBuilderOpen(false)} className="rounded-md bg-bg px-3 py-1.5 text-sm text-muted">Отмена</button>
              <button
                onClick={() => { setAdvancedFilter(draftFilter); setOffset(0); setBuilderOpen(false); }}
                className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white"
              >
                Применить
              </button>
            </div>
          </div>
        </div>
      )}

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && data?.items.length === 0 && <EmptyState>По этому фильтру ничего не найдено</EmptyState>}

      {!!data?.items.length && (
        <>
          {/* Десктоп — таблица со всеми колонками; на узких экранах те же
              данные читать построчно неудобно (раздел 34 доп. ТЗ), поэтому
              ниже — отдельный карточный список, а не сжатая таблица. */}
          <div className="hidden overflow-x-auto rounded-xl border border-border md:block">
            <table className="w-full min-w-[900px] text-sm">
              <thead className="bg-card text-left text-xs text-muted">
                <tr>
                  <th className="px-4 py-2">Время</th>
                  <th className="px-4 py-2">Приоритет</th>
                  <th className="px-4 py-2">Объект</th>
                  <th className="px-4 py-2">Симптом</th>
                  <th className="px-4 py-2">Источник</th>
                  <th className="px-4 py-2">Статус</th>
                  <th className="px-4 py-2">Инцидент</th>
                  <th className="px-4 py-2">Реакция</th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((a) => (
                  <tr key={a.id} className="border-t border-border hover:bg-fg/5">
                    <td className="px-4 py-2 text-muted">{new Date(a.occurred_at).toLocaleString("ru-RU")}</td>
                    <td className="px-4 py-2"><PriorityBadge priority={a.priority} /></td>
                    <td className="px-4 py-2">{a.object_id ?? "—"}</td>
                    <td className="px-4 py-2">
                      {a.symptom_class}
                      {a.symptom_class_source === "ai" && (
                        <span className="ml-1 rounded bg-accent/15 px-1 text-[10px] text-accent">ИИ</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-muted">{a.source_system}</td>
                    <td className="px-4 py-2 text-muted">{a.status ?? "—"}</td>
                    <td className="px-4 py-2 text-muted">{a.incident_id ? `INC-${String(a.incident_id).padStart(4, "0")}` : "—"}</td>
                    <td className="px-4 py-2 text-muted">{a.acknowledged_at ? "подтверждено" : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="space-y-2 md:hidden">
            {data.items.map((a) => (
              <div key={a.id} className="rounded-xl border border-border p-3 text-sm">
                <div className="mb-1 flex items-center justify-between">
                  <span className="flex items-center gap-1.5">
                    <PriorityBadge priority={a.priority} />
                    <span className="font-medium">{a.symptom_class}</span>
                  </span>
                  <span className="text-xs text-muted">{new Date(a.occurred_at).toLocaleString("ru-RU", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" })}</span>
                </div>
                <div className="text-muted">{a.object_id ?? "—"}</div>
                <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
                  <span>{a.source_system}</span>
                  <span>{a.status ?? "—"}</span>
                  <span>{a.acknowledged_at ? "ACK получен" : "ACK отсутствует"}</span>
                  {a.incident_id && <span className="text-accent">INC-{String(a.incident_id).padStart(4, "0")}</span>}
                </div>
              </div>
            ))}
          </div>
          <div className="mt-3 flex items-center justify-between text-sm text-muted">
            <span>Всего: {data.total}</span>
            <div className="flex gap-2">
              <button
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - limit))}
                className="rounded-md bg-card px-3 py-1 disabled:opacity-40"
              >
                ← назад
              </button>
              <button
                disabled={offset + limit >= data.total}
                onClick={() => setOffset(offset + limit)}
                className="rounded-md bg-card px-3 py-1 disabled:opacity-40"
              >
                вперёд →
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function PresetButton({ children, onClick }: { children: string; onClick: () => void }) {
  return (
    <button onClick={onClick} className="rounded-full border border-border px-3 py-1 text-xs text-fg hover:bg-fg/5">
      {children}
    </button>
  );
}

function FilterField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-[10px] uppercase tracking-wide text-muted">{label}</div>
      {children}
    </div>
  );
}

function PillMultiSelect({
  options,
  selected,
  onToggle,
}: {
  options: { value: string; label: string }[];
  selected: string[];
  onToggle: (value: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1">
      {options.map((opt) => (
        <button
          key={opt.value}
          onClick={() => onToggle(opt.value)}
          className={`rounded-full px-2 py-1 text-xs ${selected.includes(opt.value) ? "bg-accent text-white" : "bg-bg text-muted hover:text-fg"}`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}
