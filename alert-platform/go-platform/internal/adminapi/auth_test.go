package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginLimiterBlocksAfterMaxAttemptsWithinWindow(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for i := 0; i < loginMaxAttempts; i++ {
		if !limiter.allow("admin1", now) {
			t.Fatalf("attempt %d should still be allowed", i+1)
		}
		limiter.recordFailure("admin1", now)
	}
	if limiter.allow("admin1", now) {
		t.Fatalf("should be blocked after %d failed attempts", loginMaxAttempts)
	}
	// Другой логин не должен быть задет чужими неудачными попытками.
	if !limiter.allow("engineer1", now) {
		t.Fatalf("a different username must not share the same limiter bucket")
	}
}

func TestLoginLimiterForgetsAttemptsOutsideWindow(t *testing.T) {
	limiter := newLoginLimiter()
	start := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for i := 0; i < loginMaxAttempts; i++ {
		limiter.recordFailure("admin1", start)
	}
	later := start.Add(loginWindow + time.Second)
	if !limiter.allow("admin1", later) {
		t.Fatalf("attempts older than the window should no longer count")
	}
}

func TestLoginLimiterResetsOnSuccess(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for i := 0; i < loginMaxAttempts-1; i++ {
		limiter.recordFailure("admin1", now)
	}
	limiter.recordSuccess("admin1")
	if !limiter.allow("admin1", now) {
		t.Fatalf("a successful login should clear the failure count")
	}
}

func TestLoginHandlerRateLimitsRepeatedFailures(t *testing.T) {
	demo := httptest.NewServer(http.NotFoundHandler())
	defer demo.Close()
	server, err := New(nil, demo.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server.UseSessions(NewSessionManager(fakeAuthenticator{}, "test-secret", false))

	login := func(password string) int {
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body)))
		return response.Code
	}

	for i := 0; i < loginMaxAttempts; i++ {
		if code := login("wrong"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, code)
		}
	}
	if code := login("wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d failures, got %d", loginMaxAttempts, code)
	}
	// Даже правильный пароль не должен обходить лимит, пока окно не истекло.
	if code := login("secret"); code != http.StatusTooManyRequests {
		t.Fatalf("correct password should still be rate-limited within the window, got %d", code)
	}
}
