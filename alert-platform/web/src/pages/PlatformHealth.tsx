import { useQuery } from "@tanstack/react-query";
import { api, PlatformHealth as PlatformHealthData } from "../api";
import { Card, PageHeader } from "../components/ui";
import Sparkline from "../components/Sparkline";

const STATUS_LABEL: Record<string, string> = { normal: "Normal", degraded: "Degraded", unknown: "Нет данных" };
const STATUS_CLASS: Record<string, string> = {
  normal: "bg-emerald-500/15 text-emerald-400",
  degraded: "bg-amber-500/15 text-amber-400",
  unknown: "bg-slate-500/15 text-muted",
};

export default function PlatformHealthPage() {
  const { data, isLoading } = useQuery<PlatformHealthData>({
    queryKey: ["platform-health"],
    queryFn: () => api.get("/platform-health"),
    refetchInterval: 30_000,
  });

  return (
    <div>
      <PageHeader title="Состояние системы" />
      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {data && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Card>
            <h2 className="mb-3 text-sm font-semibold">Компоненты</h2>
            <table className="w-full text-sm">
              <tbody>
                {data.components.map((c) => (
                  <tr key={c.name} className="border-t border-border first:border-t-0">
                    <td className="py-2 pr-3">{c.name}</td>
                    <td className="py-2">
                      <span className={`rounded px-2 py-0.5 text-xs font-medium ${STATUS_CLASS[c.status]}`}>
                        {STATUS_LABEL[c.status]}
                      </span>
                    </td>
                    <td className="py-2 text-xs text-muted">{c.detail ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>

          <Card>
            <h2 className="mb-3 text-sm font-semibold">Нагрузка</h2>
            <div className="space-y-3">
              <ResourceRow label="CPU" pct={data.resources.cpu_pct} series={data.resources.cpu_series} />
              <ResourceRow
                label="RAM"
                pct={data.resources.ram_pct}
                series={data.resources.ram_series}
                detail={data.resources.ram_used_gb != null ? `${data.resources.ram_used_gb} / ${data.resources.ram_total_gb} GB` : undefined}
              />
              <ResourceRow
                label="Disk"
                pct={data.resources.disk_pct}
                series={[]}
                detail={data.resources.disk_used_gb != null ? `${data.resources.disk_used_gb} / ${data.resources.disk_total_gb} GB` : undefined}
              />
            </div>
            <p className="mt-3 text-[11px] text-muted">
              CPU/RAM — реальная нагрузка хоста, sparkline копится с момента последнего перезапуска admin-console (не переживает redeploy).
            </p>
          </Card>

          <Card>
            <h2 className="mb-3 text-sm font-semibold">AI / GPU</h2>
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div>
                <div className="text-xs text-muted">Ollama</div>
                <div className="font-medium">{data.ai.ollama_available ? "Normal" : "Недоступен"}</div>
              </div>
              <div>
                <div className="text-xs text-muted">VRAM</div>
                <div className="font-medium">
                  {data.ai.gpu === "unavailable" ? "Not available" : `${data.ai.gpu.vram_used_gb} / ${data.ai.gpu.vram_total_gb} GB`}
                </div>
              </div>
              <div>
                <div className="text-xs text-muted">AI requests / min</div>
                <div className="font-medium">{data.ai.requests_per_min_last_hour ?? "—"}</div>
              </div>
              <div>
                <div className="text-xs text-muted">Inference p95</div>
                <div className="font-medium">
                  {data.ai.inference_p95_seconds != null ? `${data.ai.inference_p95_seconds.toFixed(1)} сек` : "нет данных за 24ч"}
                </div>
              </div>
            </div>
            <p className="mt-3 text-[11px] text-muted">
              Requests/min и p95 считаются по запросам «анализ по требованию» (ai_analysis_requests) — фоновая дедупликация и поиск первопричины латентность отдельно не логируют.
            </p>
          </Card>
        </div>
      )}
    </div>
  );
}

function ResourceRow({ label, pct, series, detail }: { label: string; pct?: number; series: number[]; detail?: string }) {
  return (
    <div className="flex items-center gap-3">
      <div className="w-12 text-xs text-muted">{label}</div>
      <div className="w-14 text-sm font-medium tabular-nums">{pct != null ? `${pct}%` : "—"}</div>
      <div className="flex-1"><Sparkline values={series} /></div>
      {detail && <div className="w-32 text-right text-xs text-muted">{detail}</div>}
    </div>
  );
}
