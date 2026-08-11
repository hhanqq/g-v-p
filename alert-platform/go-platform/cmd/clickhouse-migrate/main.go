// clickhouse-migrate — одноразовое применение
// database/clickhouse_migrations/*.sql к ClickHouse. Параллельная
// Python-migrate.py конвенция (тот же паттерн, что migrate/kb-index —
// docker compose run --rm, restart: "no", не часть постоянного
// рантайма): ClickHouse — аналитический сток, не источник правды
// схемы, поэтому не стоит тянуть Python-раннер миграций на второй SQL-
// диалект вместо отдельного простого Go-бинаря.
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

func main() {
	dsn := required("CLICKHOUSE_URL")
	dir := valueOr("CLICKHOUSE_MIGRATIONS_DIR", "/app/clickhouse_migrations")

	conn, err := changelog.ConnectClickHouse(dsn)
	if err != nil {
		log.Fatalf("clickhouse-migrate: connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		log.Fatalf("clickhouse-migrate: list %s: %v", dir, err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		log.Fatalf("clickhouse-migrate: no .sql files found in %s", dir)
	}

	ctx := context.Background()
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("clickhouse-migrate: read %s: %v", path, err)
		}
		applied := 0
		for _, chunk := range strings.Split(string(body), ";") {
			statement := stripComments(chunk)
			if statement == "" {
				continue
			}
			if err := conn.Exec(ctx, statement); err != nil {
				log.Fatalf("clickhouse-migrate: %s: %v\nstatement: %s", filepath.Base(path), err, statement)
			}
			applied++
		}
		log.Printf("clickhouse-migrate: applied %s (%d statements)", filepath.Base(path), applied)
	}
	log.Printf("clickhouse-migrate: done, %d files", len(files))
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

// stripComments убирает строки-комментарии (--) и пустые строки внутри
// одного ";"-разделённого куска перед выполнением — простой парсинг
// не должен путать заголовочный блок комментариев файла с самим SQL,
// который идёт следом без разделителя ";" между ними.
func stripComments(chunk string) string {
	var lines []string
	for _, line := range strings.Split(chunk, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
