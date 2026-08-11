import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, EmployeeListItem } from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

const STATUS_LABEL: Record<string, string> = {
  available: "на месте",
  vacation: "отпуск",
  sick_leave: "больничный",
  shift: "смена",
  on_call: "на связи",
  unavailable: "недоступен",
};

export default function Employees() {
  const { data, isLoading } = useQuery<EmployeeListItem[]>({
    queryKey: ["employees"],
    queryFn: () => api.get<EmployeeListItem[]>("/employees"),
  });

  return (
    <div>
      <PageHeader title="Сотрудники" />

      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && data?.length === 0 && <EmptyState>Сотрудников пока нет</EmptyState>}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-3">
        {data?.map((e) => (
          <Link key={e.id} to={`/employees/${e.id}`}>
            <Card className="h-full hover:border-accent">
              <div className="flex items-start justify-between">
                <div>
                  <div className="text-sm font-medium">{e.full_name ?? e.trueconf_username}</div>
                  <div className="text-xs text-muted">{e.position ?? e.trueconf_username}</div>
                </div>
                {!e.active && <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-[10px] text-red-400">неактивен</span>}
              </div>
              <div className="mt-3 flex items-center justify-between text-xs text-muted">
                <span>{e.availability_status ? STATUS_LABEL[e.availability_status] ?? e.availability_status : "статус не задан"}</span>
                <span>подписок: {e.subscription_count}</span>
              </div>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}
