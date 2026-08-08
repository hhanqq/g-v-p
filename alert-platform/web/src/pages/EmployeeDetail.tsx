import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, EmployeeDetail as EmployeeDetailType } from "../api";
import { Card, PageHeader } from "../components/ui";

const STATUSES = [
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

  const { data, isLoading } = useQuery<EmployeeDetailType>({
    queryKey: ["employee", id],
    queryFn: () => api.get<EmployeeDetailType>(`/employees/${id}`),
  });

  async function setStatus(status: string) {
    setSaving(true);
    try {
      await api.post(`/employees/${id}/availability`, { status });
      await queryClient.invalidateQueries({ queryKey: ["employee", id] });
      await queryClient.invalidateQueries({ queryKey: ["employees"] });
    } finally {
      setSaving(false);
    }
  }

  if (isLoading || !data) return <div className="text-sm text-muted">Загрузка…</div>;

  return (
    <div>
      <Link to="/employees" className="text-sm text-accent">← к списку сотрудников</Link>
      <PageHeader title={data.full_name ?? data.trueconf_username} subtitle={data.position ?? "должность не указана"} />

      <div className="mb-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card><div className="text-xs text-muted">TrueConf</div><div className="text-sm">{data.trueconf_username}</div></Card>
        <Card><div className="text-xs text-muted">Телефон</div><div className="text-sm">{data.phone ?? "—"}</div></Card>
        <Card><div className="text-xs text-muted">E-mail</div><div className="text-sm">{data.email ?? "—"}</div></Card>
        <Card><div className="text-xs text-muted">Подписок</div><div className="text-sm">{data.subscriptions.length}</div></Card>
      </div>

      <Card className="mb-4">
        <h3 className="mb-3 text-sm font-semibold">Доступность</h3>
        <p className="mb-3 text-xs text-muted">
          Статус выставляется вручную (источник данных — открытый вопрос, требует уточнения источника
          интеграции с HR/TrueConf).
        </p>
        <div className="flex flex-wrap gap-2">
          {STATUSES.map((s) => (
            <button
              key={s.value}
              disabled={saving}
              onClick={() => setStatus(s.value)}
              className="rounded-md bg-bg px-3 py-1.5 text-sm hover:bg-accent hover:text-white disabled:opacity-50"
            >
              {s.label}
            </button>
          ))}
        </div>
        {data.availability_history.length > 0 && (
          <ul className="mt-4 space-y-1 text-xs text-muted">
            {data.availability_history.slice(0, 5).map((a) => (
              <li key={a.id}>
                {new Date(a.valid_from).toLocaleString("ru-RU")} — {a.status}
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card>
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
      </Card>
    </div>
  );
}
