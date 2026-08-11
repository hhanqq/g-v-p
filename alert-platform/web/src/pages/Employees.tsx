import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Search, Siren, Users } from "lucide-react";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, OrgTreeEmployee, OrgTreeNode } from "../api";
import { EmptyState, PageHeader } from "../components/ui";

const STATUS_LABEL: Record<string, string> = {
  available: "на месте",
  on_duty: "на дежурстве",
  vacation: "отпуск",
  sick_leave: "больничный",
  unavailable: "недоступен",
};
const STATUS_CLASS: Record<string, string> = {
  available: "bg-emerald-500/15 text-emerald-400",
  on_duty: "bg-sky-500/15 text-sky-400",
  vacation: "bg-amber-500/15 text-amber-400",
  sick_leave: "bg-red-500/15 text-red-400",
  unavailable: "bg-slate-500/15 text-muted",
};

function nodeMatches(node: OrgTreeNode, query: string): boolean {
  if (node.name.toLowerCase().includes(query)) return true;
  return node.employees.some(
    (e) =>
      e.full_name.toLowerCase().includes(query) ||
      e.trueconf_username.toLowerCase().includes(query) ||
      (e.position ?? "").toLowerCase().includes(query),
  );
}

// Раздел «Сотрудники» доп. ТЗ: поиск раскрывает полный путь в дереве —
// собираем id всех узлов, у которых само поддерево содержит совпадение,
// не только тех, что совпали напрямую. Ветки без единого совпадения
// (ни узел, ни сотрудник) сворачиваются/остаются как есть, не подсвечены.
function collectMatchAncestors(node: OrgTreeNode, query: string): { ids: Set<number>; matched: boolean } {
  let matched = nodeMatches(node, query);
  const ids = new Set<number>();
  for (const child of node.children) {
    const result = collectMatchAncestors(child, query);
    if (result.matched) {
      matched = true;
      result.ids.forEach((id) => ids.add(id));
    }
  }
  if (matched) ids.add(node.id);
  return { ids, matched };
}

function EmployeeRow({ employee, highlighted }: { employee: OrgTreeEmployee; highlighted: boolean }) {
  return (
    <Link
      to={`/employees/${employee.id}`}
      className={`flex items-center justify-between gap-3 rounded-lg px-3 py-2 text-sm hover:bg-fg/5 ${
        highlighted ? "bg-accent/10" : ""
      }`}
    >
      <div className="flex min-w-0 items-center gap-2">
        <Users size={14} strokeWidth={1.75} className="shrink-0 text-muted" />
        <div className="min-w-0">
          <div className="truncate font-medium">
            {employee.full_name}
            {!employee.active && <span className="ml-1.5 text-[10px] uppercase text-muted">неактивен</span>}
          </div>
          <div className="truncate text-xs text-muted">{employee.position ?? employee.trueconf_username}</div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {employee.active_alerts > 0 && (
          <span className="flex items-center gap-1 rounded bg-red-500/15 px-1.5 py-0.5 text-[10px] font-medium text-red-400" title="Активные алерты">
            <Siren size={11} strokeWidth={2} />
            {employee.active_alerts}
          </span>
        )}
        <span className={`rounded px-2 py-0.5 text-[11px] font-medium ${STATUS_CLASS[employee.availability_kind] ?? "bg-slate-500/15 text-muted"}`}>
          {STATUS_LABEL[employee.availability_kind] ?? employee.availability_kind}
        </span>
      </div>
    </Link>
  );
}

