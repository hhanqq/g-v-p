import { useMutation, useQuery } from "@tanstack/react-query";
import { api, DemoScenario } from "../api";
import { Card, PageHeader } from "../components/ui";

interface TriggerResult { scenario_id: string; events_scheduled: number; demo_duration_s: number }

export default function Demo() {
  const { data } = useQuery<Record<string, DemoScenario>>({ queryKey: ["demo-scenarios"], queryFn: () => api.get("/demo/scenarios") });
  const trigger = useMutation<TriggerResult, Error, string>({ mutationFn: (id) => api.post(`/trigger/${id}`) });
  return <div>
    <PageHeader title="Демонстрационные сценарии" subtitle="Синтетические каскады проходят штатный ingress → pipeline → delivery тракт" />
    {trigger.data && <Card className="mb-4 border-accent text-sm">Запущен {trigger.data.scenario_id}: {trigger.data.events_scheduled} событий в течение ≈ {trigger.data.demo_duration_s} с.</Card>}
    {trigger.isError && <Card className="mb-4 border-red-500 text-sm text-red-400">Ошибка запуска: {trigger.error.message}</Card>}
    <div className="space-y-2">{Object.entries(data ?? {}).map(([id, scenario]) => <Card key={id} className="flex items-center justify-between gap-4"><div><div className="text-sm font-medium">{scenario.name}</div><div className="text-xs text-muted">{scenario.description}</div></div><button disabled={trigger.isPending} onClick={() => trigger.mutate(id)} className="shrink-0 rounded-md bg-accent px-4 py-2 text-sm text-white disabled:opacity-50">Запустить</button></Card>)}</div>
  </div>;
}
