package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var ErrInvalidNUL = errors.New("raw_body содержит недопустимый NUL-байт")

type IngestRequest struct {
	SourceSystem   string  `json:"source_system"`
	SourceInstance string  `json:"source_instance"`
	RawBody        string  `json:"raw_body"`
	ExternalID     *string `json:"external_id"`
}

type IngestResult struct {
	SignalID int64  `json:"signal_id"`
	Status   string `json:"status"`
}

type Health struct {
	Status     string `json:"status"`
	DB         string `json:"db"`
	QueueDepth *int64 `json:"queue_depth"`
}

type Store interface {
	Ingest(context.Context, IngestRequest, string, time.Time) (IngestResult, error)
	Health(context.Context) (int64, error)
	Close()
}

func BodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
