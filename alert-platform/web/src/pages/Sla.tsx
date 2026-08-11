import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useState } from "react";
import { api, SlaRuleItem } from "../api";
import { Card, PageHeader } from "../components/ui";

const PRIORITIES = ["P0", "P1", "P2", "P3"];

export default function Sla() {
  const queryClient = useQueryClient();
  const { data } = useQuery<SlaRuleItem[]>({
    queryKey: ["sla-rules"],
    queryFn: () => api.get<SlaRuleItem[]>("/sla-rules"),
  });

  const [name, setName] = useState("");
  const [priority, setPriority] = useState("P1");
  const [subsidiary, setSubsidiary] = useState("");
  const [responseMinutes, setResponseMinutes] = useState(30);
  const [resolutionMinutes, setResolutionMinutes] = useState(240);
  const [saving, setSaving] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    setSaving(true);
    try {
      await api.post("/sla-rules", {
        name,
        priority,
        subsidiary: subsidiary.trim() || undefined,
        response_minutes: responseMinutes,
        resolution_minutes: resolutionMinutes,
      });
      await queryClient.invalidateQueries({ queryKey: ["sla-rules"] });
      setName("");
      setSubsidiary("");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div>
      <PageHeader title="SLA" helpArticle="sla" />

      <Card className="mb-4">
        <h3 className="mb-3 text-sm font-semibold">Новое правило</h3>
        <form onSubmit={submit} className="grid grid-cols-1 gap-3 md:grid-cols-5">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Название"
            className="rounded-md border border-border bg-bg px-3 py-2 text-sm md:col-span-2"
          />
          <select
            value={priority}
            onChange={(e) => setPriority(e.target.value)}
            className="rounded-md border border-border bg-bg px-3 py-2 text-sm"
          >
            {PRIORITIES.map((p) => (
              <option key={p} value={p}>{p}</option>
            ))}
          </select>
          <input
            value={subsidiary}
            onChange={(e) => setSubsidiary(e.target.value)}
            placeholder="Филиал (необязательно)"
            className="rounded-md border border-border bg-bg px-3 py-2 text-sm"
          />
          <button
            type="submit"
            disabled={saving}
            className="rounded-md bg-accent px-3 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
          >
            Добавить
          </button>
          <label className="text-xs text-muted md:col-span-2">
            Реакция, мин
            <input
              type="number"
              min={1}
              value={responseMinutes}
              onChange={(e) => setResponseMinutes(Number(e.target.value) || 0)}
              className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg"
            />
          </label>
          <label className="text-xs text-muted md:col-span-2">
            Устранение, мин
            <input
              type="number"
              min={1}
              value={resolutionMinutes}
              onChange={(e) => setResolutionMinutes(Number(e.target.value) || 0)}
              className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg"
            />
          </label>
        </form>
        <p className="mt-3 text-xs text-muted">
          Нарушения считаются на лету по времени открытия проблемы — отдельной таблицы для этого нет. При
          превышении «Реакция, мин» ответственным (раздел 8) уходит одно напоминание за жизненный цикл проблемы.
        </p>
      </Card>

      <div className="space-y-2">
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
