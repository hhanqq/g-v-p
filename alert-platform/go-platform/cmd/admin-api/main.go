package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/adminapi"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
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
	handler.UseSessions(adminapi.NewSessionManager(
		adminapi.NewLDAPAuthenticator(
			valueOr("LDAP_URL", "ldap://ldap:389"),
			valueOr("LDAP_BASE_DN", "dc=gpn-dispatcher,dc=local"),
			valueOr("LDAP_SEARCH_USER", "svc-search"),
			valueOr("LDAP_SEARCH_PASSWORD", "svc123"),
		),
		valueOr("SESSION_SECRET", "dispatcher-demo-secret"),
		valueOr("SESSION_COOKIE_SECURE", "false") == "true",
	))
	server := &http.Server{
		Addr:              ":" + valueOr("PORT", "8090"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
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
