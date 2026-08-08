import { useQuery } from "@tanstack/react-query";
import { api, ScenarioListItem } from "../api";
import { Card, PageHeader, StagePlaceholder } from "../components/ui";

export default function Scenarios() {
  const { data } = useQuery<ScenarioListItem[]>({
    queryKey: ["scenarios"],
    queryFn: () => api.get<ScenarioListItem[]>("/scenarios"),
  });

  return (
    <div>
      <PageHeader title="Сценарии" subtitle="Визуальный no-code конструктор обработки и эскалации алертов" />
      <StagePlaceholder
        title="Редактор графа сценариев — этап 2"
        description="Drag-and-drop конструктор на ReactFlow (условия по приоритету/филиалу/сервису → действия: уведомить, ждать, эскалировать) с реальным исполнением поверх маршрутизации. Здесь — только список сохранённых сценариев."
      />
      <div className="mt-4 space-y-2">
        {data?.length ? (
          data.map((s) => (
            <Card key={s.id} className="flex items-center justify-between">
              <div>
                <div className="text-sm">{s.name}</div>
                {s.description && <div className="text-xs text-muted">{s.description}</div>}
              </div>
              <span className="rounded bg-white/10 px-2 py-0.5 text-xs text-muted">{s.status}</span>
            </Card>
          ))
        ) : (
          <p className="text-sm text-muted">Сохранённых сценариев пока нет.</p>
        )}
      </div>
    </div>
  );
}
