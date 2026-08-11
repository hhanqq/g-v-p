import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, PlatformUserDetail, PlatformUserListItem, RBACMeta } from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

export default function Users() {
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const { data: users, isLoading } = useQuery<PlatformUserListItem[]>({
    queryKey: ["platform-users"],
    queryFn: () => api.get("/users"),
  });
  const { data: meta } = useQuery<RBACMeta>({
    queryKey: ["users-meta"],
    queryFn: () => api.get("/users/meta"),
    staleTime: 60_000,
  });

  return (
    <div>
      <PageHeader title="Пользователи и права" />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_420px]">
        <Card className="!p-0">
          {isLoading && <div className="p-5 text-sm text-muted">Загрузка…</div>}
          {!isLoading && users?.length === 0 && (
            <div className="p-5">
              <EmptyState>Пока никто не входил в ADP — список появится после первого LDAP-входа.</EmptyState>
            </div>
          )}
          {!!users?.length && (
            <table className="w-full text-sm">
              <thead className="bg-bg text-left text-xs text-muted">
                <tr>
                  <th className="px-4 py-2">Пользователь</th>
                  <th className="px-4 py-2">Роль</th>
                  <th className="px-4 py-2">Статус</th>
                  <th className="px-4 py-2">Исключения</th>
                  <th className="px-4 py-2">Область</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr
                    key={u.id}
                    onClick={() => setSelectedId(u.id)}
                    className={`cursor-pointer border-t border-border hover:bg-fg/5 ${selectedId === u.id ? "bg-accent/10" : ""}`}
                  >
                    <td className="px-4 py-2 font-medium">{u.username}</td>
                    <td className="px-4 py-2 text-muted">{u.role_label}</td>
                    <td className="px-4 py-2">
                      {u.active ? (
                        <span className="rounded bg-emerald-500/15 px-2 py-0.5 text-xs text-emerald-400">активен</span>
                      ) : (
                        <span className="rounded bg-red-500/15 px-2 py-0.5 text-xs text-red-400">деактивирован</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-muted">{u.override_count || "—"}</td>
                    <td className="px-4 py-2 text-muted">{u.scope_count || "весь доступ роли"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
        {selectedId && meta && <UserDetailPanel userId={selectedId} meta={meta} onClose={() => setSelectedId(null)} />}
      </div>
    </div>
  );
}

function UserDetailPanel({ userId, meta, onClose }: { userId: number; meta: RBACMeta; onClose: () => void }) {
  const queryClient = useQueryClient();
  const { data: detail } = useQuery<PlatformUserDetail>({
    queryKey: ["platform-user", userId],
    queryFn: () => api.get(`/users/${userId}`),
  });
  const [pendingOverrides, setPendingOverrides] = useState<Record<string, "grant" | "deny"> | null>(null);
  const [pendingScopes, setPendingScopes] = useState<{ type: string; value: string }[] | null>(null);
  const [scopeType, setScopeType] = useState(meta.scope_types[0]?.value ?? "site");
  const [scopeValue, setScopeValue] = useState("");

  const overrides = pendingOverrides ?? Object.fromEntries((detail?.overrides ?? []).map((o) => [o.permission, o.effect]));
  const scopes = pendingScopes ?? detail?.scopes ?? [];

  function refresh() {
    queryClient.invalidateQueries({ queryKey: ["platform-user", userId] });
    queryClient.invalidateQueries({ queryKey: ["platform-users"] });
  }

  const roleMutation = useMutation({
    mutationFn: (role: string) => api.put(`/users/${userId}`, { role }),
    onSuccess: refresh,
  });
  const activeMutation = useMutation({
    mutationFn: (active: boolean) => api.put(`/users/${userId}`, { active }),
    onSuccess: refresh,
  });
  const permissionsMutation = useMutation({
    mutationFn: (list: { permission: string; effect: string }[]) => api.put(`/users/${userId}/permissions`, { overrides: list }),
    onSuccess: () => {
      setPendingOverrides(null);
      refresh();
    },
  });
  const scopesMutation = useMutation({
    mutationFn: (list: { type: string; value: string }[]) => api.put(`/users/${userId}/scopes`, { scopes: list }),
    onSuccess: () => {
      setPendingScopes(null);
      refresh();
    },
  });

  if (!detail) return <Card>Загрузка…</Card>;

  const roleDefaults = new Set(meta.role_permissions[detail.role] ?? []);

  function toggle(permission: string) {
    const currentlyGranted = overrides[permission] ? overrides[permission] === "grant" : roleDefaults.has(permission);
    const next = { ...overrides };
    const roleDefault = roleDefaults.has(permission);
    const wantGrant = !currentlyGranted;
    if (wantGrant === roleDefault) {
      delete next[permission];
    } else {
      next[permission] = wantGrant ? "grant" : "deny";
    }
    setPendingOverrides(next);
  }

  function addScope() {
    if (!scopeValue.trim()) return;
    setPendingScopes([...scopes, { type: scopeType, value: scopeValue.trim() }]);
    setScopeValue("");
  }
  function removeScope(i: number) {
    setPendingScopes(scopes.filter((_, idx) => idx !== i));
  }

  return (
    <Card>
      <div className="mb-4 flex items-center justify-between">
        <div>
          <div className="text-sm font-semibold">{detail.username}</div>
          <div className="text-xs text-muted">Карточка доступа</div>
        </div>
        <button onClick={onClose} className="rounded p-1 text-muted hover:bg-fg/10" aria-label="Закрыть">×</button>
      </div>

      <label className="mb-2 block text-xs text-muted">
        Роль
        <select
          value={detail.role}
          onChange={(e) => roleMutation.mutate(e.target.value)}
          className="mt-1 block w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
        >
          {meta.roles.map((r) => (
            <option key={r.value} value={r.value}>{r.label}</option>
          ))}
        </select>
      </label>

      <label className="mb-4 flex items-center gap-2 text-sm">
        <input type="checkbox" checked={detail.active} onChange={(e) => activeMutation.mutate(e.target.checked)} />
        Учётная запись активна
      </label>

      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">Дополнительные права</div>
      <p className="mb-2 text-[11px] text-muted">
        Отмечено — доступ есть. Серым — унаследовано от роли «{detail.role_label}»; изменение галочки создаёт
        индивидуальное исключение (grant/deny) поверх роли.
      </p>
      <div className="mb-3 max-h-64 space-y-1 overflow-y-auto rounded-md border border-border p-2">
        {meta.permissions.map((p) => {
          const roleDefault = roleDefaults.has(p.value);
          const override = overrides[p.value];
          const checked = override ? override === "grant" : roleDefault;
          return (
            <label key={p.value} className="flex items-center gap-2 rounded px-1 py-0.5 text-xs hover:bg-fg/5">
              <input type="checkbox" checked={checked} onChange={() => toggle(p.value)} />
              <span className={override ? "font-medium text-accent" : ""}>{p.label}</span>
              {override && <span className="text-[10px] text-muted">({override === "grant" ? "выдано" : "запрещено"})</span>}
            </label>
          );
        })}
      </div>
      {pendingOverrides && (
        <button
          onClick={() => permissionsMutation.mutate(Object.entries(pendingOverrides).map(([permission, effect]) => ({ permission, effect })))}
          className="mb-4 w-full rounded-md bg-accent py-1.5 text-xs font-medium text-white"
        >
          Сохранить права
        </button>
      )}

      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">Область данных (scope)</div>
      <p className="mb-2 text-[11px] text-muted">Без ограничений — доступ по всей компании. Добавление хотя бы одной строки сужает видимость.</p>
      <div className="mb-2 space-y-1">
        {scopes.length === 0 && <div className="text-xs text-muted">Ограничений нет — вся компания.</div>}
        {scopes.map((s, i) => (
          <div key={i} className="flex items-center justify-between rounded bg-bg px-2 py-1 text-xs">
            <span>{meta.scope_types.find((t) => t.value === s.type)?.label ?? s.type}: <strong>{s.value}</strong></span>
            <button onClick={() => removeScope(i)} className="text-muted hover:text-red-400">×</button>
          </div>
        ))}
      </div>
      <div className="mb-3 flex gap-1">
        <select value={scopeType} onChange={(e) => setScopeType(e.target.value)} className="rounded-md border border-border bg-bg px-2 py-1 text-xs">
          {meta.scope_types.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
        <input
          value={scopeValue}
          onChange={(e) => setScopeValue(e.target.value)}
          placeholder="значение"
          className="flex-1 rounded-md border border-border bg-bg px-2 py-1 text-xs"
        />
        <button onClick={addScope} className="rounded-md border border-border px-2 py-1 text-xs text-muted hover:bg-fg/5">+</button>
      </div>
      {pendingScopes && (
        <button
          onClick={() => scopesMutation.mutate(pendingScopes)}
          className="mb-4 w-full rounded-md bg-accent py-1.5 text-xs font-medium text-white"
        >
          Сохранить область
        </button>
      )}

      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">Эффективные права</div>
      <div className="flex flex-wrap gap-1">
        {detail.effective_permissions.map((p) => (
          <span key={p} className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-400">
            {meta.permissions.find((m) => m.value === p)?.label ?? p}
          </span>
        ))}
      </div>
    </Card>
  );
}
