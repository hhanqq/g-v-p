package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeStore struct {
	request IngestRequest
	hash    string
	result  IngestResult
	depth   int64
	tokens  map[string]string
}

func (s *fakeStore) Ingest(_ context.Context, request IngestRequest, hash string, _ time.Time) (IngestResult, error) {
	s.request = request
	s.hash = hash
	return s.result, nil
}
func (s *fakeStore) SourceToken(_ context.Context, instance string) (*string, error) {
	if token, ok := s.tokens[instance]; ok {
		return &token, nil
	}
	return nil, nil
}
func (s *fakeStore) Health(context.Context) (int64, error) { return s.depth, nil }
func (s *fakeStore) Close()                                {}

func TestIngestCompatibility(t *testing.T) {
	store := &fakeStore{result: IngestResult{SignalID: 42, Status: "queued"}}
	handler := NewHTTPHandler(store)
	body := []byte(`{"source_system":"zabbix","source_instance":"zbx-1","raw_body":"PROBLEM"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/raw", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result IngestResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SignalID != 42 || result.Status != "queued" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if store.hash != BodyHash("PROBLEM") {
		t.Fatalf("hash mismatch: %s", store.hash)
	}
}

func TestIngestUnregisteredSourceHasNoTokenRequirement(t *testing.T) {
	// Обратная совместимость: источники, зарегистрированные до появления
	// этой проверки (или вовсе не зарегистрированные, как демо-триггеры),
	// не должны сломаться из-за отсутствия заголовка.
	store := &fakeStore{result: IngestResult{SignalID: 1, Status: "queued"}}
	handler := NewHTTPHandler(store)
	body := []byte(`{"source_system":"zabbix","source_instance":"zbx-legacy","raw_body":"PROBLEM"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/raw", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestIngestRejectsMissingOrWrongTokenForProtectedSource(t *testing.T) {
	store := &fakeStore{result: IngestResult{SignalID: 1, Status: "queued"}, tokens: map[string]string{"zbx-secure": "s3cr3t"}}
	handler := NewHTTPHandler(store)
	body := []byte(`{"source_system":"zabbix","source_instance":"zbx-secure","raw_body":"PROBLEM"}`)

	// Нет заголовка вовсе.
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/raw", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: status = %d, body = %s", response.Code, response.Body.String())
	}

	// Неверный токен.
	request = httptest.NewRequest(http.MethodPost, "/api/v1/ingest/raw", bytes.NewReader(body))
	request.Header.Set("X-Source-Token", "wrong")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestIngestAcceptsCorrectTokenForProtectedSource(t *testing.T) {
	store := &fakeStore{result: IngestResult{SignalID: 1, Status: "queued"}, tokens: map[string]string{"zbx-secure": "s3cr3t"}}
	handler := NewHTTPHandler(store)
	body := []byte(`{"source_system":"zabbix","source_instance":"zbx-secure","raw_body":"PROBLEM"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/raw", bytes.NewReader(body))
	request.Header.Set("X-Source-Token", "s3cr3t")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestIngestRejectsNUL(t *testing.T) {
	store := &fakeStore{}
	handler := NewHTTPHandler(store)
	body := []byte("{\"source_system\":\"zabbix\",\"source_instance\":\"zbx-1\",\"raw_body\":\"bad\\u0000body\"}")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/raw", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHealth(t *testing.T) {
	store := &fakeStore{depth: 7}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(store).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"queue_depth":7`)) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestOpenAPIIsValidJSON(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(openAPISpec), &spec); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("unexpected OpenAPI version: %#v", spec["openapi"])
	}
}
