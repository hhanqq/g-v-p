import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  api,
  EquipmentGroup,
  EquipmentListItem,
  EquipmentSearchResult,
  EquipmentSummary,
} from "../api";
import { Card, PageHeader, PriorityBadge } from "../components/ui";

// Раздел «Оборудование» — интерактивное дерево (не карточки-категории и
// не набор страниц): филиал → категория → объект, раскрывается inline
// без перехода на новый экран. Каждый уровень грузится лениво (тот же
// принцип, что уже был у drill-down — GET /equipment/groups[?site=] и
// GET /equipment?site=&equipment_type=), просто теперь оба уровня живут
// на одной странице как узлы дерева, а не как отдельные "экраны".

type Status = "critical" | "warning" | "degraded" | "normal";

function statusOf(activeProblems: number, worstPriority: string | null): Status {
  if (worstPriority === "P0") return "critical";
  if (worstPriority === "P1") return "warning";
  if (activeProblems > 0) return "degraded";
  return "normal";
}

const STATUS_LABEL: Record<Status, string> = {
  critical: "Critical", warning: "Warning", degraded: "Degraded", normal: "Normal",
};
const STATUS_CLASS: Record<Status, string> = {
  critical: "bg-red-500/15 text-red-400",
  warning: "bg-orange-500/15 text-orange-400",
  degraded: "bg-amber-500/15 text-amber-400",
  normal: "bg-emerald-500/15 text-emerald-400",
};

