// changelog-worker — два независимых, но живущих в одном процессе
// потока (меньше сервисов в docker-compose при той же наблюдаемости):
// (1) relay переносит несинхронизированные строки Postgres.change_events
// в Kafka-топик change_events.v1; (2) sink — consumer-group этого же
// топика, батчами пишущий в ClickHouse для low-code поиска. Ни один из
// двух никогда не встаёт между шлюзом и pipeline-worker'ом — оба строго
// downstream, отставание или недоступность Redpanda/ClickHouse не
// влияет на приём событий или доставку уведомлений.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

const topic = "change_events.v1"

func main() {
	databaseURL := required("DATABASE_URL")
	brokers := strings.Split(required("KAFKA_BROKERS"), ",")
	clickhouseURL := required("CLICKHOUSE_URL")
	pollInterval := durationSeconds("CHANGELOG_POLL_INTERVAL_S", 2)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("changelog-worker: connect postgres: %v", err)
	}
	defer pool.Close()

	producer, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		log.Fatalf("changelog-worker: kafka producer: %v", err)
	}
	defer producer.Close()

	// Явное создание топика через kadm, а не только надежда на
	// auto_create_topics_enabled брокера: без этого консьюмер-группа
	// может надолго зависнуть на получении метаданных несуществующего
	// топика ещё до первого продюса.
	admin := kadm.NewClient(producer)
	if _, err := admin.CreateTopic(ctx, 1, 1, nil, topic); err != nil && !isTopicExistsErr(err) {
		log.Printf("changelog-worker: create topic %s: %v (продолжаем — возможно, уже существует)", topic, err)
	}

	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup("changelog-clickhouse"),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		log.Fatalf("changelog-worker: kafka consumer: %v", err)
	}
	defer consumer.Close()

	ch, err := changelog.ConnectClickHouse(clickhouseURL)
	if err != nil {
		log.Fatalf("changelog-worker: connect clickhouse: %v", err)
	}
	defer ch.Close()

	relay := changelog.NewRelay(pool, producer, topic)
	sink := changelog.NewSink(consumer, ch)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		log.Printf("changelog-worker: relay started, poll=%s topic=%s", pollInterval, topic)
		for {
			moved, err := relay.Tick(ctx)
			if err != nil {
				log.Printf("changelog-worker: relay tick: %v", err)
			} else if moved > 0 {
				log.Printf("changelog-worker: relay moved %d change_events row(s) to kafka", moved)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}()

	go func() {
		defer wg.Done()
		log.Printf("changelog-worker: clickhouse sink started, group=changelog-clickhouse")
		if err := sink.Run(ctx); err != nil {
			log.Printf("changelog-worker: sink stopped: %v", err)
		}
	}()

	wg.Wait()
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func isTopicExistsErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "TOPIC_ALREADY_EXISTS")
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
