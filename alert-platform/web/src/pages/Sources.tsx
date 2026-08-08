import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, SourceCatalog } from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

export default function Sources() {
  const queryClient = useQueryClient();
  const { data } = useQuery<SourceCatalog>({ queryKey: ["sources"], queryFn: () => api.get("/sources") });
  const [instance, setInstance] = useState("");
  const [system, setSystem] = useState("zabbix");
  const [site, setSite] = useState("");
  const add = useMutation({
    mutationFn: () => api.post("/sources", { instance, system, site }),
    onSuccess: () => {
      setInstance("");
      queryClient.invalidateQueries({ queryKey: ["sources"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.delete(`/sources/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["sources"] }),
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    add.mutate();
  }

  return (
    <div>
      <PageHeader title="Источники событий" subtitle="Регистрация нового инстанса без изменения кода и редеплоя" />
      <Card>
        <form onSubmit={submit} className="grid gap-3 md:grid-cols-4">
          <input required value={instance} onChange={(e) => setInstance(e.target.value)} placeholder="zbx-newsite-01" className="rounded-md border border-border bg-background px-3 py-2 text-sm" />
          <select value={system} onChange={(e) => setSystem(e.target.value)} className="rounded-md border border-border bg-background px-3 py-2 text-sm">
            {(data?.systems ?? ["zabbix", "solarwinds"]).map((value) => <option key={value}>{value}</option>)}
          </select>
          <input required list="source-sites" value={site} onChange={(e) => setSite(e.target.value)} placeholder="gpn-newsite" className="rounded-md border border-border bg-background px-3 py-2 text-sm" />
          <datalist id="source-sites">{data?.sites.map((value) => <option key={value} value={value} />)}</datalist>
          <button disabled={add.isPending} className="rounded-md bg-accent px-4 py-2 text-sm text-white disabled:opacity-50">Добавить</button>
        </form>
        {add.isError && <p className="mt-2 text-sm text-red-400">Не удалось добавить источник: {add.error.message}</p>}
      </Card>
      <div className="mt-4 space-y-2">
        {data?.items.length === 0 && <EmptyState>Источников пока нет</EmptyState>}
        {data?.items.map((source) => (
          <Card key={source.id} className="flex items-center justify-between">
            <div><div className="text-sm font-medium">{source.instance}</div><div className="text-xs text-muted">{source.system} · {source.site}</div></div>
            <button onClick={() => remove.mutate(source.id)} className="rounded-md bg-red-500/15 px-3 py-1.5 text-xs text-red-400">Удалить</button>
          </Card>
        ))}
      </div>
    </div>
  );
}
