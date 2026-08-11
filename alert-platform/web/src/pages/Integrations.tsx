import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api, BIServiceAccount, DeliveryChannelAnalytics, hasPermission, IntegrationStatus, RBACMeta } from "../api";
import { Card, PageHeader } from "../components/ui";
import { useCurrentUser } from "../auth";

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
  const { data: user } = useCurrentUser();
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

      <BISection canManage={hasPermission(user, "integrations.manage")} canRead={hasPermission(user, "integrations.read")} />

      <div className="mt-4 flex gap-3">
        <Link to="/sources" className="rounded-md bg-accent px-4 py-2 text-sm text-white">Управление источниками</Link>
        <Link to="/audit" className="rounded-md bg-card px-4 py-2 text-sm text-fg">Открыть аудит</Link>
      </div>
    </div>
  );
}

function BISection({ canManage, canRead }: { canManage: boolean; canRead: boolean }) {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [scopeType, setScopeType] = useState("site");
  const [scopeValue, setScopeValue] = useState("");
  const [scopes, setScopes] = useState<{ type: string; value: string }[]>([]);
  const [issuedToken, setIssuedToken] = useState<string | null>(null);

  const { data: accounts } = useQuery<BIServiceAccount[]>({
    queryKey: ["bi-service-accounts"],
    queryFn: () => api.get("/bi/service-accounts"),
    enabled: canRead,
  });
  const { data: meta } = useQuery<RBACMeta>({
    queryKey: ["users-meta"],
    queryFn: () => api.get("/users/meta"),
    enabled: canManage,
    staleTime: 60_000,
  });

  const createMutation = useMutation({
    mutationFn: () => api.post<{ id: number; token: string }>("/bi/service-accounts", { name, scopes }),
    onSuccess: (res) => {
      setIssuedToken(res.token);
      setName("");
      setScopes([]);
      queryClient.invalidateQueries({ queryKey: ["bi-service-accounts"] });
    },
  });
  const revokeMutation = useMutation({
    mutationFn: (id: number) => api.delete(`/bi/service-accounts/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["bi-service-accounts"] }),
  });

  if (!canRead) return null;

  return (
    <div className="mt-6">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold">BI / внешняя аналитика</h3>
        {canManage && (
          <button onClick={() => setShowForm((v) => !v)} className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white">
            + Service account
          </button>
        )}
      </div>
      <Card className="mb-3">
        <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <div><div className="text-xs text-muted">Статус</div><div className="font-medium text-emerald-400">API доступен</div></div>
          <div><div className="text-xs text-muted">Документация</div><div className="font-medium">OpenAPI, /api/v1/bi/*</div></div>
          <div><div className="text-xs text-muted">Service accounts</div><div className="font-medium tabular-nums">{accounts?.length ?? "—"}</div></div>
          <div>
            <div className="text-xs text-muted">Последний запрос</div>
            <div className="font-medium">
              {accounts?.filter((a) => a.last_used_at).sort((a, b) => (b.last_used_at! > a.last_used_at! ? 1 : -1))[0]?.last_used_at?.slice(11, 16) ?? "—"}
            </div>
          </div>
        </div>
      </Card>

      {issuedToken && (
        <Card className="mb-3 border-accent">
          <div className="mb-1 text-xs font-semibold text-accent">Токен показан один раз — сохраните сейчас</div>
          <code className="block break-all rounded bg-bg p-2 text-xs">{issuedToken}</code>
          <button onClick={() => setIssuedToken(null)} className="mt-2 text-xs text-muted hover:text-fg">Скрыть</button>
        </Card>
      )}

      {canManage && showForm && (
        <Card className="mb-3">
          <label className="mb-2 block text-xs text-muted">
            Название
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="bi-datalens-prod" className="mt-1 w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm" />
          </label>
          <div className="mb-2 text-xs text-muted">Область данных (пусто — вся компания)</div>
          <div className="mb-2 space-y-1">
            {scopes.map((s, i) => (
              <div key={i} className="flex items-center justify-between rounded bg-bg px-2 py-1 text-xs">
                <span>{meta?.scope_types.find((t) => t.value === s.type)?.label ?? s.type}: <strong>{s.value}</strong></span>
                <button onClick={() => setScopes(scopes.filter((_, idx) => idx !== i))} className="text-muted hover:text-red-400">×</button>
              </div>
            ))}
          </div>
          <div className="mb-3 flex gap-1">
            <select value={scopeType} onChange={(e) => setScopeType(e.target.value)} className="rounded-md border border-border bg-bg px-2 py-1 text-xs">
              {(meta?.scope_types ?? [{ value: "site", label: "Филиал" }]).map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
            </select>
            <input value={scopeValue} onChange={(e) => setScopeValue(e.target.value)} placeholder="значение" className="flex-1 rounded-md border border-border bg-bg px-2 py-1 text-xs" />
            <button
              onClick={() => { if (scopeValue.trim()) { setScopes([...scopes, { type: scopeType, value: scopeValue.trim() }]); setScopeValue(""); } }}
              className="rounded-md border border-border px-2 py-1 text-xs text-muted hover:bg-fg/5"
            >
              +
            </button>
          </div>
          <button
            disabled={!name.trim() || createMutation.isPending}
            onClick={() => createMutation.mutate()}
            className="w-full rounded-md bg-accent py-1.5 text-xs font-medium text-white disabled:opacity-50"
          >
            Создать и получить токен
          </button>
        </Card>
      )}

      {accounts && accounts.length > 0 && (
        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full text-sm">
            <thead className="bg-card text-left text-xs text-muted">
              <tr>
                <th className="px-3 py-2">Название</th>
                <th className="px-3 py-2">Токен</th>
                <th className="px-3 py-2">Область</th>
                <th className="px-3 py-2">Статус</th>
                <th className="px-3 py-2">Последнее использование</th>
                {canManage && <th className="px-3 py-2" />}
              </tr>
            </thead>
            <tbody>
              {accounts.map((a) => (
                <tr key={a.id} className="border-t border-border">
                  <td className="px-3 py-2">{a.name}</td>
                  <td className="px-3 py-2 text-muted">{a.token_prefix}…</td>
                  <td className="px-3 py-2 text-muted">{a.scope_count > 0 ? `${a.scope_count} огр.` : "вся компания"}</td>
                  <td className="px-3 py-2">
                    {a.active ? <span className="text-emerald-400">активен</span> : <span className="text-red-400">отозван</span>}
                  </td>
                  <td className="px-3 py-2 text-muted">{a.last_used_at?.replace("T", " ").slice(0, 16) ?? "ещё не использовался"}</td>
                  {canManage && (
                    <td className="px-3 py-2">
                      {a.active && (
                        <button onClick={() => revokeMutation.mutate(a.id)} className="text-xs text-red-400 hover:underline">
                          Отозвать
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
