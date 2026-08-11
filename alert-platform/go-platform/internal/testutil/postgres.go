//go:build integration

// Package testutil — раздел «PostgreSQL integration тесты» (доп. ТЗ,
// прямо названо одним из самых важных пунктов): реальная временная
// PostgreSQL в Docker (testcontainers-go), не мок и не in-memory
// имитация. Схема накатывается из ТЕХ ЖЕ файлов database/migrations/*.sql,
// что и scripts/migrate.py в проде — не параллельная копия схемы.
// Собирается только по тегу integration (go test -tags=integration),
// поэтому `go test ./...` (make check) не требует Docker и остаётся
// быстрым.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NewPostgres поднимает postgres:16 в Docker, накатывает миграции и
// возвращает готовый пул + DSN (некоторым конструкторам, например
// pipeline.New, нужна строка подключения, не готовый пул). Контейнер и
// пул останавливаются автоматически по завершении теста (t.Cleanup) —
// тест не оставляет мусора.
func NewPostgres(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()

	scripts, err := sortedMigrationFiles()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}

	// pgvector/pgvector:pg16 — тот же образ, что и в docker-compose.yml
	// (не ванильный postgres:16): 0009_knowledge_base.sql делает
	// CREATE EXTENSION vector для RAG по базе знаний.
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("alert_platform"),
		postgres.WithUsername("alert"),
		postgres.WithPassword("alert"),
		postgres.WithOrderedInitScripts(scripts...),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test postgres: %v", err)
	}
	return pool, connStr
}

// sortedMigrationFiles — тот же порядок применения, что и scripts/migrate.py
// (лексикографическая сортировка по имени файла; имена файлов уже
// нумерованы с ведущими нулями специально для этого).
func sortedMigrationFiles() ([]string, error) {
	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	// go-platform/internal/testutil/postgres.go -> alert-platform/database/migrations
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "database", "migrations")
}
