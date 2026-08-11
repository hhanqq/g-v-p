package adminapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"
)

const sessionCookieName = "dispatcher_session"

type Authenticator interface {
	Authenticate(context.Context, string, string) (authenticated bool, isAdmin bool)
}

type LDAPAuthenticator struct {
	URL            string
	BaseDN         string
	SearchUsername string
	SearchPassword string
	AdminGroupDN   string
	DialTimeout    time.Duration
}

func NewLDAPAuthenticator(url, baseDN, searchUsername, searchPassword string) *LDAPAuthenticator {
	return &LDAPAuthenticator{
		URL: url, BaseDN: baseDN, SearchUsername: searchUsername, SearchPassword: searchPassword,
		AdminGroupDN: "ou=admins,ou=groups," + baseDN, DialTimeout: 5 * time.Second,
	}
}

func (auth *LDAPAuthenticator) Authenticate(ctx context.Context, username, password string) (bool, bool) {
	if username == "" || password == "" || ctx.Err() != nil {
		return false, false
	}
	search, err := auth.dial()
	if err != nil {
		return false, false
	}
	defer func() { _ = search.Close() }()
	searchDN := fmt.Sprintf("cn=%s,ou=employees,ou=users,%s", auth.SearchUsername, auth.BaseDN)
	if err := search.Bind(searchDN, auth.SearchPassword); err != nil {
		return false, false
	}
	result, err := search.Search(ldap.NewSearchRequest(
		"ou=users,"+auth.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		1, 5, false,
		fmt.Sprintf("(&(objectClass=posixAccount)(uid=%s))", ldap.EscapeFilter(username)),
		[]string{"memberOf"}, nil,
	))
	if err != nil || len(result.Entries) == 0 {
		return false, false
	}
	entry := result.Entries[0]
	userConnection, err := auth.dial()
	if err != nil {
		return false, false
	}
	defer func() { _ = userConnection.Close() }()
	if err := userConnection.Bind(entry.DN, password); err != nil {
		return false, false
	}
	isAdmin := false
	for _, group := range entry.GetAttributeValues("memberOf") {
		if group == auth.AdminGroupDN {
			isAdmin = true
			break
		}
	}
	return true, isAdmin
}

func (auth *LDAPAuthenticator) dial() (*ldap.Conn, error) {
	dialer := &net.Dialer{Timeout: auth.DialTimeout}
	return ldap.DialURL(auth.URL, ldap.DialWithDialer(dialer))
}

type sessionPayload struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	Guest    bool   `json:"guest"`
	Expires  int64  `json:"expires"`
}

type SessionManager struct {
	authenticator Authenticator
	secret        []byte
	lifetime      time.Duration
	secureCookie  bool
	now           func() time.Time
	limiter       *loginLimiter
}

func NewSessionManager(authenticator Authenticator, secret string, secureCookie bool) *SessionManager {
	return &SessionManager{
		authenticator: authenticator, secret: []byte(secret), lifetime: 14 * 24 * time.Hour,
		secureCookie: secureCookie, now: func() time.Time { return time.Now().UTC() },
		limiter: newLoginLimiter(),
	}
}

// loginLimiter — защита от подбора пароля: не более loginMaxAttempts
// неудачных попыток на один логин за loginWindow. В памяти процесса —
// осознанное упрощение для демо-стенда (один процесс admin-api, не
// кластер); для продакшена на несколько реплик счётчик стоит перенести
// в общее хранилище (Postgres/Redis), сама защита должна остаться.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

const (
	loginMaxAttempts = 5
	loginWindow      = 5 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string][]time.Time)}
}

func (limiter *loginLimiter) allow(username string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	cutoff := now.Add(-loginWindow)
	kept := limiter.attempts[username][:0]
	for _, attempt := range limiter.attempts[username] {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	limiter.attempts[username] = kept
	return len(kept) < loginMaxAttempts
}

func (limiter *loginLimiter) recordFailure(username string, now time.Time) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.attempts[username] = append(limiter.attempts[username], now)
}

func (limiter *loginLimiter) recordSuccess(username string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	delete(limiter.attempts, username)
}

func (manager *SessionManager) login(response http.ResponseWriter, request *http.Request, server *Server) {
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid login payload")
		return
	}
	now := manager.now()
	if !manager.limiter.allow(payload.Username, now) {
		server.auditLoginFailure(request.Context(), payload.Username)
		writeError(response, http.StatusTooManyRequests, "Слишком много неудачных попыток входа, попробуйте позже")
		return
	}
	authenticated, isAdmin := manager.authenticator.Authenticate(request.Context(), payload.Username, payload.Password)
	if !authenticated {
		manager.limiter.recordFailure(payload.Username, now)
		server.auditLoginFailure(request.Context(), payload.Username)
		writeError(response, http.StatusUnauthorized, "Неверный логин или пароль LDAP")
		return
	}
	manager.limiter.recordSuccess(payload.Username)
	expires := manager.now().Add(manager.lifetime)
	value, err := manager.sign(sessionPayload{Username: payload.Username, IsAdmin: isAdmin, Expires: expires.Unix()})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session unavailable")
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", Expires: expires,
		MaxAge: int(manager.lifetime.Seconds()), HttpOnly: true, Secure: manager.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(response, http.StatusOK, map[string]any{"username": payload.Username, "is_admin": isAdmin})
}

func (manager *SessionManager) logout(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", Expires: time.Unix(1, 0),
		MaxAge: -1, HttpOnly: true, Secure: manager.secureCookie, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (manager *SessionManager) currentUser(request *http.Request) (map[string]any, error) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return nil, errors.New("требуется вход")
	}
	payload, err := manager.verify(cookie.Value)
	if err != nil || payload.Expires <= manager.now().Unix() || payload.Username == "" {
		return nil, errors.New("требуется вход")
	}
	return map[string]any{"username": payload.Username, "is_admin": payload.IsAdmin, "guest": payload.Guest}, nil
}

// guestLogin — раздел 17-18 доп. ТЗ: отдельная кнопка на login page,
// не общий LDAP-логин. Сессия помечена Guest=true и получает
// фиксированную read-only роль (rbac.RoleGuest) без обращения к LDAP
// или platform_users вовсе. Короче обычной сессии (guestLifetime, не
// manager.lifetime) — гостевой доступ осознанно временный.
const guestLifetime = 8 * time.Hour

func (manager *SessionManager) guestLogin(response http.ResponseWriter, request *http.Request) {
	expires := manager.now().Add(guestLifetime)
	value, err := manager.sign(sessionPayload{Username: "guest", IsAdmin: false, Guest: true, Expires: expires.Unix()})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session unavailable")
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", Expires: expires,
		MaxAge: int(guestLifetime.Seconds()), HttpOnly: true, Secure: manager.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(response, http.StatusOK, map[string]any{"username": "guest", "is_admin": false, "guest": true})
}

func (manager *SessionManager) sign(payload sessionPayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(encoded)
	mac := hmac.New(sha256.New, manager.secret)
	_, _ = mac.Write([]byte(body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (manager *SessionManager) verify(value string) (sessionPayload, error) {
	body, signature, found := strings.Cut(value, ".")
	if !found {
		return sessionPayload{}, errors.New("invalid session")
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return sessionPayload{}, errors.New("invalid session")
	}
	mac := hmac.New(sha256.New, manager.secret)
	_, _ = mac.Write([]byte(body))
	expected := mac.Sum(nil)
	if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
		return sessionPayload{}, errors.New("invalid session")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return sessionPayload{}, errors.New("invalid session")
	}
	var payload sessionPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return sessionPayload{}, errors.New("invalid session")
	}
	return payload, nil
}
