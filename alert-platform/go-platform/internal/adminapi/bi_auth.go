package adminapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/rbac"
)

// Раздел 40-46 доп. ТЗ: BI-интеграция не использует браузерную LDAP-
// сессию — отдельный service account с собственным токеном и своим
// scope (раздел 44: «один BI token получает вся компания, другой —
// только Ноябрьский филиал»). Токен хэшируется на сервере (SHA-256);
// сырой токен возвращается ровно один раз, в момент создания.

func generateBIToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "bi_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashBIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type biAccount struct {
	ID     int64
	Name   string
	Scopes []rbac.Scope
}

func (server *Server) authenticateBIToken(ctx context.Context, token string) (*biAccount, error) {
	hash := hashBIToken(token)
	var id int64
	var name string
	var active bool
	err := server.pool.QueryRow(ctx, `SELECT id, name, active FROM bi_service_accounts WHERE token_hash=$1`, hash).Scan(&id, &name, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("недействительный BI-токен")
	}
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, errors.New("service account деактивирован")
	}
	rows, err := server.pool.Query(ctx, `SELECT scope_type, scope_value FROM bi_service_account_scopes WHERE account_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := make([]rbac.Scope, 0)
	for rows.Next() {
		var t, v string
		if err := rows.Scan(&t, &v); err != nil {
			return nil, err
		}
		scopes = append(scopes, rbac.Scope{Type: rbac.ScopeType(t), Value: v})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// last_used_at — best-effort, не блокирует запрос при ошибке записи.
	_, _ = server.pool.Exec(ctx, `UPDATE bi_service_accounts SET last_used_at=$2 WHERE id=$1`, id, time.Now().UTC())
	return &biAccount{ID: id, Name: name, Scopes: scopes}, nil
}

type biAuthenticatedHandler func(http.ResponseWriter, *http.Request, *biAccount)

func (server *Server) withBIAuth(response http.ResponseWriter, request *http.Request, next biAuthenticatedHandler) {
	authHeader := request.Header.Get("Authorization")
	token, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok || token == "" {
		writeError(response, http.StatusUnauthorized, "требуется заголовок Authorization: Bearer <token>")
		return
	}
	account, err := server.authenticateBIToken(request.Context(), token)
	if err != nil {
		writeError(response, http.StatusUnauthorized, err.Error())
		return
	}
	next(response, request, account)
}

// biScopeCondition — то же самое разбиение прав, что и у обычных
// пользователей (rbac.Scope), применённое к BI-токену (раздел 44).
// objectColumn — доверенное имя колонки из фиксированного набора
// вызовов внутри пакета, никогда не строится из пользовательского ввода.
func biScopeCondition(scopes []rbac.Scope, objectColumn string, args *[]any) string {
	byType := map[rbac.ScopeType][]string{}
	for _, s := range scopes {
		byType[s.Type] = append(byType[s.Type], s.Value)
	}
	var parts []string
	add := func(template string, values []string) {
		if len(values) == 0 {
			return
		}
		*args = append(*args, values)
		parts = append(parts, fmt.Sprintf(template, len(*args)))
	}
	add(objectColumn+" IN (SELECT id FROM cmdb_objects WHERE site = ANY($%d))", byType[rbac.ScopeSite])
	add(objectColumn+" IN (SELECT object_id FROM cmdb_ownership WHERE subsidiary = ANY($%d))", byType[rbac.ScopeSubsidiary])
	add(objectColumn+" IN (SELECT object_id FROM cmdb_service_objects WHERE service_id = ANY($%d))", byType[rbac.ScopeService])
	add(objectColumn+" IN (SELECT id FROM cmdb_objects WHERE equipment_type = ANY($%d))", byType[rbac.ScopeEquipmentType])
	add(objectColumn+" = ANY($%d)", byType[rbac.ScopeObject])
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " AND ") + ")"
}

func parseBIRange(request *http.Request) (time.Time, time.Time, error) {
	query := request.URL.Query()
	now := time.Now().UTC()
	to := now
	from := now.AddDate(0, 0, -30)
	if v := query.Get("to"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid to (ожидается YYYY-MM-DD)")
		}
		to = parsed.Add(24 * time.Hour)
	}
	if v := query.Get("from"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid from (ожидается YYYY-MM-DD)")
		}
		from = parsed
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return from, to, nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 1000
	}
	if limit > 5000 {
		return 5000
	}
	return limit
}