function StatusBadge({ status }: { status: Status }) {
  return (
    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${STATUS_CLASS[status]}`}>
      {STATUS_LABEL[status]}
    </span>
  );
}

type FilterKind = "all" | "problems" | "p0p1" | "incidents";
const FILTERS: { value: FilterKind; label: string }[] = [
  { value: "all", label: "Все" },
  { value: "problems", label: "С проблемами" },
  { value: "p0p1", label: "P0/P1" },
  { value: "incidents", label: "Открытые инциденты" },
];

function matchesFilter(filter: FilterKind, activeProblems: number, worstPriority: string | null, openIncidents: number): boolean {
  switch (filter) {
    case "problems": return activeProblems > 0;
    case "p0p1": return worstPriority === "P0" || worstPriority === "P1";
    case "incidents": return openIncidents > 0;
    default: return true;
  }
}

// Общая строка дерева — филиал/категория/объект используют один и тот же
// визуальный ряд колонок (раздел I.3 ТЗ), leaf показывает "тип" вместо
// количества дочерних объектов.
function TreeRow({
  depth, expandable, expanded, onToggle, onSelectLabel, label, isSelected,
  status, countLabel, activeProblems, openIncidents, alerts24h, alerts30d, lastEventAt,
}: {
  depth: number; expandable: boolean; expanded?: boolean; onToggle?: () => void; onSelectLabel: () => void;
  label: string; isSelected?: boolean; status: Status; countLabel: string;
  activeProblems: number; openIncidents: number; alerts24h: number; alerts30d: number; lastEventAt: string | null;
}) {
  return (
    <div
      className={`grid grid-cols-[1fr_repeat(5,minmax(0,72px))] items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-fg/5 ${isSelected ? "bg-accent/10" : ""}`}
      style={{ paddingLeft: depth * 20 + 8 }}
    >
      <div className="flex min-w-0 items-center gap-1.5">
        {expandable ? (
          <button onClick={onToggle} className="shrink-0 rounded p-0.5 hover:bg-fg/10" aria-label="Развернуть">
            <ChevronRight size={14} className={`transition-transform ${expanded ? "rotate-90" : ""}`} />
          </button>
        ) : (
          <span className="inline-block w-[22px] shrink-0" />
        )}
        <button onClick={onSelectLabel} className="truncate text-left hover:text-accent hover:underline">
          {label}
        </button>
        <StatusBadge status={status} />
      </div>
      <div className="text-right text-xs text-muted tabular-nums">{countLabel}</div>
      <div className="text-right text-xs tabular-nums">{activeProblems || "—"}</div>
      <div className="text-right text-xs tabular-nums">{openIncidents || "—"}</div>
      <div className="text-right text-xs tabular-nums">{alerts24h || "—"}</div>
      <div className="text-right text-xs tabular-nums">
        {lastEventAt ? new Date(lastEventAt).toLocaleString("ru-RU", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }) : "—"}
      </div>
    </div>
  );
}

function ObjectLeaf({
  item, depth, isSelected, onSelect,
}: { item: EquipmentListItem; depth: number; isSelected: boolean; onSelect: (id: string) => void }) {
  return (
    <TreeRow
      depth={depth} expandable={false} onSelectLabel={() => onSelect(item.id)} isSelected={isSelected}
      label={item.name} status={statusOf(item.active_problems, item.worst_priority)}
      countLabel={item.equipment_type ?? item.kind}
      activeProblems={item.active_problems} openIncidents={item.open_incidents}
      alerts24h={item.alerts_24h} alerts30d={item.alerts_30d} lastEventAt={item.last_event_at}
    />
  );
}

function CategoryBranch({
  site, group, depth, expandedKeys, onToggle, filter, selectedId, onSelect,
}: {
  site: string; group: EquipmentGroup; depth: number; expandedKeys: Set<string>;
  onToggle: (key: string) => void; filter: FilterKind; selectedId: string | null; onSelect: (id: string) => void;
}) {
  const key = `${site}|${group.key}`;
  const expanded = expandedKeys.has(key);
  const { data: items, isLoading } = useQuery<EquipmentListItem[]>({
    queryKey: ["equipment-leaf", site, group.key],
    queryFn: () => api.get<EquipmentListItem[]>(`/equipment?site=${encodeURIComponent(site)}&equipment_type=${encodeURIComponent(group.key)}`),
    enabled: expanded,
  });
  const filtered = useMemo(
    () => (items ?? []).filter((i) => matchesFilter(filter, i.active_problems, i.worst_priority, i.open_incidents)),
    [items, filter]
  );
  if (filter !== "all" && !matchesFilter(filter, group.active_problems, group.worst_priority, group.open_incidents)) {
    return null;
  }
  return (
    <div>
      <TreeRow
        depth={depth} expandable expanded={expanded} onToggle={() => onToggle(key)} onSelectLabel={() => onToggle(key)}
        label={group.label} status={statusOf(group.active_problems, group.worst_priority)}
        countLabel={`${group.object_count} объектов`} activeProblems={group.active_problems}
        openIncidents={group.open_incidents} alerts24h={group.alerts_24h} alerts30d={group.alerts_30d}
        lastEventAt={null}
      />
      {expanded && (
        <div>
          {isLoading && <div className="py-1 text-xs text-muted" style={{ paddingLeft: (depth + 1) * 20 + 8 }}>Загрузка…</div>}
          {!isLoading && filtered.length === 0 && (
            <div className="py-1 text-xs text-muted" style={{ paddingLeft: (depth + 1) * 20 + 8 }}>Ничего не найдено</div>
          )}
          {filtered.map((item) => (
            <ObjectLeaf key={item.id} item={item} depth={depth + 1} isSelected={selectedId === item.id} onSelect={onSelect} />
          ))}
        </div>
      )}
    </div>
  );
}

function SiteBranch({
  group, depth, expandedKeys, onToggle, filter, selectedId, onSelect,
}: {
  group: EquipmentGroup; depth: number; expandedKeys: Set<string>; onToggle: (key: string) => void;
  filter: FilterKind; selectedId: string | null; onSelect: (id: string) => void;
}) {
  const key = group.key;
  const expanded = expandedKeys.has(key);
  const { data: categories, isLoading } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups", group.key],
    queryFn: () => api.get<EquipmentGroup[]>(`/equipment/groups?site=${encodeURIComponent(group.key)}`),
    enabled: expanded,
  });
  if (filter !== "all" && !matchesFilter(filter, group.active_problems, group.worst_priority, group.open_incidents)) {
    return null;
  }
  return (
    <div>
      <TreeRow
        depth={depth} expandable expanded={expanded} onToggle={() => onToggle(key)} onSelectLabel={() => onToggle(key)}
        label={group.label} status={statusOf(group.active_problems, group.worst_priority)}
        countLabel={`${group.object_count} объектов`} activeProblems={group.active_problems}
        openIncidents={group.open_incidents} alerts24h={group.alerts_24h} alerts30d={group.alerts_30d}
        lastEventAt={null}
      />
      {expanded && (
        <div>
          {isLoading && <div className="py-1 text-xs text-muted" style={{ paddingLeft: (depth + 1) * 20 + 8 }}>Загрузка…</div>}
          {categories?.map((cat) => (
            <CategoryBranch
              key={cat.key} site={group.key} group={cat} depth={depth + 1} expandedKeys={expandedKeys}
              onToggle={onToggle} filter={filter} selectedId={selectedId} onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function QuickPanel({ objectId, onClose }: { objectId: string; onClose: () => void }) {
  const { data: summary } = useQuery<EquipmentSummary>({
    queryKey: ["equipment-summary", objectId],
    queryFn: () => api.get<EquipmentSummary>(`/equipment/${encodeURIComponent(objectId)}/summary`),
  });
  const { data } = useQuery<EquipmentListItem>({
    queryKey: ["equipment-basic", objectId],
    queryFn: () => api.get<EquipmentListItem>(`/equipment/${encodeURIComponent(objectId)}`),
  });

  return (
    <Card className="sticky top-4 h-fit">
      <div className="mb-3 flex items-start justify-between">
        <div>
          <div className="text-sm font-semibold">{data?.name ?? objectId}</div>
          <div className="text-xs text-muted">{data?.site} {data?.equipment_type ? `· ${data.equipment_type}` : ""}</div>
        </div>
        <button onClick={onClose} className="rounded p-1 text-muted hover:bg-fg/10" aria-label="Закрыть">
          <X size={14} />
        </button>
      </div>
      <div className="mb-3">
        <StatusBadge status={statusOf(summary?.active_problems ?? 0, summary?.worst_priority ?? null)} />
      </div>
      <div className="grid grid-cols-2 gap-2 text-sm">
        <div><div className="text-xs text-muted">Активные проблемы</div>{summary?.active_problems ?? "—"}</div>
        <div><div className="text-xs text-muted">Инциденты</div>{summary?.open_incidents ?? "—"}</div>
        <div><div className="text-xs text-muted">Алерты 24ч</div>{summary?.alerts_24h ?? "—"}</div>
        <div><div className="text-xs text-muted">Алерты 30д</div>{summary?.alerts_30d ?? "—"}</div>
      </div>
      {data && (
        <div className="mt-3 space-y-1 text-xs text-muted">
          {data.ip && <div>IP: {data.ip}</div>}
          {data.fqdn && <div>FQDN: {data.fqdn}</div>}
        </div>
      )}
      <Link to={`/equipment/${encodeURIComponent(objectId)}`} className="mt-4 block rounded-md bg-accent px-3 py-1.5 text-center text-sm text-white hover:opacity-90">
        Открыть полностью →
      </Link>
    </Card>
  );
}

export default function Equipment() {
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState<FilterKind>("all");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [searchText, setSearchText] = useState("");
  const [searchResults, setSearchResults] = useState<EquipmentSearchResult[] | null>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  const { data: sites, isLoading } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups", "root"],
    queryFn: () => api.get<EquipmentGroup[]>("/equipment/groups"),
  });

  function toggle(key: string) {
    setExpandedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  useEffect(() => {
    clearTimeout(debounceRef.current);
    if (searchText.trim().length < 2) {
      setSearchResults(null);
      return;
    }
    debounceRef.current = setTimeout(async () => {
      const results = await api.get<EquipmentSearchResult[]>(`/equipment/search?q=${encodeURIComponent(searchText.trim())}`);
      setSearchResults(results);
    }, 300);
    return () => clearTimeout(debounceRef.current);
  }, [searchText]);

  function revealResult(result: EquipmentSearchResult) {
    setExpandedKeys((prev) => {
      const next = new Set(prev);
      next.add(result.site);
      if (result.equipment_type) next.add(`${result.site}|${result.equipment_type}`);
      return next;
    });
    setSelectedId(result.id);
    setSearchResults(null);
    setSearchText("");
  }

  return (
    <div>
      <PageHeader
        title="Оборудование"
        subtitle="Инфраструктура компании как дерево: филиал → категория → объект. Раскрывается на месте, без перехода на новый экран."
      />

      <div className="mb-3 flex flex-wrap items-center gap-3">
        <div className="relative w-72 max-w-full">
          <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
          <input
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            placeholder="Поиск оборудования…"
            className="w-full rounded-md border border-border bg-bg py-1.5 pl-8 pr-2 text-sm"
          />
          {searchResults && searchResults.length > 0 && (
            <div className="absolute z-10 mt-1 w-full rounded-md border border-border bg-card shadow-lg">
              {searchResults.map((r) => (
                <button
                  key={r.id}
                  onClick={() => revealResult(r)}
                  className="block w-full truncate px-3 py-1.5 text-left text-sm hover:bg-fg/5"
                >
                  <span className="font-medium">{r.name}</span>
                  <span className="text-muted"> — {r.site_label}{r.category_label ? ` / ${r.category_label}` : ""}</span>
                </button>
              ))}
            </div>
          )}
          {searchResults && searchResults.length === 0 && (
            <div className="absolute z-10 mt-1 w-full rounded-md border border-border bg-card px-3 py-1.5 text-sm text-muted shadow-lg">
              Ничего не найдено
            </div>
          )}
        </div>
        <div className="flex flex-wrap gap-1.5">
          {FILTERS.map((f) => (
            <button
              key={f.value}
              onClick={() => setFilter(f.value)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium ${filter === f.value ? "bg-accent text-white" : "bg-card text-muted hover:bg-fg/5"}`}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      <div className={`grid gap-4 ${selectedId ? "lg:grid-cols-[1fr_280px]" : ""}`}>
        <div className="rounded-xl border border-border bg-card p-2">
          <div className="grid grid-cols-[1fr_repeat(5,minmax(0,72px))] gap-2 border-b border-border px-2 pb-1.5 text-[10px] uppercase text-muted">
            <div>Название</div>
            <div className="text-right">Объекты</div>
            <div className="text-right">Проблемы</div>
            <div className="text-right">Инциденты</div>
            <div className="text-right">24ч</div>
            <div className="text-right">Событие</div>
          </div>
          {isLoading && <div className="p-3 text-sm text-muted">Загрузка…</div>}
          {sites?.map((site) => (
            <SiteBranch
              key={site.key} group={site} depth={0} expandedKeys={expandedKeys} onToggle={toggle}
              filter={filter} selectedId={selectedId} onSelect={setSelectedId}
            />
          ))}
        </div>
        {selectedId && <QuickPanel objectId={selectedId} onClose={() => setSelectedId(null)} />}
      </div>
    </div>
  );
}
