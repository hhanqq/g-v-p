package changelog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
)

// DataLakeSink переносит сырые Signal в MinIO (S3-совместимый Data
// Lake) для долгосрочного хранения/переобработки/ML/RAG — раздел
// «История изменений», опциональная фаза. Читает signals СНАРУЖИ
// gateway-процесса, по водяному знаку (data_lake_watermark), в точности
// как read-реплика: сама WORM-таблица (раздел И3) никогда не
// изменяется этим кодом, только читается. Отставание или полная
// недоступность MinIO не влияет на POST /api/v1/ingest/raw —
// gateway/http.go и postgres.go этот пакет не импортируют и о нём не
// знают.
type DataLakeSink struct {
	pool   *pgxpool.Pool
	client *minio.Client
	bucket string
	batch  int
}

func NewDataLakeSink(pool *pgxpool.Pool, client *minio.Client, bucket string) *DataLakeSink {
	return &DataLakeSink{pool: pool, client: client, bucket: bucket, batch: 500}
}

// EnsureBucket создаёт бакет при первом запуске — идемпотентно,
// безопасно вызывать на каждом старте процесса.
func (sink *DataLakeSink) EnsureBucket(ctx context.Context) error {
	exists, err := sink.client.BucketExists(ctx, sink.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return sink.client.MakeBucket(ctx, sink.bucket, minio.MakeBucketOptions{})
	}
	return nil
}

type rawSignal struct {
	ID             int64     `json:"id"`
	SourceSystem   string    `json:"source_system"`
	SourceInstance string    `json:"source_instance"`
	ExternalID     *string   `json:"external_id,omitempty"`
	ReceivedAt     time.Time `json:"received_at"`
	RawBody        string    `json:"raw_body"`
	Hash           string    `json:"hash"`
}

// Tick архивирует один батч signals за водяным знаком и возвращает
// число архивированных строк.
func (sink *DataLakeSink) Tick(ctx context.Context) (int, error) {
	tx, err := sink.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var watermark int64
	if err := tx.QueryRow(ctx, `SELECT last_signal_id FROM data_lake_watermark WHERE id=1 FOR UPDATE`).Scan(&watermark); err != nil {
		return 0, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id,source_system,source_instance,external_id,received_at,raw_body,hash
		FROM signals WHERE id > $1 ORDER BY id LIMIT $2`, watermark, sink.batch)
	if err != nil {
		return 0, err
	}
	var records []rawSignal
	for rows.Next() {
		var s rawSignal
		var externalID *string
		if err := rows.Scan(&s.ID, &s.SourceSystem, &s.SourceInstance, &externalID, &s.ReceivedAt, &s.RawBody, &s.Hash); err != nil {
			rows.Close()
			return 0, err
		}
		s.ExternalID = externalID
		records = append(records, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(records) == 0 {
		return 0, nil
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return 0, err
		}
	}

	now := time.Now().UTC()
	objectName := fmt.Sprintf("raw/%04d/%02d/%02d/%d-%d.ndjson",
		now.Year(), now.Month(), now.Day(), records[0].ID, records[len(records)-1].ID)
	_, err = sink.client.PutObject(ctx, sink.bucket, objectName, bytes.NewReader(buf.Bytes()), int64(buf.Len()),
		minio.PutObjectOptions{ContentType: "application/x-ndjson"})
	if err != nil {
		return 0, err
	}

	lastID := records[len(records)-1].ID
	if _, err := tx.Exec(ctx, `UPDATE data_lake_watermark SET last_signal_id=$1, updated_at=$2 WHERE id=1`, lastID, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(records), nil
}
