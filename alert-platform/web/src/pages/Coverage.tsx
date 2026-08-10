import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { api, CoveragePolicy, GroupListItem, PolicyGapsResponse } from "../api";
import { Card, PageHeader } from "../components/ui";

function toISO(date: Date): string {
  return date.toISOString().slice(0, 19);
}

export default function Coverage() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [groupID, setGroupID] = useState<number | null>(null);
  const [minAvailable, setMinAvailable] = useState(1);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const from = useMemo(() => new Date(), []);
  const to = useMemo(() => new Date(from.getTime() + 30 * 86400000), [from]);

  const { data: policies } = useQuery<CoveragePolicy[]>({
    queryKey: ["coverage-policies"],
    queryFn: () => api.get<CoveragePolicy[]>("/coverage/policies"),
  });
  const { data: groups } = useQuery<GroupListItem[]>({
    queryKey: ["groups"],
    queryFn: () => api.get<GroupListItem[]>("/groups"),
  });
  const { data: gapsByPolicy } = useQuery<PolicyGapsResponse[]>({
    queryKey: ["coverage-gaps", from.toISOString(), to.toISOString()],
    queryFn: () => api.get<PolicyGapsResponse[]>(`/coverage/gaps?from=${toISO(from)}&to=${toISO(to)}`),
    refetchInterval: 30000,
  });

  async function createPolicy() {
    if (!name.trim() || !groupID) {
      setError("Укажите название и группу");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await api.post("/coverage/policies", { name, group_id: groupID, min_available: minAvailable });
      await queryClient.invalidateQueries({ queryKey: ["coverage-policies"] });
      setName("");
      setGroupID(null);
      setMinAvailable(1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Не удалось создать политику");
    } finally {
      setSaving(false);
    }
  }

  async function deletePolicy(id: number) {
    await api.delete(`/coverage/policies/${id}`);
    await queryClient.invalidateQueries({ queryKey: ["coverage-policies"] });
  }

  return (
    <div>
      <PageHeader
        title="Покрытие"
        subtitle="Минимальное число одновременно доступных дежурных для критичных групп — обнаружение пробелов по календарю доступности"
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <h3 className="mb-3 text-sm font-semibold">Политики и пробелы (ближайшие 30 дней)</h3>
          <div className="space-y-3">
            {policies?.length ? (
              policies.map((p) => {
                const gaps = gapsByPolicy?.find((g) => g.policy_id === p.id)?.gaps ?? [];
                return (
                  <div key={p.id} className="rounded-md border border-border p-3 text-sm">
                    <div className="flex items-center justify-between">
                      <div>
                        <span className="font-medium text-fg">{p.name}</span>
                        <span className="ml-2 text-xs text-muted">
                          группа «{p.group_name}» · минимум {p.min_available}
                        </span>
                      </div>
                      <button onClick={() => deletePolicy(p.id)} className="text-xs text-red-400 hover:underline">
                        удалить
                      </button>
                    </div>
                    {gaps.length > 0 ? (
                      <ul className="mt-2 space-y-1 text-xs">
                        {gaps.map((g, i) => (
                          <li key={i} className="rounded-md bg-amber-500/15 px-2 py-1 text-amber-400">
                            ⚠ {new Date(g.from).toLocaleString("ru-RU")} — {new Date(g.to).toLocaleString("ru-RU")} (доступно: {g.min_available})
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <div className="mt-2 text-xs text-emerald-400">пробелов не найдено</div>
                    )}
                  </div>
                );
              })
            ) : (
              <p className="text-sm text-muted">Политик покрытия пока нет — создайте первую.</p>
            )}
          </div>
        </Card>

        <Card>
          <h3 className="mb-3 text-sm font-semibold">Новая политика</h3>
          <div className="space-y-3">
            <div>
              <label className="mb-1 block text-xs text-muted">Название</label>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="например, дежурство по буровой"
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted">Группа</label>
              <select
                value={groupID ?? ""}
                onChange={(e) => setGroupID(e.target.value ? Number(e.target.value) : null)}
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
              >
                <option value="">не выбрана</option>
                {groups?.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted">Минимум одновременно доступных</label>
              <input
                type="number"
                min={1}
                value={minAvailable}
                onChange={(e) => setMinAvailable(Math.max(1, Number(e.target.value) || 1))}
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
              />
            </div>
            {error && <div className="rounded-md bg-red-500/15 px-3 py-2 text-xs text-red-400">{error}</div>}
            <button
              disabled={saving}
              onClick={createPolicy}
              className="w-full rounded-md bg-accent px-3 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
            >
              {saving ? "Сохранение…" : "Создать политику"}
            </button>
            <p className="text-xs text-muted">
              Пробелы пересчитываются по требованию (не хранятся) — та же логика, что использует dry-run на
              карточке доступности сотрудника при создании интервала.
            </p>
          </div>
        </Card>
      </div>
    </div>
  );
}
