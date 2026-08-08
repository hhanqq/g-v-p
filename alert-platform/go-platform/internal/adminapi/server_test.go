package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAuthenticator struct{}

func (fakeAuthenticator) Authenticate(_ context.Context, username, password string) (bool, bool) {
	return username == "admin" && password == "secret", true
}

func TestIntegrationsRequiresGoSession(t *testing.T) {
	demo := httptest.NewServer(http.NotFoundHandler())
	defer demo.Close()
	server, err := New(nil, demo.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server.UseSessions(NewSessionManager(fakeAuthenticator{}, "test-secret", false))

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/integrations/status", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}

	login := httptest.NewRecorder()
	server.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"secret"}`)))
	request := httptest.NewRequest(http.MethodGet, "/console/api/integrations/status", nil)
	request.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "TrueConf") {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
}

func TestOnlyDemoRoutesAreProxied(t *testing.T) {
	demo := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/ai-selftest" {
			t.Fatalf("unexpected proxy path: %s", request.URL.Path)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer demo.Close()
	server, err := New(nil, demo.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/console/api/ai-selftest", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/console/not-migrated", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown route should not be proxied: %d", missing.Code)
	}
}

func TestGoSessionLoginMeLogout(t *testing.T) {
	demo := httptest.NewServer(http.NotFoundHandler())
	defer demo.Close()
	server, err := New(nil, demo.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessionManager(fakeAuthenticator{}, "test-secret", false)
	server.UseSessions(sessions)

	login := httptest.NewRecorder()
	server.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"secret"}`)))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe session cookie: %#v", cookie)
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meRequest.AddCookie(cookie)
	me := httptest.NewRecorder()
	server.ServeHTTP(me, meRequest)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"admin"`) {
		t.Fatalf("me failed: %d %s", me.Code, me.Body.String())
	}

	tampered := *cookie
	tampered.Value += "x"
	tamperedRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	tamperedRequest.AddCookie(&tampered)
	tamperedResponse := httptest.NewRecorder()
	server.ServeHTTP(tamperedResponse, tamperedRequest)
	if tamperedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie accepted: %d", tamperedResponse.Code)
	}
}

func TestLegacyHTMLRoutesRedirectToSPA(t *testing.T) {
	demo := httptest.NewServer(http.NotFoundHandler())
	defer demo.Close()
	server, err := New(nil, demo.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/console/sources/", nil))
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/console/app/sources" {
		t.Fatalf("unexpected redirect: %d %s", response.Code, response.Header().Get("Location"))
	}
}
