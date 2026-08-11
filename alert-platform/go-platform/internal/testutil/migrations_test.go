//go:build integration

package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestMigrationsApplyCleanlyAndAreReapplySafe — раздел «Migration тесты
// (clean + upgrade)» доп. ТЗ, и правило CLAUDE.md «делай повторное
// применение безопасным и проверяй как на существующей, так и на
// чистой PostgreSQL БД». Поднимает голый контейнер (без init-скриптов —
// они бы прогнали миграции только один раз при старте), затем
// накатывает все файлы database/migrations/*.sql вручную ДВАЖДЫ на
// одном и том же экземпляре: первый проход — чистая БД, второй —
// «уже существующая», симулирует повторный деплой/рестарт миграционного
// job. Каждый CREATE TABLE/ADD COLUMN в проекте написан как IF NOT
// EXISTS — второй проход должен быть безопасным no-op, не ошибкой.
func TestMigrationsApplyCleanlyAndAreReapplySafe(t *testing.T) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("alert_platform"),
		postgres.WithUsername("alert"),
		postgres.WithPassword("alert"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	files, err := sortedMigrationFiles()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migration files found — path resolution is likely broken")
	}

	applyAll := func(pass string) {
		for _, path := range files {
			sqlBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("[%s] read %s: %v", pass, path, err)
			}
			if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
				t.Fatalf("[%s] apply %s: %v", pass, path, err)
			}
		}
	}

	applyAll("clean DB (первый прогон)")
	applyAll("existing DB (повторный прогон — идемпотентность)")
}
