package adminapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

// Раздел «История изменений»: subscribers раньше не имел ни одной
// create/update-ручки в Go admin API — только чтение. trueconf_username
// и access_token намеренно НЕ редактируемы: это ключи идентичности,
// на которые завязана маршрутизация подписок и личный кабинет в
// других местах — их правка вне объёма этой фичи.

type employeeCreateRequest struct {
	TrueconfUsername string  `json:"trueconf_username"`
	FullName         *string `json:"full_name"`
	Phone            *string `json:"phone"`
	Email            *string `json:"email"`
	Position         *string `json:"position"`
}

func generateAccessToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (server *Server) createEmployee(response http.ResponseWriter, request *http.Request, user map[string]any) {
	if !requireAdmin(response, user) {
		return
	}
	var payload employeeCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee payload")
		return
	}
	payload.TrueconfUsername = strings.TrimSpace(payload.TrueconfUsername)
	if payload.TrueconfUsername == "" {
		writeError(response, http.StatusUnprocessableEntity, "trueconf_username is required")
		return
	}
	token, err := generateAccessToken()
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "token generation failed")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	var id int64
	err = tx.QueryRow(request.Context(), `
		INSERT INTO subscribers(trueconf_username,full_name,phone,email,position,access_token,active,created_at)
		VALUES($1,$2,$3,$4,$5,$6,true,$7) RETURNING id`,
		payload.TrueconfUsername, payload.FullName, payload.Phone, payload.Email, payload.Position, token, now,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(response, http.StatusConflict, "сотрудник с таким trueconf_username уже существует")
			return
		}
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	err = changelog.Record(request.Context(), tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "subscriber.create",
		ResourceType: "subscriber", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{
			"trueconf_username": payload.TrueconfUsername, "full_name": payload.FullName,
			"phone": payload.Phone, "email": payload.Email, "position": payload.Position,
		},
	})
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": id})
}

type employeeUpdateRequest struct {
	FullName *string `json:"full_name"`
	Phone    *string `json:"phone"`
	Email    *string `json:"email"`
	Position *string `json:"position"`
	Active   *bool   `json:"active"`
}

// updateEmployee — та же конвенция NULLIF-по-пустой-строке, что у
// updateEquipment/updateGroup: пустое или отсутствующее поле не меняет
// значение. Active — отдельно, т.к. это bool, а не text.
func (server *Server) updateEmployee(response http.ResponseWriter, request *http.Request, user map[string]any) {
	if !requireAdmin(response, user) {
		return
	}
	id, ok := pathInt(normalizePath(request.URL.Path), "/api/employees/")
	if !ok {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee id")
		return
	}
	var payload employeeUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid employee payload")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()

	var beforeFullName, beforePhone, beforeEmail, beforePosition sql.NullString
	var beforeActive bool
	err = tx.QueryRow(request.Context(), `SELECT full_name,phone,email,position,active FROM subscribers WHERE id=$1 FOR UPDATE`, id).
		Scan(&beforeFullName, &beforePhone, &beforeEmail, &beforePosition, &beforeActive)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Сотрудник не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	active := beforeActive
	if payload.Active != nil {
		active = *payload.Active
	}

	var fullName, phone, email, position sql.NullString
	var resultActive bool
	err = tx.QueryRow(request.Context(), `
		UPDATE subscribers SET
			full_name=COALESCE(NULLIF($2,''),full_name), phone=COALESCE(NULLIF($3,''),phone),
			email=COALESCE(NULLIF($4,''),email), position=COALESCE(NULLIF($5,''),position), active=$6
		WHERE id=$1
		RETURNING full_name,phone,email,position,active`,
		id, valueOrEmpty(payload.FullName), valueOrEmpty(payload.Phone), valueOrEmpty(payload.Email),
		valueOrEmpty(payload.Position), active,
	).Scan(&fullName, &phone, &email, &position, &resultActive)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	err = changelog.Record(request.Context(), tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "subscriber.update",
		ResourceType: "subscriber", ResourceID: strconv.FormatInt(id, 10),
		Before: map[string]any{
			"full_name": nullableString(beforeFullName), "phone": nullableString(beforePhone),
			"email": nullableString(beforeEmail), "position": nullableString(beforePosition), "active": beforeActive,
		},
		After: map[string]any{
			"full_name": nullableString(fullName), "phone": nullableString(phone),
			"email": nullableString(email), "position": nullableString(position), "active": resultActive,
		},
	})
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}
