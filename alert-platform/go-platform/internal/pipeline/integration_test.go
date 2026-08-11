//go:build integration

package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/testutil"
)

// Раздел «PostgreSQL integration тесты» доп. ТЗ (прямо названо одним из
// самых важных пунктов): реальная временная PostgreSQL (testutil.NewPostgres,
// testcontainers), реальные internal/pipeline.Service.Tick/processOne — не
// юнит-тест на чистых функциях (те уже покрыты parser_test.go/state_test.go),
// а сквозной прогон через реальные SQL-запросы конвейера: claim с
// FOR UPDATE SKIP LOCKED, резолвинг CMDB, дедупликация по dedup_key,
// переходы состояния OPEN→повтор→RESOLVED.
//
// go test -tags=integration ./internal/pipeline/... (нужен Docker).

const testSourceInstance = "zbx-brd-noyabrsk-01" // реальный зарегистрированный инстанс из connectors/sources.yaml

func seedTestObject(t *testing.T, service *Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := service.pool.Exec(ctx,
		`INSERT INTO cmdb_objects(id,kind,site,name) VALUES('db-03','server','brd-noyabrsk','db-03')`); err != nil {
		t.Fatalf("seed cmdb_objects: %v", err)
	}
	if _, err := service.pool.Exec(ctx,
		`INSERT INTO cmdb_aliases(site,raw_name,object_id) VALUES('brd-noyabrsk','db-03','db-03')`); err != nil {
		t.Fatalf("seed cmdb_aliases: %v", err)
	}
}

func enqueueSignal(t *testing.T, service *Service, rawBody, hash string) {
	t.Helper()
	ctx := context.Background()
	var signalID int64
	err := service.pool.QueryRow(ctx, `
		INSERT INTO signals(source_system,source_instance,received_at,raw_body,hash)
		VALUES('zabbix',$1,$2,$3,$4) RETURNING id`,
		testSourceInstance, time.Now().UTC(), rawBody, hash,
	).Scan(&signalID)
	if err != nil {
		t.Fatalf("insert signal: %v", err)
	}
	if _, err := service.pool.Exec(ctx, `
		INSERT INTO signal_queue(signal_id,status,attempts,enqueued_at) VALUES($1,'pending',0,$2)`,
		signalID, time.Now().UTC()); err != nil {
		t.Fatalf("insert signal_queue: %v", err)
	}
}

