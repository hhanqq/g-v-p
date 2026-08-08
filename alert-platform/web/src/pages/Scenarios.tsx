import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ScenarioListItem } from "../api";
import { Card, PageHeader } from "../components/ui";

export default function Scenarios() {
  const { data } = useQuery<ScenarioListItem[]>({
    queryKey: ["scenarios"],
    queryFn: () => api.get<ScenarioListItem[]>("/scenarios"),
  });

  return (
    <div>
      <div className="mb-6 flex items-start justify-between">
        <PageHeader title="Сценарии" subtitle="Визуальный no-code конструктор обработки и эскалации алертов" />
        <Link to="/scenarios/new" className="rounded-md bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90">
          + Создать сценарий
        </Link>
      </div>
      <div className="space-y-2">
        {data?.length ? (
          data.map((s) => (
            <Link key={s.id} to={`/scenarios/${s.id}/edit`}>
              <Card className="flex items-center justify-between hover:border-accent">
                <div>
                  <div className="text-sm">{s.name}</div>
                  {s.description && <div className="text-xs text-muted">{s.description}</div>}
                </div>
                <span
                  className={`rounded px-2 py-0.5 text-xs ${
                    s.status === "active" ? "bg-emerald-500/15 text-emerald-400" : "bg-fg/10 text-muted"
                  }`}
                >
                  {s.status === "active" ? "активен" : "черновик"}
                </span>
              </Card>
            </Link>
          ))
        ) : (
          <p className="text-sm text-muted">Сохранённых сценариев пока нет — создайте первый.</p>
        )}
      </div>
      <Link to="/demo" className="mt-4 inline-block rounded-md bg-accent px-4 py-2 text-sm text-white">Запустить демо-сценарий →</Link>
    </div>
  );
}
