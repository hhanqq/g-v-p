package adminapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// resourceSample — один снимок нагрузки хоста. CPU/RAM читаются из
// /proc внутри контейнера admin-console, который делит /proc с хостом
// (не namespaced по памяти/CPU — подтверждено живым сравнением с `free`
// на самом хосте), поэтому это настоящая нагрузка хоста, не контейнера.
// Диск — через read-only bind-mount host / в /hostfs (docker-compose),
// иначе контейнер видел бы только свой overlay, а не реальный диск ВМ.
type resourceSample struct {
	At       time.Time
	CPUPct   float64
	RAMUsed  uint64
	RAMTotal uint64
	DiskUsed uint64
	DiskTot  uint64
}

const resourceSampleInterval = 30 * time.Second
const resourceSampleWindow = time.Hour

type resourceSampler struct {
	mu      sync.Mutex
	samples []resourceSample
}

func newResourceSampler() *resourceSampler {
	return &resourceSampler{}
}

// start — раздел 25 доп. ТЗ: sparkline за последний час требует
// истории, которой сегодня нигде не было; фоновый семплер в процессе
// admin-api — самый простой честный источник. Сбрасывается при
// перезапуске процесса (не переживает redeploy) — это ограничение
// сознательно принято, не выдаётся за постоянное хранилище метрик.
func (sampler *resourceSampler) start(ctx context.Context) {
	take := func() {
		sample := resourceSample{At: time.Now().UTC()}
		if percents, err := cpu.Percent(200*time.Millisecond, false); err == nil && len(percents) > 0 {
			sample.CPUPct = percents[0]
		}
		if vm, err := mem.VirtualMemory(); err == nil {
			sample.RAMUsed, sample.RAMTotal = vm.Used, vm.Total
		}
		if usage, err := disk.Usage("/hostfs"); err == nil {
			sample.DiskUsed, sample.DiskTot = usage.Used, usage.Total
		}
		sampler.mu.Lock()
		sampler.samples = append(sampler.samples, sample)
		cutoff := time.Now().UTC().Add(-resourceSampleWindow)
		kept := sampler.samples[:0]
		for _, s := range sampler.samples {
			if s.At.After(cutoff) {
				kept = append(kept, s)
			}
		}
		sampler.samples = kept
		sampler.mu.Unlock()
	}
	take()
	go func() {
		ticker := time.NewTicker(resourceSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				take()
			}
		}
	}()
}

func (sampler *resourceSampler) recent() []resourceSample {
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	out := make([]resourceSample, len(sampler.samples))
	copy(out, sampler.samples)
	return out
}

type componentStatus struct {
	Name   string  `json:"name"`
	Status string  `json:"status"` // "normal" | "degraded" | "unknown"
	Detail *string `json:"detail,omitempty"`
}

func str(s string) *string { return &s }

// platformHealth — GET /api/platform-health (раздел 24-26 доп. ТЗ).
// «Состояние системы»: техническая диагностика самой ADP, в отличие от
// Главной/Аналитики (бизнес-метрики). Каждый компонент — реальная
// проверка, не заглушка: Postgres/Ollama — прямой ping/HTTP, Gateway —
// его собственный /api/v1/health по внутренней docker-сети, Pipeline —
// возраст самой старой необработанной записи очереди, TrueConf/Email —
// фактический процент успешных доставок за последний час (у этих двух
// воркеров нет HTTP-эндпоинта здоровья, поэтому честный сигнал — их
// реальный результат, а не придуманный ping).
func (server *Server) platformHealth(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	components := []componentStatus{
		server.checkPostgres(ctx),
		server.checkGateway(ctx),
		server.checkPipeline(ctx),
		server.checkDeliveryChannel(ctx, "trueconf", "TrueConf"),
		server.checkDeliveryChannel(ctx, "email", "Email"),
		server.checkOllama(ctx),
	}

	samples := server.resources.recent()
	var latest *resourceSample
	if len(samples) > 0 {
		latest = &samples[len(samples)-1]
	}
	cpuSeries := make([]float64, 0, len(samples))
	ramSeries := make([]float64, 0, len(samples))
	for _, s := range samples {
		cpuSeries = append(cpuSeries, round(s.CPUPct, 1))
		if s.RAMTotal > 0 {
			ramSeries = append(ramSeries, round(float64(s.RAMUsed)*100/float64(s.RAMTotal), 1))
		}
	}

	resources := map[string]any{"cpu_series": cpuSeries, "ram_series": ramSeries}
	if latest != nil {
		resources["cpu_pct"] = round(latest.CPUPct, 1)
		if latest.RAMTotal > 0 {
			resources["ram_pct"] = round(float64(latest.RAMUsed)*100/float64(latest.RAMTotal), 1)
			resources["ram_used_gb"] = round(float64(latest.RAMUsed)/1e9, 1)
			resources["ram_total_gb"] = round(float64(latest.RAMTotal)/1e9, 1)
		}
		if latest.DiskTot > 0 {
			resources["disk_pct"] = round(float64(latest.DiskUsed)*100/float64(latest.DiskTot), 1)
			resources["disk_used_gb"] = round(float64(latest.DiskUsed)/1e9, 1)
			resources["disk_total_gb"] = round(float64(latest.DiskTot)/1e9, 1)
		}
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"components": components,
		"resources":  resources,
		"ai":         server.aiTelemetry(ctx),
	})
}

