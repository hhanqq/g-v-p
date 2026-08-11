import { useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";
import {
  api,
  AvailabilityCalendarResponse,
  DryRunResponse,
  EmployeeListItem,
} from "../api";
import { Card, PageHeader } from "../components/ui";

const KIND_OPTIONS = [
  { value: "vacation", label: "отпуск" },
  { value: "sick_leave", label: "больничный" },
  { value: "shift", label: "смена" },
  { value: "on_call", label: "на связи" },
  { value: "unavailable", label: "недоступен" },
  { value: "available", label: "на месте" },
  { value: "override_available", label: "исключение: доступен" },
  { value: "override_unavailable", label: "исключение: недоступен" },
  { value: "delegation", label: "делегирование" },
];

const KIND_LABEL: Record<string, string> = Object.fromEntries(KIND_OPTIONS.map((k) => [k.value, k.label]));

const KIND_DOT: Record<string, string> = {
  vacation: "bg-amber-400",
  sick_leave: "bg-rose-400",
  shift: "bg-sky-400",
  on_call: "bg-sky-400",
  unavailable: "bg-red-400",
  available: "bg-emerald-400",
  override_available: "bg-emerald-400",
  override_unavailable: "bg-red-400",
  delegation: "bg-purple-400",
};

const WEEKDAY_LABELS = ["Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"];

function toDateInputValue(date: Date): string {
  return date.toISOString().slice(0, 10);
}

function monthGrid(year: number, month: number): Date[] {
  const first = new Date(Date.UTC(year, month, 1));
  // ISO: понедельник = 0 ... воскресенье = 6, чтобы сетка начиналась с Пн
  const leading = (first.getUTCDay() + 6) % 7;
  const start = new Date(first);
  start.setUTCDate(start.getUTCDate() - leading);
  return Array.from({ length: 42 }, (_, i) => {
    const d = new Date(start);
    d.setUTCDate(d.getUTCDate() + i);
    return d;
  });
}

export default function AvailabilityCalendar() {
  const queryClient = useQueryClient();
  const today = useMemo(() => new Date(), []);
  const [employeeID, setEmployeeID] = useState<number | null>(null);
  const [cursor, setCursor] = useState(() => new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1)));

  const [kind, setKind] = useState("vacation");
  const [validFrom, setValidFrom] = useState(toDateInputValue(today));
  const [validUntil, setValidUntil] = useState(toDateInputValue(today));
  const [note, setNote] = useState("");
  const [delegateTo, setDelegateTo] = useState<number | null>(null);
  const [recurring, setRecurring] = useState(false);
  const [recurWeekdays, setRecurWeekdays] = useState<number[]>([1, 2, 3, 4, 5]);
  const [recurUntil, setRecurUntil] = useState(toDateInputValue(new Date(today.getTime() + 30 * 86400000)));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const { data: employees } = useQuery<EmployeeListItem[]>({
    queryKey: ["employees"],
    queryFn: () => api.get<EmployeeListItem[]>("/employees"),
  });

  const rangeFrom = useMemo(() => monthGrid(cursor.getUTCFullYear(), cursor.getUTCMonth())[0], [cursor]);
  const rangeTo = useMemo(() => {
    const grid = monthGrid(cursor.getUTCFullYear(), cursor.getUTCMonth());
    const last = new Date(grid[grid.length - 1]);
    last.setUTCDate(last.getUTCDate() + 1);
    return last;
  }, [cursor]);

  const { data: calendar } = useQuery<AvailabilityCalendarResponse>({
    queryKey: ["availability-calendar", employeeID, rangeFrom.toISOString(), rangeTo.toISOString()],
    queryFn: () =>
      api.get<AvailabilityCalendarResponse>(
        `/employees/${employeeID}/availability/calendar?from=${rangeFrom.toISOString().slice(0, 19)}&to=${rangeTo.toISOString().slice(0, 19)}`,
      ),
    enabled: employeeID !== null,
  });

  const { data: dryRun } = useQuery<DryRunResponse>({
    queryKey: ["availability-dry-run", employeeID, kind, validFrom, validUntil],
    queryFn: () =>
      api.post<DryRunResponse>(`/employees/${employeeID}/availability/dry-run`, {
        kind,
        valid_from: `${validFrom}T00:00:00`,
        valid_until: validUntil ? `${validUntil}T23:59:59` : null,
      }),
    enabled: employeeID !== null && !!validFrom,
  });

  const dayByDate = useMemo(() => {
    const map = new Map<string, { available: boolean; kind: string }>();
    for (const d of calendar?.days ?? []) map.set(d.date, d);
    return map;
  }, [calendar]);

  async function createInterval() {
    if (!employeeID) return;
    setSaving(true);
    setError(null);
    try {
      const payload: Record<string, unknown> = {
        kind,
        valid_from: `${validFrom}T00:00:00`,
        valid_until: validUntil ? `${validUntil}T23:59:59` : null,
        note: note || null,
        delegate_to_subscriber_id: kind === "delegation" ? delegateTo : null,
      };
      if (recurring) {
        payload.recurrence = { weekdays: recurWeekdays, until: `${recurUntil}T00:00:00` };
      }
      await api.post(`/employees/${employeeID}/availability/intervals`, payload);
      await queryClient.invalidateQueries({ queryKey: ["availability-calendar"] });
      setNote("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Не удалось создать интервал");
    } finally {
      setSaving(false);
    }
  }

  async function deleteInterval(intervalID: number) {
    if (!employeeID) return;
    await api.delete(`/employees/${employeeID}/availability/intervals/${intervalID}`);
    await queryClient.invalidateQueries({ queryKey: ["availability-calendar"] });
  }

  const grid = monthGrid(cursor.getUTCFullYear(), cursor.getUTCMonth());
  const monthLabel = cursor.toLocaleDateString("ru-RU", { month: "long", year: "numeric" });
  const todayStr = toDateInputValue(today);

  return (
    <div>
      <PageHeader title="Доступность" />

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <select
          value={employeeID ?? ""}
          onChange={(e) => setEmployeeID(e.target.value ? Number(e.target.value) : null)}
          className="rounded-md border border-border bg-card px-3 py-2 text-sm"
        >
          <option value="">выберите сотрудника</option>
          {employees?.map((e) => (
            <option key={e.id} value={e.id}>
              {e.full_name ?? e.trueconf_username}
            </option>
          ))}
        </select>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setCursor(new Date(Date.UTC(cursor.getUTCFullYear(), cursor.getUTCMonth() - 1, 1)))}
            className="rounded-md bg-bg px-2 py-1 text-sm hover:bg-fg/10"
          >
            ←
          </button>
          <span className="w-36 text-center text-sm capitalize">{monthLabel}</span>
          <button
            onClick={() => setCursor(new Date(Date.UTC(cursor.getUTCFullYear(), cursor.getUTCMonth() + 1, 1)))}
            className="rounded-md bg-bg px-2 py-1 text-sm hover:bg-fg/10"
          >
            →
          </button>
        </div>
      </div>

      {!employeeID ? (
        <p className="text-sm text-muted">Выберите сотрудника, чтобы увидеть и редактировать календарь доступности.</p>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <Card className="lg:col-span-2">
            <div className="grid grid-cols-7 gap-1 text-center text-[11px] text-muted">
              {WEEKDAY_LABELS.map((w) => (
                <div key={w} className="py-1">
                  {w}
                </div>
              ))}
            </div>
            <div className="grid grid-cols-7 gap-1">
              {grid.map((day) => {
                const dateStr = day.toISOString().slice(0, 10);
                const inMonth = day.getUTCMonth() === cursor.getUTCMonth();
                const info = dayByDate.get(dateStr);
                const isToday = dateStr === todayStr;
                return (
                  <div
                    key={dateStr}
                    className={`min-h-[64px] rounded-md border p-1 text-[11px] ${
                      inMonth ? "border-border bg-bg" : "border-transparent opacity-30"
                    } ${isToday ? "ring-1 ring-accent" : ""}`}
                  >
                    <div className="text-muted">{day.getUTCDate()}</div>
                    {info && info.kind && (
                      <div className="mt-1 flex items-center gap-1">
                        <span className={`h-1.5 w-1.5 rounded-full ${KIND_DOT[info.kind] ?? "bg-muted"}`} />
                        <span className="truncate">{KIND_LABEL[info.kind] ?? info.kind}</span>
                      </div>
                    )}
                    {info && !info.kind && <div className="mt-1 text-muted">на месте</div>}
                  </div>
                );
              })}
            </div>

            <h4 className="mb-2 mt-5 text-xs font-semibold text-fg">Интервалы в этом месяце</h4>
            <div className="space-y-1.5">
              {calendar?.intervals.length ? (
                calendar.intervals.map((iv) => {
                  const future = new Date(iv.valid_from) > today;
                  return (
                    <div key={iv.id} className="flex items-center justify-between rounded-md bg-bg px-3 py-1.5 text-xs">
                      <span>
                        <span className={`mr-1.5 inline-block h-1.5 w-1.5 rounded-full ${KIND_DOT[iv.kind] ?? "bg-muted"}`} />
                        {KIND_LABEL[iv.kind] ?? iv.kind} · {new Date(iv.valid_from).toLocaleDateString("ru-RU")}
                        {iv.valid_until ? ` — ${new Date(iv.valid_until).toLocaleDateString("ru-RU")}` : " (бессрочно)"}
                        {iv.note ? ` · ${iv.note}` : ""}
                      </span>
                      {future && (
                        <button onClick={() => deleteInterval(iv.id)} className="text-red-400 hover:underline">
                          удалить
                        </button>
                      )}
                    </div>
                  );
                })
              ) : (
                <p className="text-xs text-muted">Интервалов, пересекающих этот месяц, нет.</p>
              )}
            </div>
          </Card>

          <Card>
            <h3 className="mb-3 text-sm font-semibold">Новый интервал</h3>
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs text-muted">Тип</label>
                <select value={kind} onChange={(e) => setKind(e.target.value)} className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm">
                  {KIND_OPTIONS.map((k) => (
                    <option key={k.value} value={k.value}>
                      {k.label}
                    </option>
                  ))}
                </select>
              </div>
              {kind === "delegation" && (
                <div>
                  <label className="mb-1 block text-xs text-muted">Делегат</label>
                  <select
                    value={delegateTo ?? ""}
                    onChange={(e) => setDelegateTo(e.target.value ? Number(e.target.value) : null)}
                    className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                  >
                    <option value="">не выбран</option>
                    {employees?.filter((e) => e.id !== employeeID).map((e) => (
                      <option key={e.id} value={e.id}>
                        {e.full_name ?? e.trueconf_username}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label className="mb-1 block text-xs text-muted">С</label>
                  <input type="date" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm" />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted">По (включительно)</label>
                  <input type="date" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm" />
                </div>
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">Заметка (необязательно)</label>
                <input value={note} onChange={(e) => setNote(e.target.value)} className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm" />
              </div>

              <label className="flex items-center gap-1.5 text-xs">
                <input type="checkbox" checked={recurring} onChange={(e) => setRecurring(e.target.checked)} />
                Повторять по дням недели
              </label>
              {recurring && (
                <div className="space-y-2 rounded-md bg-bg p-2">
                  <div className="flex flex-wrap gap-2 text-xs">
                    {WEEKDAY_LABELS.map((label, idx) => {
                      const iso = idx + 1 === 7 ? 0 : idx + 1; // Пн=1..Вс=0 (time.Weekday)
                      const checked = recurWeekdays.includes(iso);
                      return (
                        <label key={label} className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() =>
                              setRecurWeekdays((prev) => (checked ? prev.filter((w) => w !== iso) : [...prev, iso]))
                            }
                          />
                          {label}
                        </label>
                      );
                    })}
                  </div>
                  <div>
                    <label className="mb-1 block text-xs text-muted">Повторять до</label>
                    <input type="date" value={recurUntil} onChange={(e) => setRecurUntil(e.target.value)} className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-sm" />
                  </div>
                </div>
              )}

              {dryRun && dryRun.warnings.length > 0 && (
                <div className="rounded-md bg-amber-500/15 px-3 py-2 text-xs text-amber-400">
                  {dryRun.warnings.map((w, i) => (
                    <div key={i} className="flex items-center gap-1.5">
                      <TriangleAlert size={13} strokeWidth={1.75} className="shrink-0" />
                      {w}
                    </div>
                  ))}
                </div>
              )}
              {error && <div className="rounded-md bg-red-500/15 px-3 py-2 text-xs text-red-400">{error}</div>}

              <button
                disabled={saving || (kind === "delegation" && !delegateTo)}
                onClick={createInterval}
                className="w-full rounded-md bg-accent px-3 py-2 text-sm text-white hover:opacity-90 disabled:opacity-50"
              >
                {saving ? "Сохранение…" : "Создать интервал"}
              </button>
            </div>
          </Card>
        </div>
      )}
    </div>
  );
}
