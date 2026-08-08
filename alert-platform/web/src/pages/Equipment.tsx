import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, EquipmentListItem } from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

export default function Equipment() {
  const { data, isLoading } = useQuery<EquipmentListItem[]>({
    queryKey: ["equipment"],
    queryFn: () => api.get<EquipmentListItem[]>("/equipment"),
  });

  return (
    <div>
      <PageHeader title="Оборудование" subtitle="Карточки скважин, насосов, установок и других узлов CMDB" />

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && data?.length === 0 && <EmptyState>Оборудование не найдено</EmptyState>}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-3">
        {data?.map((eq) => (
          <Link key={eq.id} to={`/equipment/${encodeURIComponent(eq.id)}`}>
            <Card className="h-full hover:border-accent">
              <div className="text-sm font-medium">{eq.name}</div>
              <div className="mt-1 text-xs text-muted">
                {eq.equipment_type ?? eq.kind} · {eq.site}
              </div>
              {eq.ip && <div className="mt-2 text-xs text-muted">{eq.ip}</div>}
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}