func (server *Server) checkPostgres(ctx context.Context) componentStatus {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := server.pool.Ping(pingCtx); err != nil {
		return componentStatus{Name: "PostgreSQL", Status: "degraded", Detail: str(err.Error())}
	}
	return componentStatus{Name: "PostgreSQL", Status: "normal"}
}

func (server *Server) checkOllama(ctx context.Context) componentStatus {
	if server.ollama == nil {
		return componentStatus{Name: "Ollama", Status: "unknown", Detail: str("не настроен (OLLAMA_URL)")}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if !server.ollama.Reachable(checkCtx) {
		return componentStatus{Name: "Ollama", Status: "degraded", Detail: str("нет ответа от /api/tags")}
	}
	return componentStatus{Name: "Ollama", Status: "normal"}
}

func (server *Server) checkGateway(ctx context.Context) componentStatus {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, server.gatewayHealthURL, nil)
	if err != nil {
		return componentStatus{Name: "Gateway", Status: "unknown"}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return componentStatus{Name: "Gateway", Status: "degraded", Detail: str(err.Error())}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return componentStatus{Name: "Gateway", Status: "degraded"}
	}
	return componentStatus{Name: "Gateway", Status: "normal"}
}

// checkPipeline — нет HTTP-эндпоинта здоровья у pipeline-worker (это
// очередь-потребитель, не веб-сервис); честный сигнал живости —
// возраст самой старой необработанной записи очереди, а не выдуманный
// ping. 10 минут — тот же порядок величины, что и остальные пороги
// деградации в проекте (не мгновенно чувствительно к нормальным
// всплескам, но ловит реально зависшую очередь).
func (server *Server) checkPipeline(ctx context.Context) componentStatus {
	var oldest sql.NullTime
	err := server.pool.QueryRow(ctx, `
		SELECT MIN(enqueued_at) FROM signal_queue WHERE status IN ('pending','processing')`,
	).Scan(&oldest)
	if err != nil {
		return componentStatus{Name: "Pipeline", Status: "unknown", Detail: str(err.Error())}
	}
	if !oldest.Valid {
		return componentStatus{Name: "Pipeline", Status: "normal"}
	}
	age := time.Since(oldest.Time)
	if age > 10*time.Minute {
		return componentStatus{Name: "Pipeline", Status: "degraded", Detail: str("в очереди есть необработанная запись старше 10 минут")}
	}
	return componentStatus{Name: "Pipeline", Status: "normal"}
}

func (server *Server) checkDeliveryChannel(ctx context.Context, channel, label string) componentStatus {
	var total, sent int64
	err := server.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status='sent')
		FROM delivery_outbox WHERE channel=$1 AND created_at >= $2`,
		channel, time.Now().UTC().Add(-time.Hour),
	).Scan(&total, &sent)
	if err != nil {
		return componentStatus{Name: label, Status: "unknown"}
	}
	if total == 0 {
		return componentStatus{Name: label, Status: "unknown", Detail: str("нет попыток доставки за последний час")}
	}
	successPct := float64(sent) * 100 / float64(total)
	if successPct < 80 {
		return componentStatus{Name: label, Status: "degraded", Detail: str(fmt.Sprintf("успешных доставок за час: %.1f%%", successPct))}
	}
	return componentStatus{Name: label, Status: "normal"}
}

// aiTelemetry — раздел 26: «AI / GPU» на Главной. p95/requests-per-min
// считаются из реальных ai_analysis_requests (единственный AI-путь,
// где есть created_at/processed_at на каждый запрос — фоновая
// дедупликация/root-cause не логируют латентность отдельно). VRAM used
// — из Ollama /api/ps (реально загруженные модели), total — из
// GPU_TOTAL_VRAM_MB (реальная физическая емкость этой карты, задаётся
// один раз оператором, потому что сам Ollama API её не отдаёт, а
// nvidia-smi недоступен из контейнера без GPU passthrough). Ничего из
// этого не подставляется фиктивным значением — при недоступности сразу
// nil/"unavailable".
func (server *Server) aiTelemetry(ctx context.Context) map[string]any {
	result := map[string]any{"ollama_available": false, "gpu": "unavailable"}
	if server.ollama == nil {
		return result
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result["ollama_available"] = server.ollama.Reachable(checkCtx)

	if models, err := server.ollama.RunningModels(checkCtx); err == nil {
		var usedBytes int64
		for _, m := range models {
			usedBytes += m.SizeVRAM
		}
		if server.gpuTotalVRAMMB > 0 {
			result["gpu"] = map[string]any{
				"vram_used_gb":  round(float64(usedBytes)/1e9, 1),
				"vram_total_gb": round(float64(server.gpuTotalVRAMMB)/1024, 1),
			}
		}
	}

	var requestsLastHour int64
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE processed_at IS NOT NULL) FROM ai_analysis_requests WHERE created_at >= $1`,
		time.Now().UTC().Add(-time.Hour),
	).Scan(&requestsLastHour); err == nil {
		result["requests_per_min_last_hour"] = round(float64(requestsLastHour)/60, 2)
	}

	p95, err := queryNullableFloat(ctx, server.pool, `
		SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (processed_at-created_at)))
		FROM ai_analysis_requests WHERE processed_at IS NOT NULL AND created_at >= $1`,
		time.Now().UTC().Add(-24*time.Hour))
	if err == nil {
		result["inference_p95_seconds"] = p95
	}
	return result
}
