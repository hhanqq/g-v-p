import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { Fragment, useState } from "react";
import {
  api,
  ChangeHistoryCondition,
  ChangeHistoryField,
  ChangeHistorySearchResult,
} from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

const OP_LABEL: Record<string, string> = {
  eq: "равно",
  contains: "содержит",
  gte: "не раньше",
  lte: "не позже",
};

function emptyCondition(fields: Record<string, ChangeHistoryField>): ChangeHistoryCondition {
  const firstField = Object.keys(fields)[0] ?? "actor";
  const firstOp = fields[firstField]?.ops[0] ?? "eq";
  return { field: firstField, op: firstOp, value: "" };
}

export default function ChangeHistory() {
  const { data: fields } = useQuery<Record<string, ChangeHistoryField>>({
    queryKey: ["change-history-fields"],
    queryFn: () => api.get("/change-history/fields"),
  });

  const [match, setMatch] = useState<"all" | "any">("all");
  const [conditions, setConditions] = useState<ChangeHistoryCondition[]>([]);
  const [results, setResults] = useState<ChangeHistorySearchResult[] | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function addCondition() {
    if (!fields) return;
    setConditions([...conditions, emptyCondition(fields)]);
  }

  function updateCondition(index: number, patch: Partial<ChangeHistoryCondition>) {
    setConditions(conditions.map((c, i) => (i === index ? { ...c, ...patch } : c)));
  }

  function removeCondition(index: number) {
    setConditions(conditions.filter((_, i) => i !== index));
  }

  async function runSearch() {
    setSearching(true);
    setError(null);
    try {
      const data = await api.post<ChangeHistorySearchResult[]>("/change-history/search", {
        match, conditions, limit: 200,
      });
      setResults(data);
    } catch (e) {
      setError(String(e));
      setResults(null);
    } finally {
      setSearching(false);
    }
  }

  return (
    <div>
      <PageHeader
        title="История изменений"
        subtitle="Кто, что и когда менял в объектах, сотрудниках, группах и источниках — low-code поиск по условиям, без ручного SQL"
      />

      <Card className="mb-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-semibold">Условия поиска</h3>
          {conditions.length > 1 && (
            <div className="flex gap-1 text-xs">
              <button
                onClick={() => setMatch("all")}
                className={`rounded px-2 py-1 ${match === "all" ? "bg-accent text-white" : "bg-bg text-muted"}`}
              >
                выполнены все
              </button>
              <button
                onClick={() => setMatch("any")}
                className={`rounded px-2 py-1 ${match === "any" ? "bg-accent text-white" : "bg-bg text-muted"}`}
              >
                выполнено любое
              </button>
            </div>
          )}
        </div>

        {conditions.length === 0 && (
          <p className="mb-3 text-sm text-muted">
            Без условий поиск вернёт последние 200 записей истории по всем сущностям.
          </p>
        )}

        <div className="space-y-2">
          {conditions.map((cond, i) => {
            const spec = fields?.[cond.field];
            return (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <select
                  value={cond.field}
                  onChange={(e) => {
                    const nextField = e.target.value;
                    const nextOp = fields?.[nextField]?.ops[0] ?? "eq";
                    updateCondition(i, { field: nextField, op: nextOp });
                  }}
                  className="rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  {fields && Object.entries(fields).map(([key, f]) => (
                    <option key={key} value={key}>{f.label}</option>
                  ))}
                </select>
                <select
                  value={cond.op}
                  onChange={(e) => updateCondition(i, { op: e.target.value })}
                  className="rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  {spec?.ops.map((op) => (
                    <option key={op} value={op}>{OP_LABEL[op] ?? op}</option>
                  ))}
                </select>
                <input
                  type={spec?.kind === "datetime" ? "datetime-local" : "text"}
                  value={cond.value}
                  onChange={(e) => updateCondition(i, { value: e.target.value })}
                  placeholder={spec?.kind === "datetime" ? undefined : "значение"}
                  className="min-w-[160px] flex-1 rounded-md border border-border bg-bg px-3 py-1.5 text-sm"
                />
                <button
                  onClick={() => removeCondition(i)}
                  aria-label="Удалить условие"
                  className="rounded-md border border-border px-2 py-1.5 text-xs text-muted hover:bg-bg"
                >
                  <X size={13} strokeWidth={1.75} />
                </button>
              </div>
            );
          })}
        </div>

        <div className="mt-3 flex gap-2">
          <button
            onClick={addCondition}
            disabled={!fields}
            className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-bg disabled:opacity-50"
          >
            + Добавить условие
          </button>
          <button
            onClick={runSearch}
            disabled={searching}
            className="rounded-md bg-accent px-4 py-1.5 text-sm text-white disabled:opacity-50"
          >
            {searching ? "Ищу…" : "Искать"}
          </button>
        </div>
        {error && <p className="mt-2 text-xs text-red-400">{error}</p>}
      </Card>

      {results !== null && (
        <>
          {results.length === 0 ? (
            <EmptyState>По этим условиям ничего не найдено</EmptyState>
          ) : (
            <div className="overflow-hidden rounded-xl border border-border">
              <table className="w-full text-sm">
                <thead className="bg-card text-left text-xs text-muted">
                  <tr>
                    <th className="px-4 py-2">Время</th>
                    <th className="px-4 py-2">Пользователь</th>
                    <th className="px-4 py-2">Действие</th>
                    <th className="px-4 py-2">Сущность</th>
                    <th className="px-4 py-2">Результат</th>
                  </tr>
                </thead>
                <tbody>
                  {results.map((row) => (
                    <Fragment key={row.event_id}>
                      <tr
                        onClick={() => setExpanded(expanded === row.event_id ? null : row.event_id)}
                        className="cursor-pointer border-t border-border hover:bg-bg"
                      >
                        <td className="px-4 py-2 text-muted">{new Date(row.occurred_at).toLocaleString("ru-RU")}</td>
                        <td className="px-4 py-2">{row.actor} <span className="text-muted">({row.actor_role})</span></td>
                        <td className="px-4 py-2">{row.action}</td>
                        <td className="px-4 py-2 text-muted">{row.resource_type} · {row.resource_id}</td>
                        <td className="px-4 py-2">
                          <span className={row.result === "success" ? "text-emerald-400" : "text-amber-400"}>
                            {row.result}
                          </span>
                        </td>
                      </tr>
                      {expanded === row.event_id && (
                        <tr className="border-t border-border bg-bg/50">
                          <td colSpan={5} className="px-4 py-3">
                            <div className="grid grid-cols-1 gap-3 text-xs sm:grid-cols-2">
                              <div>
                                <div className="mb-1 font-semibold text-muted">До</div>
                                <pre className="whitespace-pre-wrap break-words rounded bg-bg p-2">{row.before_json || "—"}</pre>
                              </div>
                              <div>
                                <div className="mb-1 font-semibold text-muted">После</div>
                                <pre className="whitespace-pre-wrap break-words rounded bg-bg p-2">{row.after_json || "—"}</pre>
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </div>
  );
}
