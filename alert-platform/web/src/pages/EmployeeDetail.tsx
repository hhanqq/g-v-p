import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, ChangeHistoryItem, EmployeeDetail as EmployeeDetailType, SubscriptionSuggestion } from "../api";
import { Card, PageHeader, PriorityBadge, StatusBadge } from "../components/ui";

const NOTIFICATION_TYPE_LABEL: Record<string, string> = {
  NEW: "новый алерт",
  CLOSURE: "закрытие",
  SUPPLEMENT: "ИИ-сводка",
  DUPLICATE_NOTE: "отметка о дубле",
  SCENARIO: "сценарий",
  SLA_BREACH: "нарушение SLA",
};

const KINDS = [
  { value: "available", label: "на месте" },
  { value: "shift", label: "смена" },
  { value: "on_call", label: "на связи" },
  { value: "vacation", label: "отпуск" },
  { value: "sick_leave", label: "больничный" },
  { value: "unavailable", label: "недоступен" },
];

export default function EmployeeDetail() {
  const { id } = useParams();
  const queryClient = useQueryClient();
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editSaving, setEditSaving] = useState(false);
  const [form, setForm] = useState({ full_name: "", phone: "", email: "", position: "", active: true });

  const { data, isLoading } = useQuery<EmployeeDetailType>({
    queryKey: ["employee", id],
    queryFn: () => api.get<EmployeeDetailType>(`/employees/${id}`),
  });

  const { data: history } = useQuery<ChangeHistoryItem[]>({
    queryKey: ["employee-history", id],
    queryFn: () => api.get<ChangeHistoryItem[]>(`/employees/${id}/history`),
  });

  const { data: suggestion } = useQuery<SubscriptionSuggestion | null>({
    queryKey: ["employee-subscription-suggestion", id],
    queryFn: () => api.get<SubscriptionSuggestion | null>(`/employees/${id}/subscription-suggestion`),
    enabled: !!data && data.subscriptions.length === 0,
  });

  async function setStatus(kind: string) {
    setSaving(true);
    try {
      await api.post(`/employees/${id}/availability`, { kind });
      await queryClient.invalidateQueries({ queryKey: ["employee", id] });
      await queryClient.invalidateQueries({ queryKey: ["employees"] });
    } finally {
      setSaving(false);
    }
  }

  function startEditing() {
    if (!data) return;
    setForm({
      full_name: data.full_name ?? "", phone: data.phone ?? "", email: data.email ?? "",
      position: data.position ?? "", active: data.active,
    });
    setEditing(true);
  }

  async function saveEmployee() {
    setEditSaving(true);
    try {
      await api.put(`/employees/${id}`, form);
      await queryClient.invalidateQueries({ queryKey: ["employee", id] });
      await queryClient.invalidateQueries({ queryKey: ["employees"] });
      setEditing(false);
    } finally {
      setEditSaving(false);
    }
  }

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  return (
    <div>
      <Link to="/employees" className="text-sm text-accent">← к списку сотрудников</Link>
      <div className="flex items-start justify-between">
        <PageHeader title={data.full_name ?? data.trueconf_username} subtitle={data.position ?? "должность не указана"} />
        {!editing && (
          <button
            onClick={startEditing}
            className="mt-1 shrink-0 rounded-md border border-border bg-card px-3 py-1.5 text-sm hover:bg-accent hover:text-white"
          >
            Редактировать
          </button>
        )}
      </div>

      {editing ? (
        <Card className="mb-4">
          <h3 className="mb-3 text-sm font-semibold">Редактирование сотрудника</h3>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <input value={form.full_name} onChange={(e) => setForm({ ...form, full_name: e.target.value })}
              placeholder="ФИО" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.position} onChange={(e) => setForm({ ...form, position: e.target.value })}
              placeholder="Должность" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })}
              placeholder="Телефон" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
            <input value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })}
              placeholder="E-mail" className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm" />
          </div>
          <label className="mt-3 flex items-center gap-2 text-sm">
            <input type="checkbox" checked={form.active} onChange={(e) => setForm({ ...form, active: e.target.checked })} />
            Активен (действующий сотрудник)
          </label>
          <div className="mt-3 flex gap-2">
            <button onClick={saveEmployee} disabled={editSaving}
              className="rounded-md bg-accent px-4 py-2 text-sm text-white disabled:opacity-50">
              {editSaving ? "Сохранение…" : "Сохранить"}
            </button>
            <button onClick={() => setEditing(false)} disabled={editSaving}
              className="rounded-md border border-border px-4 py-2 text-sm hover:bg-bg">
              Отмена
            </button>
          </div>
        </Card>
      ) : (
        <div className="mb-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
          <Card><div className="text-xs text-muted">TrueConf</div><div className="text-sm">{data.trueconf_username}</div></Card>
          <Card><div className="text-xs text-muted">Телефон</div><div className="text-sm">{data.phone ?? "—"}</div></Card>
          <Card><div className="text-xs text-muted">E-mail</div><div className="text-sm">{data.email ?? "—"}</div></Card>
          <Card><div className="text-xs text-muted">Подписок</div><div className="text-sm">{data.subscriptions.length}</div></Card>
        </div>
      )}

      <Card className="mb-4">
        <h3 className="mb-3 text-sm font-semibold">Доступность</h3>
        <p className="mb-3 text-xs text-muted">
          Быстрая кнопка — интервал «с этого момента, бессрочно», автоматически закрывает предыдущий
          открытый. Источник — ручной ввод, не HR-фид; типизированные интервалы с датами начала/конца
          и делегированием — на карточке доступности (следующий этап).
        </p>
        <div className="flex flex-wrap gap-2">
          {KINDS.map((k) => (
            <button
              key={k.value}
              disabled={saving}
              onClick={() => setStatus(k.value)}
              className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-accent hover:text-white disabled:opacity-50"
            >
              {k.label}
            </button>
          ))}
        </div>
        {data.availability_history.length > 0 && (
          <ul className="mt-4 space-y-1 text-xs text-muted">
            {data.availability_history.slice(0, 5).map((a) => (
              <li key={a.id}>
                {new Date(a.valid_from).toLocaleString("ru-RU")} — {a.kind}
                {a.valid_until ? ` — до ${new Date(a.valid_until).toLocaleString("ru-RU")}` : ""}
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card className="mb-4">
        <h3 className="mb-3 text-sm font-semibold">Полученные алерты</h3>
        <p className="mb-3 text-xs text-muted">
          Уведомления, реально отправленные этому сотруднику ботом TrueConf — по факту доставки, а не
          по текущим подпискам (подписки могли измениться уже после отправки).
        </p>
        {data.recent_alerts.length === 0 && <p className="text-sm text-muted">Алертов пока не было.</p>}
        <div className="space-y-2">
          {data.recent_alerts.map((a) => (
            <div key={a.notification_id} className="flex items-center justify-between rounded-lg bg-bg px-3 py-2">
              <div className="text-sm">
                <span className="text-muted">{NOTIFICATION_TYPE_LABEL[a.type] ?? a.type}</span>
                {" · "}
                {a.symptom_class}
                {a.object_id && <span className="text-muted"> · {a.object_id}</span>}
                <div className="text-xs text-muted">
                  {new Date(a.sent_at ?? a.created_at).toLocaleString("ru-RU")}
                  {a.status !== "sent" && ` · ${a.status}`}
                </div>
              </div>
              <div className="flex shrink-0 gap-2">
                <PriorityBadge priority={a.priority} />
                <StatusBadge status={a.problem_status} />
              </div>
            </div>
          ))}
        </div>
      </Card>

      <Card className="mb-4">
        <h3 className="mb-3 text-sm font-semibold">Подписки на уведомления</h3>
        {data.subscriptions.length === 0 && <p className="text-sm text-muted">Подписок нет.</p>}
        <ul className="space-y-2">
          {data.subscriptions.map((s) => (
            <li key={s.id} className="text-sm text-muted">
              {s.subsidiary ?? "любой филиал"} · {s.service_id ?? "любой сервис"} ·{" "}
              {s.priority_threshold ? `приоритет ≤ ${s.priority_threshold}` : "любой приоритет"}
            </li>
          ))}
        </ul>
        {data.subscriptions.length === 0 && suggestion && (
          <div className="mt-3 rounded-lg border border-accent/30 bg-accent/10 p-3">
            <div className="text-xs font-semibold uppercase tracking-wide text-accent">Рекомендация ИИ на основе истории</div>
            <p className="mt-1 text-sm">{suggestion.explanation}</p>
            <p className="mt-1 text-xs text-muted">
              {suggestion.subsidiary ?? "любой филиал"} · {suggestion.service_id ?? "любой сервис"} ·{" "}
              {suggestion.priority_threshold ? `приоритет ≤ ${suggestion.priority_threshold}` : "любой приоритет"}
              {" · по "}{suggestion.peer_count}{" "}
              {suggestion.peer_count === 1 ? "коллеге из групп" : "коллегам из групп"}
            </p>
          </div>
        )}
      </Card>

      <Card>
        <h3 className="mb-1 text-sm font-semibold">История изменений</h3>
        <p className="mb-3 text-xs text-muted">Правки самой карточки сотрудника — кто, что и когда менял.</p>
        {(!history || history.length === 0) ? (
          <p className="text-sm text-muted">Изменений записи пока не было.</p>
        ) : (
          <div className="space-y-2">
            {history.map((h) => (
              <div key={h.id} className="flex items-center justify-between rounded-lg bg-bg px-3 py-2 text-sm">
                <span>{h.actor} <span className="text-muted">({h.actor_role})</span> · {h.action}</span>
                <span className="text-xs text-muted">{new Date(h.occurred_at).toLocaleString("ru-RU")}</span>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
