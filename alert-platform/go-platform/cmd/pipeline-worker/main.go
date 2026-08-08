package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/pipeline"
	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/planner"
)

func main() {
	databaseURL := required("DATABASE_URL")
	connectorsDir := valueOr("CONNECTORS_DIR", "/app/connectors")
	priorityPath := valueOr("PRIORITY_MATRIX_PATH", "/app/priority_matrix.yaml")
	pollInterval := durationSeconds("PIPELINE_POLL_INTERVAL_S", 1)
	sweepInterval := durationSeconds("TTL_SWEEP_EVERY_S", 30)
	stuckTimeout := durationSeconds("PIPELINE_STUCK_TIMEOUT_S", 120)
	aiTimeout := durationSeconds("PIPELINE_AI_TIMEOUT_S", 20)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ai := planner.NewOllamaClient(
		valueOr("OLLAMA_URL", "http://127.0.0.1:11434"),
		valueOr("OLLAMA_MODEL", "log-reader"),
		valueOr("OLLAMA_EMBED_MODEL", "nomic-embed-text"), aiTimeout,
	)
	service, err := pipeline.New(ctx, databaseURL, connectorsDir, priorityPath, ai)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()

	log.Printf("pipeline-worker: Go worker started, poll=%s sweep=%s", pollInterval, sweepInterval)
	sweepTicker := time.NewTicker(sweepInterval)
	defer sweepTicker.Stop()
	for {
		processed, err := service.Tick(ctx)
		if err != nil {
			log.Printf("pipeline-worker: tick: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-sweepTicker.C:
			closed, requeued, err := service.Sweep(ctx, stuckTimeout)
			if err != nil {
				log.Printf("pipeline-worker: sweep: %v", err)
			} else if closed > 0 || requeued > 0 {
				log.Printf("pipeline-worker: sweep closed=%d requeued=%d", closed, requeued)
			}
		default:
			if processed == 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(pollInterval):
				}
			}
		}
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
