package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/planner"
)

func main() {
	databaseURL := required("DATABASE_URL")
	pollInterval := durationSeconds("DELIVERY_POLL_INTERVAL_S", 5)
	ollamaTimeout := durationSeconds("OLLAMA_TIMEOUT_S", 90)
	ollamaURL := valueOr("OLLAMA_URL", "http://127.0.0.1:11434")
	ollamaModel := valueOr("OLLAMA_MODEL", "log-reader")
	ollamaEmbedModel := valueOr("OLLAMA_EMBED_MODEL", "nomic-embed-text")
	runbooksPath := valueOr("RUNBOOKS_PATH", "/app/runbooks.yaml")

	runbooks, err := planner.LoadRunbooks(runbooksPath)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	service, err := planner.New(
		ctx,
		databaseURL,
		planner.NewOllamaClient(ollamaURL, ollamaModel, ollamaEmbedModel, ollamaTimeout),
		runbooks,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()
	service.SetPublicConsoleURL(valueOr("TRUECONF_PUBLIC_CONSOLE_URL", "http://localhost:8090"))
	if err := service.EnsureFallbackSubscriber(ctx, os.Getenv("TRUECONF_TEST_RECIPIENT")); err != nil {
		log.Fatal(err)
	}

	log.Printf("delivery-planner: Go planner started, poll=%s", pollInterval)
	if err := service.Tick(ctx); err != nil {
		log.Printf("delivery-planner: initial tick: %v", err)
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := service.Tick(ctx); err != nil {
				log.Printf("delivery-planner: tick: %v", err)
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
