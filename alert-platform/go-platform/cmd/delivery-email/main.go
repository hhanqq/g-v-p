package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/deliveryemail"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	databaseURL := required("DATABASE_URL")
	pollInterval := durationSeconds("DELIVERY_POLL_INTERVAL_S", 2)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	service := deliveryemail.New(pool, deliveryemail.Config{
		SMTPHost:        required("SMTP_HOST"),
		SMTPPort:        valueOr("SMTP_PORT", "25"),
		SMTPUsername:    os.Getenv("SMTP_USERNAME"),
		SMTPPassword:    os.Getenv("SMTP_PASSWORD"),
		FromAddress:     valueOr("SMTP_FROM", "dispatcher@gpn-dispatcher.local"),
		TrackingBaseURL: os.Getenv("EMAIL_TRACKING_BASE_URL"),
	})

	log.Printf("delivery-email: started, poll=%s", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if n, err := service.Tick(ctx); err != nil {
			log.Printf("delivery-email: tick: %v", err)
		} else if n > 0 {
			log.Printf("delivery-email: processed %d command(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
