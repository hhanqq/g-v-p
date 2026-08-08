import { useQuery } from "@tanstack/react-query";
import { api, IntegrationStatus } from "../api";
import { Card, PageHeader } from "../components/ui";
import { Link } from "react-router-dom";

const STATUS_STYLE: Record<IntegrationStatus["status"], string> = {
  active: "bg-emerald-500/15 text-emerald-400",
  planned: "bg-slate-500/15 text-slate-400",
  open_question: "bg-amber-500/15 text-amber-400",
};

const STATUS_LABEL: Record<IntegrationStatus["status"], string> = {
  active: "работает",
  planned: "запланировано",
  open_question: "открытый вопрос",
};

export default function Integrations() {
  const { data } = useQuery<IntegrationStatus[]>({
    queryKey: ["integrations"],
    queryFn: () => api.get<IntegrationStatus[]>("/integrations/status"),
  });

  return (
    <div>
      <PageHeader title="Интеграции" subtitle="TrueConf, SMS, email, системы мониторинга, HR-системы" />
      <div className="space-y-2">
        {data?.map((i) => (
          <Card key={i.name} className="flex items-center justify-between">
            <div>
              <div className="text-sm font-medium">{i.name}</div>
              <div className="text-xs text-muted">{i.detail}</div>
            </div>
            <span className={`rounded px-2 py-1 text-xs font-medium ${STATUS_STYLE[i.status]}`}>
              {STATUS_LABEL[i.status]}
            </span>
          </Card>
        ))}
      </div>
      <div className="mt-4 flex gap-3">
        <Link to="/sources" className="rounded-md bg-accent px-4 py-2 text-sm text-white">Управление источниками</Link>
        <Link to="/audit" className="rounded-md bg-card px-4 py-2 text-sm text-slate-200">Открыть аудит</Link>
      </div>
    </div>
  );
}
