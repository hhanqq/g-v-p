import { useQuery } from "@tanstack/react-query";
import { api, DeliveryChannelAnalytics, IntegrationStatus } from "../api";
import { Card, PageHeader } from "../components/ui";
import { Link } from "react-router-dom";

const CHANNEL_LABEL: Record<string, string> = { trueconf: "TrueConf", email: "Email" };

const STATUS_STYLE: Record<IntegrationStatus["status"], string> = {
  active: "bg-emerald-500/15 text-emerald-400",
  planned: "bg-slate-500/15 text-muted",
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
  const { data: analytics } = useQuery<DeliveryChannelAnalytics[]>({
    queryKey: ["delivery-analytics"],
    queryFn: () => api.get<DeliveryChannelAnalytics[]>("/integrations/delivery-analytics"),
    refetchInterval: 30000,
  });

  return (
    <div>
      <PageHeader title="Интеграции" />
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

      {analytics && analytics.length > 0 && (
        <div className="mt-6">
          <h3 className="mb-2 text-sm font-semibold">Доставка по каналам</h3>
          <p className="mb-3 text-xs text-muted">
            «Отправлено» означает, что канал принял сообщение (SMTP accepted / TrueConf API) — без
            корпоративного механизма read-receipt мы не знаем, прочитал ли получатель письмо, поэтому
            не заявляем это как «прочитано».
          </p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {analytics.map((a) => (
              <Card key={a.channel}>
                <div className="text-sm font-medium">{CHANNEL_LABEL[a.channel] ?? a.channel}</div>
                <div className="mt-1 text-xs text-muted">{a.total} сообщений всего</div>
                <div className="mt-2 flex gap-4 text-sm tabular-nums">
                  <span className="text-emerald-400">
                    Отправлено {a.sent_pct != null ? `${a.sent_pct.toFixed(1)}%` : "—"}
                  </span>
                  <span className="text-red-400">
                    Ошибок {a.failed_pct != null ? `${a.failed_pct.toFixed(1)}%` : "—"}
                  </span>
                </div>
              </Card>
            ))}
          </div>
        </div>
      )}

      <div className="mt-4 flex gap-3">
        <Link to="/sources" className="rounded-md bg-accent px-4 py-2 text-sm text-white">Управление источниками</Link>
        <Link to="/audit" className="rounded-md bg-card px-4 py-2 text-sm text-fg">Открыть аудит</Link>
      </div>
    </div>
  );
}