function OrgNode({
  node,
  depth,
  expanded,
  toggle,
  isExpanded,
  matchIds,
  query,
}: {
  node: OrgTreeNode;
  depth: number;
  expanded: Set<number>;
  toggle: (id: number) => void;
  isExpanded: (id: number) => boolean;
  matchIds: Set<number> | null;
  query: string;
}) {
  const open = isExpanded(node.id);
  const dimmed = matchIds !== null && !matchIds.has(node.id);
  const availabilityParts = Object.entries(node.availability).filter(([, count]) => count > 0);

  return (
    <div className={dimmed ? "opacity-40" : ""}>
      <button
        onClick={() => toggle(node.id)}
        className="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left hover:bg-fg/5"
        style={{ paddingLeft: `${depth * 20 + 8}px` }}
      >
        {node.children.length > 0 || node.employees.length > 0 ? (
          open ? <ChevronDown size={15} strokeWidth={2} className="shrink-0 text-muted" /> : <ChevronRight size={15} strokeWidth={2} className="shrink-0 text-muted" />
        ) : (
          <span className="w-[15px] shrink-0" />
        )}
        <span className="truncate text-sm font-medium">{node.name}</span>
        <span className="shrink-0 text-xs text-muted">{node.kind}</span>
        <span className="ml-auto shrink-0 tabular-nums text-xs text-muted">{node.headcount} чел.</span>
        <div className="hidden shrink-0 gap-1.5 sm:flex">
          {availabilityParts.map(([kind, count]) => (
            <span key={kind} className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${STATUS_CLASS[kind] ?? "bg-slate-500/15 text-muted"}`}>
              {count}
            </span>
          ))}
        </div>
      </button>
      {open && (
        <div>
          {node.employees.map((employee) => (
            <div key={employee.id} style={{ paddingLeft: `${(depth + 1) * 20 + 8}px` }}>
              <EmployeeRow
                employee={employee}
                highlighted={query.length > 0 && (
                  employee.full_name.toLowerCase().includes(query) ||
                  employee.trueconf_username.toLowerCase().includes(query) ||
                  (employee.position ?? "").toLowerCase().includes(query)
                )}
              />
            </div>
          ))}
          {node.children.map((child) => (
            <OrgNode
              key={child.id}
              node={child}
              depth={depth + 1}
              expanded={expanded}
              toggle={toggle}
              isExpanded={isExpanded}
              matchIds={matchIds}
              query={query}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export default function Employees() {
  const { data, isLoading } = useQuery<OrgTreeNode[]>({
    queryKey: ["org-units-tree"],
    queryFn: () => api.get<OrgTreeNode[]>("/org-units/tree"),
  });
  const [rawQuery, setRawQuery] = useState("");
  const query = rawQuery.trim().toLowerCase();
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  // По умолчанию раскрыты корень + первый уровень подразделений — дальше
  // пользователь раскрывает вручную. При активном поиске это игнорируется:
  // раскрывается ровно путь до совпадений (см. matchIds ниже).
  const defaultExpanded = useMemo(() => {
    const ids = new Set<number>();
    for (const root of data ?? []) {
      ids.add(root.id);
      for (const child of root.children) ids.add(child.id);
    }
    return ids;
  }, [data]);

  const matchInfo = useMemo(() => {
    if (!query || !data) return null;
    const ids = new Set<number>();
    for (const root of data) {
      const result = collectMatchAncestors(root, query);
      result.ids.forEach((id) => ids.add(id));
    }
    return ids;
  }, [data, query]);

  const toggle = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev.size > 0 ? prev : defaultExpanded);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const isExpanded = (id: number) => (matchInfo ? matchInfo.has(id) : (expanded.size > 0 ? expanded : defaultExpanded).has(id));

  return (
    <div>
      <PageHeader title="Сотрудники" subtitle="Дерево организации: филиал → отдел → сотрудник" />

      <div className="mb-4 flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-2">
        <Search size={16} strokeWidth={1.75} className="text-muted" />
        <input
          value={rawQuery}
          onChange={(e) => setRawQuery(e.target.value)}
          placeholder="Поиск сотрудника, подразделения, должности…"
          className="w-full bg-transparent text-sm outline-none"
        />
      </div>

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && (!data || data.length === 0) && <EmptyState>Дерево организации пока не настроено</EmptyState>}
      {!isLoading && query && matchInfo && matchInfo.size === 0 && <EmptyState>Ничего не найдено по запросу «{rawQuery}»</EmptyState>}

      {!!data?.length && (
        <div className="rounded-xl border border-border bg-card p-1">
          {data.map((root) => (
            <OrgNode
              key={root.id}
              node={root}
              depth={0}
              expanded={expanded}
              toggle={toggle}
              isExpanded={isExpanded}
              matchIds={matchInfo}
              query={query}
            />
          ))}
        </div>
      )}
    </div>
  );
}
