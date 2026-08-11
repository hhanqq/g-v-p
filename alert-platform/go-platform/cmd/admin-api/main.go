package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/adminapi"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/planner"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, required("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	handler, err := adminapi.New(
		pool,
		valueOr("DEMO_RUNNER_URL", "http://demo-runner:8091"),
		valueOr("WEB_DIST_DIR", "/app/web"),
	)
	if err != nil {
		log.Fatal(err)
	}
	// ClickHouse — опциональный аналитический сток для low-code поиска
	// (раздел «История изменений»). Не задан CLICKHOUSE_URL — не авария:
	// остальной admin API работает как раньше, только поиск вернёт 503.
	if chURL := os.Getenv("CLICKHOUSE_URL"); chURL != "" {
		chConn, err := changelog.ConnectClickHouse(chURL)
		if err != nil {
			log.Printf("admin-api: connect clickhouse: %v (low-code поиск будет недоступен)", err)
		} else {
			handler.UseClickHouse(chConn)
		}
	}
	// Ollama — опциональная локальная LLM для «умная маршрутизация на
	// основе истории» (раздел «Использование ИИ»): формулирует подсказку
	// по подписке для нового сотрудника. Не задан OLLAMA_URL — подсказка
	// всё равно показывается (реальный SQL к истории подписок коллег),
	// просто шаблонной фразой вместо ИИ-формулировки. 20с — не мгновенно,
	// но ограничено: модель log-reader (30B) при холодном старте отвечает
	// ~30-40с (см. память проекта), при уже прогретой (keep_alive: 30m в
	// OllamaClient.Ask, вызов остальными ИИ-сценариями держит её тёплой)
	// — единицы секунд. Раздел И5: любой таймаут — деградация к шаблону,
	// не ошибка.
	if ollamaURL := os.Getenv("OLLAMA_URL"); ollamaURL != "" {
		handler.UseOllama(planner.NewOllamaClient(ollamaURL, valueOr("OLLAMA_MODEL", "log-reader"), "", 35*time.Second))
	}
	// GPU_TOTAL_VRAM_MB — реальная физическая емкость VRAM карты хоста
	// (nvidia-smi --query-gpu=memory.total), задаётся оператором один раз
	// в .env; не задано — «Состояние системы» показывает VRAM used без
	// total, не выдуманную емкость (раздел 26 доп. ТЗ).
	if totalMB, err := strconv.Atoi(os.Getenv("GPU_TOTAL_VRAM_MB")); err == nil && totalMB > 0 {
		handler.UseGPUCapacity(totalMB)
	}
	if gatewayURL := os.Getenv("GATEWAY_HEALTH_URL"); gatewayURL != "" {
		handler.UseGatewayHealthURL(gatewayURL)
	}
	// LDAP_SEARCH_PASSWORD/SESSION_SECRET — раньше имели хардкоженный
	// демо-фолбэк ("svc123"/"dispatcher-demo-secret") прямо в исходном
	// коде: любой новый деплой без явной настройки .env тихо подписывал
	// бы сессии этим публичным значением (полный auth bypass — известный
	// секрет подписи куки), либо ходил бы в LDAP известным паролем.
	// Реальный прод-стенд уже был найден с этим фолбэком живым (раздел
	// «аудит паролей»), исправлено точечной ротацией .env на машине —
	// но сам фолбэк в коде оставался и повторил бы проблему на следующем
	// чистом деплое. required() — явный отказ старта, не тихий дефолт.
	handler.UseSessions(adminapi.NewSessionManager(
		adminapi.NewLDAPAuthenticator(
			valueOr("LDAP_URL", "ldap://ldap:389"),
			valueOr("LDAP_BASE_DN", "dc=gpn-dispatcher,dc=local"),
			valueOr("LDAP_SEARCH_USER", "svc-search"),
			required("LDAP_SEARCH_PASSWORD"),
		),
		required("SESSION_SECRET"),
		valueOr("SESSION_COOKIE_SECURE", "false") == "true",
	))
	server := &http.Server{
		Addr:              ":" + valueOr("PORT", "8090"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// 30с было мало: /api/ai-selftest (проксируется на demo-runner) и
		// /api/employees/{id}/subscription-suggestion оба зовут Ollama
		// синхронно, холодный старт log-reader (30B) измерен вживую до
		// ~49с — сервер обрывал соединение на середине ответа ("empty
		// reply from server"), раньше, чем успевал сработать таймаут
		// самого Ollama-клиента (35с). Найдено при разборе критерия
		// «Инфраструктура» 2026-08-10.
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		// ctx уже отменён сигналом; shutdown нужен независимый timeout, не унаследованный от отменённого ctx
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx) //nolint:contextcheck
	}()
	log.Printf("admin-api: Go facade started on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func valueOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