// TestPipelineIngestDedupeAndResolve — сквозной цикл «первое срабатывание
// создаёт Problem» → «повторное срабатывание того же объекта увеличивает
// repeat_count, а НЕ создаёт вторую строку Problem (дедупликация по
// dedup_key)» → «RESOLVED-событие переводит проблему в статус RESOLVED».
// Именно этот путь CLAUDE.md называет «идемпотентная, безопасная для
// нескольких реплик обработка» — здесь проверяется на реальной БД, не
// на моке пула.
func TestPipelineIngestDedupeAndResolve(t *testing.T) {
	ctx := context.Background()
	_, dsn := testutil.NewPostgres(t)

	service, err := New(ctx, dsn, projectPath(t, "connectors"), projectPath(t, "packages", "rules", "priority_matrix.yaml"), nil)
	if err != nil {
		t.Fatalf("pipeline.New: %v", err)
	}
	defer service.Close()

	seedTestObject(t, service)

	firing1 := "PROBLEM: Free disk space is less than 70% on volume C:\nHost: db-03 (10.42.50.5)\nSeverity: Warning\nTime: 2026.08.06 03:35:21"
	enqueueSignal(t, service, firing1, "hash-1")
	if n, err := service.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("first tick: claimed=%d err=%v", n, err)
	}

	var problemCount, repeatCount int
	var status string
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM problems WHERE object_id='db-03'`).Scan(&problemCount); err != nil {
		t.Fatalf("count problems: %v", err)
	}
	if problemCount != 1 {
		t.Fatalf("expected exactly 1 problem after first firing event, got %d", problemCount)
	}
	if err := service.pool.QueryRow(ctx, `SELECT repeat_count, status FROM problems WHERE object_id='db-03'`).Scan(&repeatCount, &status); err != nil {
		t.Fatalf("load problem: %v", err)
	}
	if repeatCount != 1 || status != "OPEN" {
		t.Fatalf("unexpected initial state: repeat_count=%d status=%s", repeatCount, status)
	}

	firing2 := "PROBLEM: Free disk space is less than 70% on volume C:\nHost: db-03 (10.42.50.5)\nSeverity: Warning\nTime: 2026.08.06 04:10:00"
	enqueueSignal(t, service, firing2, "hash-2")
	if _, err := service.Tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM problems WHERE object_id='db-03'`).Scan(&problemCount); err != nil {
		t.Fatalf("count problems after repeat: %v", err)
	}
	if problemCount != 1 {
		t.Fatalf("repeated firing event must NOT create a second problem row (dedup), got %d rows", problemCount)
	}
	if err := service.pool.QueryRow(ctx, `SELECT repeat_count FROM problems WHERE object_id='db-03'`).Scan(&repeatCount); err != nil {
		t.Fatalf("load repeat_count: %v", err)
	}
	if repeatCount != 2 {
		t.Fatalf("expected repeat_count=2 after second firing of the same problem, got %d", repeatCount)
	}

	resolved := "RESOLVED: Free disk space is less than 70% on volume C:\nHost: db-03 (10.42.50.5)\nSeverity: Warning\nTime: 2026.08.06 05:00:00"
	enqueueSignal(t, service, resolved, "hash-3")
	if _, err := service.Tick(ctx); err != nil {
		t.Fatalf("third tick: %v", err)
	}

	if err := service.pool.QueryRow(ctx, `SELECT status FROM problems WHERE object_id='db-03'`).Scan(&status); err != nil {
		t.Fatalf("load status after resolve: %v", err)
	}
	if status != "RESOLVED" {
		t.Fatalf("expected status=RESOLVED after RESOLVED: event, got %q", status)
	}
}

// TestClaimBatchSkipsLockedRows — раздел CLAUDE.md «для очередей используй
// транзакции и FOR UPDATE SKIP LOCKED; обработка должна быть безопасной
// для нескольких реплик»: доказывает буквально, что вторая транзакция не
// видит строку, залоченную первой, пока первая не отпустит лок — то самое
// поведение, на которое рассчитан multi-replica claim.
func TestClaimBatchSkipsLockedRows(t *testing.T) {
	ctx := context.Background()
	pool, dsn := testutil.NewPostgres(t)

	service, err := New(ctx, dsn, projectPath(t, "connectors"), projectPath(t, "packages", "rules", "priority_matrix.yaml"), nil)
	if err != nil {
		t.Fatalf("pipeline.New: %v", err)
	}
	defer service.Close()

	enqueueSignal(t, service, "PROBLEM: Unavailable by ICMP ping\nHost: sw-01 (10.1.1.1)\nSeverity: Average\nTime: 2026.08.06 03:35:21", "hash-lock-1")

	txA, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin txA: %v", err)
	}
	defer func() { _ = txA.Rollback(ctx) }()
	var lockedID int64
	if err := txA.QueryRow(ctx, `SELECT id FROM signal_queue WHERE status='pending' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&lockedID); err != nil {
		t.Fatalf("txA lock row: %v", err)
	}

	// Второй реплик (собственное подключение через service.pool, как claimBatch)
	// не должен увидеть строку, залоченную txA.
	ids, err := service.claimBatch(ctx)
	if err != nil {
		t.Fatalf("claimBatch while locked: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected claimBatch to skip the row locked by txA, got %v", ids)
	}

	if err := txA.Rollback(ctx); err != nil {
		t.Fatalf("rollback txA: %v", err)
	}

	ids, err = service.claimBatch(ctx)
	if err != nil {
		t.Fatalf("claimBatch after unlock: %v", err)
	}
	if len(ids) != 1 || ids[0] != lockedID {
		t.Fatalf("expected to claim the now-unlocked row %d, got %v", lockedID, ids)
	}
}
