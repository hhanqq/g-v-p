package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() { s.pool.Close() }

func (s *PostgresStore) Ingest(
	ctx context.Context,
	request IngestRequest,
	bodyHash string,
	receivedAt time.Time,
) (IngestResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return IngestResult{}, fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var signalID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO signals(source_system, source_instance, external_id, received_at, raw_body, hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source_instance, hash) DO NOTHING
		RETURNING id`,
		request.SourceSystem, request.SourceInstance, request.ExternalID,
		receivedAt, request.RawBody, bodyHash,
	).Scan(&signalID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`SELECT id FROM signals WHERE source_instance = $1 AND hash = $2`,
			request.SourceInstance, bodyHash,
		).Scan(&signalID)
		if err != nil {
			return IngestResult{}, fmt.Errorf("load duplicate signal: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return IngestResult{}, fmt.Errorf("commit duplicate lookup: %w", err)
		}
		return IngestResult{SignalID: signalID, Status: "duplicate"}, nil
	}
	if err != nil {
		return IngestResult{}, fmt.Errorf("insert signal: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO signal_queue(signal_id, status, attempts, enqueued_at)
		VALUES ($1, 'pending', 0, $2)`, signalID, receivedAt)
	if err != nil {
		return IngestResult{}, fmt.Errorf("enqueue signal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return IngestResult{}, fmt.Errorf("commit ingest: %w", err)
	}
	return IngestResult{SignalID: signalID, Status: "queued"}, nil
}

func (s *PostgresStore) SourceToken(ctx context.Context, instance string) (*string, error) {
	var token *string
	err := s.pool.QueryRow(ctx, `SELECT api_token FROM source_instances WHERE instance=$1`, instance).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup source token: %w", err)
	}
	return token, nil
}

func (s *PostgresStore) Health(ctx context.Context) (int64, error) {
	var depth int64
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM signal_queue WHERE status = 'pending'`,
	).Scan(&depth)
	return depth, err
}
