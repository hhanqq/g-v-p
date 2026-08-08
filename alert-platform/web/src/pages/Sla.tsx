import { useQuery } from "@tanstack/react-query";
import { api, SlaRuleItem } from "../api";
import { Card, PageHeader, StagePlaceholder } from "../components/ui";

export default function Sla() {
  const { data } = useQuery<SlaRuleItem[]>({
    queryKey: ["sla-rules"],
    queryFn: () => api.get<SlaRuleItem[]>("/sla-rules"),
  });

  return (
    <div>
      <PageHeader title="SLA" subtitle="Правила и нормативы реакции" />
      <StagePlaceholder
        title="Редактор правил и автонапоминания — этап 2"
        description="Создание/редактирование нормативов по приоритету и филиалу, автоматический повтор уведомления ответственному при приближении дедлайна. Нарушения считаются на лету по существующим временным меткам Problem — отдельная таблица для этого не заводилась."
      />
      <div className="mt-4 space-y-2">
        {data?.length ? (
          data.map((r) => (
            <Card key={r.id} className="flex items-center justify-between">
              <div className="text-sm">{r.name} · {r.priority}{r.subsidiary ? ` · ${r.subsidiary}` : ""}</div>
              <div className="text-xs text-muted">
                реакция ≤ {r.response_minutes} мин · устранение ≤ {r.resolution_minutes} мин
              </div>
            </Card>
          ))
        ) : (
          <p className="text-sm text-muted">Правил SLA пока не задано.</p>
        )}
      </div>
    </div>
  );
}
