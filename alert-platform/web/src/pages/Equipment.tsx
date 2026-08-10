import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { api, EquipmentGroup, EquipmentListItem } from "../api";
import { Card, EmptyState, PageHeader, PriorityBadge } from "../components/ui";

// Раздел «Оборудование» — drill-down CMDB-explorer (не плоский список и
// не dropdown-фильтр): филиал → категория → объект, с агрегатами на
// каждом уровне и хлебными крошками. Состояние текущего уровня живёт в
// query-параметрах (?site=&type=), поэтому ссылка на конкретный узел
// дерева копируема и переживает обновление страницы.

function Breadcrumbs({ site, siteLabel, type, typeLabel }: { site?: string; siteLabel?: string; type?: string; typeLabel?: string }) {
  return (
    <div className="mb-4 flex items-center gap-2 text-sm text-muted">
      <Link to="/equipment" className="hover:text-accent">Оборудование</Link>
      {site && (
        <>
          <span>/</span>
          <Link to={`/equipment?site=${encodeURIComponent(site)}`} className="hover:text-accent">
            {siteLabel ?? site}
          </Link>
        </>
      )}
      {type && (
        <>
          <span>/</span>
          <span className="text-fg">{typeLabel ?? type}</span>
        </>
      )}
    </div>
  );
}

function HealthLine({ group }: { group: EquipmentGroup }) {
  if (group.active_problems === 0) {
    return <span className="text-emerald-400">проблем не зафиксировано</span>;
  }
  return (
    <span className="text-red-400">
      {group.active_problems} проблем{group.active_problems === 1 ? "а" : "ы"} активна
      {group.p0_active > 0 && <> · P0: {group.p0_active}</>}
    </span>
  );
}

function GroupTile({ to, group }: { to: string; group: EquipmentGroup }) {
  return (
    <Link to={to}>
      <Card className="h-full hover:border-accent">
        <div className="flex items-start justify-between">
          <div className="text-sm font-medium">{group.label}</div>
          {group.worst_priority && <PriorityBadge priority={group.worst_priority} />}
        </div>
        <div className="mt-1 text-xs text-muted">{group.object_count} объектов</div>
        <div className="mt-3 text-xs">
          <HealthLine group={group} />
        </div>
        <div className="mt-2 grid grid-cols-2 gap-1 text-xs text-muted tabular-nums">
          <div>Открытых инцидентов: {group.open_incidents}</div>
          <div>Алертов за 24ч: {group.alerts_24h}</div>
          <div>Алертов за 30д: {group.alerts_30d}</div>
          <div>MTTR: {group.avg_mttr_minutes != null ? `${Math.round(group.avg_mttr_minutes)} мин` : "—"}</div>
        </div>
      </Card>
    </Link>
  );
}

function ObjectRow({ item }: { item: EquipmentListItem }) {
  return (
    <Link to={`/equipment/${encodeURIComponent(item.id)}`}>
      <tr className="cursor-pointer border-b border-border last:border-0 hover:bg-card">
        <td className="px-3 py-2 text-sm font-medium">{item.name}</td>
        <td className="px-3 py-2">
          {item.worst_priority ? <PriorityBadge priority={item.worst_priority} /> : (
            <span className="text-xs text-emerald-400">Normal</span>
          )}
        </td>
        <td className="px-3 py-2 text-right text-sm tabular-nums">{item.active_problems}</td>
        <td className="px-3 py-2 text-right text-sm tabular-nums">{item.open_incidents}</td>
        <td className="px-3 py-2 text-right text-sm tabular-nums">{item.alerts_24h}</td>
        <td className="px-3 py-2 text-right text-sm tabular-nums">{item.alerts_30d}</td>
        <td className="px-3 py-2 text-right text-xs text-muted">
          {item.last_event_at ? new Date(item.last_event_at).toLocaleString("ru-RU") : "—"}
        </td>
      </tr>
    </Link>
  );
}

export default function Equipment() {
  const [params] = useSearchParams();
  const site = params.get("site") ?? undefined;
  const equipmentType = params.get("type") ?? undefined;

  const { data: groups, isLoading: groupsLoading } = useQuery<EquipmentGroup[]>({
    queryKey: ["equipment-groups", site, equipmentType],
    queryFn: () => api.get<EquipmentGroup[]>(`/equipment/groups${site ? `?site=${encodeURIComponent(site)}` : ""}`),
    enabled: !equipmentType,
  });

  const { data: items, isLoading: itemsLoading } = useQuery<EquipmentListItem[]>({
    queryKey: ["equipment-leaf", site, equipmentType],
    queryFn: () =>
      api.get<EquipmentListItem[]>(
        `/equipment?site=${encodeURIComponent(site ?? "")}&equipment_type=${encodeURIComponent(equipmentType ?? "")}`
      ),
    enabled: !!equipmentType,
  });

  const siteLabel = groups?.find((g) => g.key === site)?.label;
  const isLoading = equipmentType ? itemsLoading : groupsLoading;

  return (
    <div>
      <PageHeader
        title="Оборудование"
        subtitle="Филиал → категория → объект. Кликните на карточку, чтобы провалиться внутрь структуры."
      />
      <Breadcrumbs site={site} siteLabel={siteLabel} type={equipmentType} />

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}

      {!isLoading && !equipmentType && groups?.length === 0 && <EmptyState>Ничего не найдено</EmptyState>}

      {!equipmentType && groups && groups.length > 0 && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {groups.map((group) => (
            <GroupTile
              key={group.key}
              to={site ? `/equipment?site=${encodeURIComponent(site)}&type=${encodeURIComponent(group.key)}` : `/equipment?site=${encodeURIComponent(group.key)}`}
              group={group}
            />
          ))}
        </div>
      )}

      {equipmentType && (
        <div className="overflow-x-auto rounded-xl border border-border bg-card">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-border text-xs text-muted">
                <th className="px-3 py-2">Объект</th>
                <th className="px-3 py-2">Состояние</th>
                <th className="px-3 py-2 text-right">Активные алерты</th>
                <th className="px-3 py-2 text-right">Открытые инциденты</th>
                <th className="px-3 py-2 text-right">24 ч</th>
                <th className="px-3 py-2 text-right">30 дней</th>
                <th className="px-3 py-2 text-right">Последнее событие</th>
              </tr>
            </thead>
            <tbody>
              {items?.map((item) => <ObjectRow key={item.id} item={item} />)}
            </tbody>
          </table>
          {!itemsLoading && items?.length === 0 && (
            <div className="p-6">
              <EmptyState>Объектов в этой категории не найдено</EmptyState>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
