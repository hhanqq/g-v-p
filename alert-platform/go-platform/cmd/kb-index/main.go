// kb-index — одноразовая индексация базы знаний компании
// (knowledge_base/*.md) в kb_chunks для RAG в ИИ-разборе алерта по
// запросу (internal/planner/ai_analysis.go). Запускается вручную/при
// деплое (docker compose run --rm kb-index), не часть постоянно
// работающего рантайма — аналог scripts/migrate.py по духу.
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/planner"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	databaseURL := required("DATABASE_URL")
	ollamaURL := valueOr("OLLAMA_URL", "http://127.0.0.1:11434")
	embedModel := valueOr("OLLAMA_EMBED_MODEL", "nomic-embed-text")
	kbDir := valueOr("KB_DIR", "/app/knowledge_base")
	timeout := durationSeconds("OLLAMA_TIMEOUT_S", 60)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("kb-index: connect postgres: %v", err)
	}
	defer pool.Close()

	ollama := planner.NewOllamaClient(ollamaURL, "", embedModel, timeout)

	files, err := filepath.Glob(filepath.Join(kbDir, "*.md"))
	if err != nil {
		log.Fatalf("kb-index: list %s: %v", kbDir, err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		log.Fatalf("kb-index: no .md files found in %s", kbDir)
	}

	totalChunks, totalFailed := 0, 0
	for _, path := range files {
		source := filepath.Base(path)
		body, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("kb-index: read %s: %v", path, err)
		}
		chunks := planner.ChunkMarkdown(string(body))

		if _, err := pool.Exec(ctx, `DELETE FROM kb_chunks WHERE source=$1`, source); err != nil {
			log.Fatalf("kb-index: clear previous chunks for %s: %v", source, err)
		}

		indexed := 0
		for i, chunk := range chunks {
			vector := ollama.Embed(ctx, chunk.Title+"\n"+chunk.Content)
			if vector == nil {
				log.Printf("kb-index: WARNING embedding failed for %s chunk %d (%q) — пропущен", source, i, chunk.Title)
				totalFailed++
				continue
			}
			_, err := pool.Exec(ctx, `
				INSERT INTO kb_chunks(source, title, chunk_index, content, embedding, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6)`,
				source, chunk.Title, i, chunk.Content, planner.VectorLiteral(vector), time.Now().UTC(),
			)
			if err != nil {
				log.Fatalf("kb-index: insert chunk for %s: %v", source, err)
			}
			indexed++
		}
		log.Printf("kb-index: %s -> %d chunks indexed", source, indexed)
		totalChunks += indexed
	}
	log.Printf("kb-index: done, %d files, %d chunks indexed, %d embedding failures", len(files), totalChunks, totalFailed)
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

func durationSeconds(name string, fallback float64) time.Duration {
	value := fallback
	if raw := os.Getenv(name); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			log.Fatalf("invalid %s: %v", name, err)
		}
		value = parsed
	}
	return time.Duration(value * float64(time.Second))
}
