import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, EmployeeListItem, EquipmentListItem, GroupDetail, GroupListItem } from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

// Раздел «Группы» — кто отвечает за какое оборудование. Зона
// ответственности (group_equipment_scope) сама по себе ни на что не
// уведомляет: она используется узлом «Условие» (оборудование/тип) и
// узлом «Уведомить»/«Проверка подписки» (группа) в редакторе сценариев
// (`ScenarioEditor.tsx`) — там строится собственно каскад.

export default function Groups() {
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  const { data: groups } = useQuery<GroupListItem[]>({ queryKey: ["groups"], queryFn: () => api.get("/groups") });
  const { data: group } = useQuery<GroupDetail>({
    queryKey: ["group", selectedId],
    queryFn: () => api.get(`/groups/${selectedId}`),
    enabled: selectedId !== null,
  });
  const { data: employees } = useQuery<EmployeeListItem[]>({ queryKey: ["employees"], queryFn: () => api.get("/employees") });
  const { data: equipment } = useQuery<EquipmentListItem[]>({ queryKey: ["equipment"], queryFn: () => api.get("/equipment") });

  const equipmentTypes = useMemo(
    () => Array.from(new Set(equipment?.map((e) => e.equipment_type).filter((v): v is string => !!v) ?? [])),
    [equipment],
  );
  const sites = useMemo(() => Array.from(new Set(equipment?.map((e) => e.site) ?? [])), [equipment]);

  const createGroup = useMutation({
    mutationFn: () => api.post<{ id: number }>("/groups", { name, description: description || undefined }),
    onSuccess: (created) => {
      setName("");
      setDescription("");
      queryClient.invalidateQueries({ queryKey: ["groups"] });
      setSelectedId(created.id);
    },
  });
  const deleteGroup = useMutation({
    mutationFn: (id: number) => api.delete(`/groups/${id}`),
    onSuccess: () => {
      setSelectedId(null);
      queryClient.invalidateQueries({ queryKey: ["groups"] });
    },
  });
  const addMember = useMutation({
    mutationFn: (subscriberId: number) => api.post(`/groups/${selectedId}/members`, { subscriber_id: subscriberId }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["group", selectedId] }),
  });
  const removeMember = useMutation({
    mutationFn: (subscriberId: number) => api.delete(`/groups/${selectedId}/members/${subscriberId}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["group", selectedId] }),
  });
  const addScope = useMutation({
    mutationFn: (payload: { object_id?: string; equipment_type?: string; site?: string }) =>
      api.post(`/groups/${selectedId}/equipment`, payload),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["group", selectedId] }),
  });
  const removeScope = useMutation({
    mutationFn: (scopeId: number) => api.delete(`/groups/${selectedId}/equipment/${scopeId}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["group", selectedId] }),
  });

  const [scopeKind, setScopeKind] = useState<"object" | "type" | "site">("type");
  const [scopeValue, setScopeValue] = useState("");

  function submitCreate(event: FormEvent) {
    event.preventDefault();
    if (!name.trim()) return;
    createGroup.mutate();
  }

  function submitScope(event: FormEvent) {
    event.preventDefault();
    if (!scopeValue) return;
    if (scopeKind === "object") addScope.mutate({ object_id: scopeValue });
    if (scopeKind === "type") addScope.mutate({ equipment_type: scopeValue });
    if (scopeKind === "site") addScope.mutate({ site: scopeValue });
    setScopeValue("");
  }

  const memberIds = new Set(group?.members.map((m) => m.subscriber_id));
  const availableEmployees = employees?.filter((e) => !memberIds.has(e.id)) ?? [];

  return (
    <div>
      <PageHeader title="Группы" />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="lg:col-span-1">
          <Card className="mb-4">
            <h3 className="mb-3 text-sm font-semibold">Новая группа</h3>
            <form onSubmit={submitCreate} className="space-y-2">
              <input
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="например, Механики Ноябрьск"
                className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm"
              />
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="описание (необязательно)"
                className="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm"
              />
              <button disabled={createGroup.isPending} className="w-full rounded-md bg-accent px-4 py-2 text-sm text-white disabled:opacity-50">
                Создать
              </button>
            </form>
          </Card>

          {groups?.length === 0 && <EmptyState>Групп пока нет</EmptyState>}
          <div className="space-y-2">
            {groups?.map((g) => (
              <Card
                key={g.id}
                className={`cursor-pointer hover:border-accent ${selectedId === g.id ? "border-accent" : ""}`}
                onClick={() => setSelectedId(g.id)}
              >
                <div className="flex items-center justify-between">
                  <div>
                    <div className="text-sm font-medium">{g.name}</div>
                    <div className="text-xs text-muted">{g.description ?? "без описания"}</div>
                  </div>
                  <div className="text-right text-xs text-muted">
                    <div>{g.member_count} участников</div>
                    <div>{g.equipment_scope_count} зон</div>
                  </div>
                </div>
              </Card>
            ))}
          </div>
        </div>

        <div className="lg:col-span-2">
          {!selectedId && <EmptyState>Выберите группу слева, чтобы настроить участников и оборудование</EmptyState>}
          {selectedId && group && (
            <div className="space-y-4">
              <Card>
                <div className="flex items-start justify-between">
                  <div>
                    <h3 className="text-sm font-semibold">{group.name}</h3>
                    <p className="text-xs text-muted">{group.description ?? "без описания"}</p>
                  </div>
                  <button
                    onClick={() => deleteGroup.mutate(group.id)}
                    className="rounded-md bg-red-500/15 px-3 py-1.5 text-xs text-red-400"
                  >
                    Удалить группу
                  </button>
                </div>
              </Card>

              <Card>
                <h3 className="mb-3 text-sm font-semibold">Участники</h3>
                {group.members.length === 0 && <p className="mb-3 text-sm text-muted">Участников нет.</p>}
                <div className="mb-3 space-y-1.5">
                  {group.members.map((m) => (
                    <div key={m.subscriber_id} className="flex items-center justify-between rounded-lg bg-bg px-3 py-2 text-sm">
                      <span>{m.full_name ?? m.trueconf_username}</span>
                      <button onClick={() => removeMember.mutate(m.subscriber_id)} className="text-xs text-red-400 hover:underline">
                        убрать
                      </button>
                    </div>
                  ))}
                </div>
                <select
                  value=""
                  onChange={(e) => e.target.value && addMember.mutate(Number(e.target.value))}
                  className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                >
                  <option value="">+ добавить сотрудника</option>
                  {availableEmployees.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.full_name ?? e.trueconf_username}
                    </option>
                  ))}
                </select>
              </Card>

              <Card>
                <h3 className="mb-3 text-sm font-semibold">Зона ответственности по оборудованию</h3>
                <p className="mb-3 text-xs text-muted">
                  Незаполненная ось не сужает — та же логика, что у подписок и условий сценариев.
                </p>
                {group.equipment_scope.length === 0 && <p className="mb-3 text-sm text-muted">Зона не задана.</p>}
                <div className="mb-3 space-y-1.5">
                  {group.equipment_scope.map((s) => (
                    <div key={s.id} className="flex items-center justify-between rounded-lg bg-bg px-3 py-2 text-sm">
                      <span>
                        {s.object_id ? `оборудование: ${s.object_name ?? s.object_id}` : s.equipment_type ? `тип: ${s.equipment_type}` : `площадка: ${s.site}`}
                      </span>
                      <button onClick={() => removeScope.mutate(s.id)} className="text-xs text-red-400 hover:underline">
                        убрать
                      </button>
                    </div>
                  ))}
                </div>
                <form onSubmit={submitScope} className="flex gap-2">
                  <select
                    value={scopeKind}
                    onChange={(e) => {
                      setScopeKind(e.target.value as typeof scopeKind);
                      setScopeValue("");
                    }}
                    className="rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                  >
                    <option value="type">тип оборудования</option>
                    <option value="site">площадка</option>
                    <option value="object">конкретное оборудование</option>
                  </select>
                  {scopeKind === "object" ? (
                    <select
                      required
                      value={scopeValue}
                      onChange={(e) => setScopeValue(e.target.value)}
                      className="flex-1 rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                    >
                      <option value="">выберите объект</option>
                      {equipment?.map((eq) => (
                        <option key={eq.id} value={eq.id}>
                          {eq.name} ({eq.site})
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      required
                      list={scopeKind === "type" ? "group-equipment-types" : "group-sites"}
                      value={scopeValue}
                      onChange={(e) => setScopeValue(e.target.value)}
                      placeholder={scopeKind === "type" ? "например, plc_controller" : "например, gpn-noyabrsk"}
                      className="flex-1 rounded-md border border-border bg-bg px-2 py-1.5 text-sm"
                    />
                  )}
                  <button disabled={addScope.isPending} className="rounded-md bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-50">
                    Добавить
                  </button>
                </form>
                <datalist id="group-equipment-types">
                  {equipmentTypes.map((v) => <option key={v} value={v} />)}
                </datalist>
                <datalist id="group-sites">
                  {sites.map((v) => <option key={v} value={v} />)}
                </datalist>
              </Card>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
