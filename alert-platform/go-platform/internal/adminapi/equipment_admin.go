package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

// Раздел «История изменений»: cmdb_objects раньше не имел ни одной
// create/update-ручки в Go admin API — записи создавал только
// scripts/seed_demo.py. Без этого редактировать карточку объекта
// (и, соответственно, писать её историю изменений) было нечем.

type equipmentCreateRequest struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Site          string  `json:"site"`
	Name          string  `json:"name"`
	FQDN          *string `json:"fqdn"`
	IP            *string `json:"ip"`
	Subnet        *string `json:"subnet"`
	EquipmentType *string `json:"equipment_type"`
	InstallDate   *string `json:"install_date"`
	SpecJSON      *string `json:"spec_json"`
}

func (server *Server) createEquipment(response http.ResponseWriter, request *http.Request, user map[string]any) {
	var payload equipmentCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid equipment payload")
		return
	}
	payload.ID = strings.TrimSpace(payload.ID)
	payload.Kind = strings.TrimSpace(payload.Kind)
	payload.Site = strings.TrimSpace(payload.Site)
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.ID == "" || payload.Kind == "" || payload.Site == "" || payload.Name == "" {
		writeError(response, http.StatusUnprocessableEntity, "id, kind, site и name обязательны")
		return
	}
	installDate, err := parseOptionalDate(payload.InstallDate)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid install_date, ожидается YYYY-MM-DD")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO cmdb_objects(id,kind,site,name,fqdn,ip,subnet,equipment_type,install_date,spec_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		payload.ID, payload.Kind, payload.Site, payload.Name, payload.FQDN, payload.IP, payload.Subnet,
		payload.EquipmentType, installDate, payload.SpecJSON)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(response, http.StatusConflict, "объект с таким id уже существует")
			return
		}
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	err = changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "cmdb_object.create",
		ResourceType: "cmdb_object", ResourceID: payload.ID,
		After: map[string]any{
			"kind": payload.Kind, "site": payload.Site, "name": payload.Name, "fqdn": payload.FQDN,
			"ip": payload.IP, "subnet": payload.Subnet, "equipment_type": payload.EquipmentType,
			"install_date": payload.InstallDate, "spec_json": payload.SpecJSON,
		},
	})
	if err != nil || tx.Commit(ctx) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "id": payload.ID})
}

type equipmentUpdateRequest struct {
	Name          *string `json:"name"`
	Site          *string `json:"site"`
	IP            *string `json:"ip"`
	FQDN          *string `json:"fqdn"`
	Subnet        *string `json:"subnet"`
	EquipmentType *string `json:"equipment_type"`
	InstallDate   *string `json:"install_date"`
	SpecJSON      *string `json:"spec_json"`
}

// updateEquipment следует конвенции NULLIF-по-пустой-строке, уже принятой
// в updateGroup: пустое или отсутствующее поле означает не менять
// значение, а не очистить его — та же семантика, что и у остального
// admin API, а не новая.
func (server *Server) updateEquipment(response http.ResponseWriter, request *http.Request, user map[string]any) {
	rawID := strings.TrimPrefix(normalizePath(request.URL.Path), "/api/equipment/")
	objectID, err := url.PathUnescape(rawID)
	if err != nil || objectID == "" {
		writeError(response, http.StatusBadRequest, "invalid object id")
		return
	}
	var payload equipmentUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid equipment payload")
		return
	}
	installDate, err := parseOptionalDate(payload.InstallDate)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid install_date, ожидается YYYY-MM-DD")
		return
	}
	actor, _ := user["username"].(string)
	now := time.Now().UTC()
	ctx := request.Context()
	tx, err := server.pool.Begin(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var beforeName, beforeSite string
	var beforeFQDN, beforeIP, beforeSubnet, beforeEquipmentType, beforeSpecJSON sql.NullString
	var beforeInstallDate sql.NullTime
	err = tx.QueryRow(ctx, `
		SELECT name,site,fqdn,ip,subnet,equipment_type,install_date,spec_json FROM cmdb_objects WHERE id=$1 FOR UPDATE`,
		objectID,
	).Scan(&beforeName, &beforeSite, &beforeFQDN, &beforeIP, &beforeSubnet, &beforeEquipmentType, &beforeInstallDate, &beforeSpecJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusNotFound, "Объект не найден")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var name, site string
	var fqdn, ip, subnet, equipmentType, specJSON sql.NullString
	var resultInstallDate sql.NullTime
	err = tx.QueryRow(ctx, `
		UPDATE cmdb_objects SET
			name=COALESCE(NULLIF($2,''),name), site=COALESCE(NULLIF($3,''),site),
			fqdn=COALESCE(NULLIF($4,''),fqdn), ip=COALESCE(NULLIF($5,''),ip),
			subnet=COALESCE(NULLIF($6,''),subnet), equipment_type=COALESCE(NULLIF($7,''),equipment_type),
			install_date=COALESCE($8,install_date), spec_json=COALESCE(NULLIF($9,''),spec_json)
		WHERE id=$1
		RETURNING name,site,fqdn,ip,subnet,equipment_type,install_date,spec_json`,
		objectID, valueOrEmpty(payload.Name), valueOrEmpty(payload.Site), valueOrEmpty(payload.FQDN),
		valueOrEmpty(payload.IP), valueOrEmpty(payload.Subnet), valueOrEmpty(payload.EquipmentType),
		installDate, valueOrEmpty(payload.SpecJSON),
	).Scan(&name, &site, &fqdn, &ip, &subnet, &equipmentType, &resultInstallDate, &specJSON)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	err = changelog.Record(ctx, tx, changelog.Event{
		OccurredAt: now, Actor: actor, ActorRole: changelog.RoleFromUser(user), Action: "cmdb_object.update",
		ResourceType: "cmdb_object", ResourceID: objectID,
		Before: map[string]any{
			"name": beforeName, "site": beforeSite, "fqdn": nullableString(beforeFQDN), "ip": nullableString(beforeIP),
			"subnet": nullableString(beforeSubnet), "equipment_type": nullableString(beforeEquipmentType),
			"install_date": nullableDate(beforeInstallDate), "spec_json": nullableString(beforeSpecJSON),
		},
		After: map[string]any{
			"name": name, "site": site, "fqdn": nullableString(fqdn), "ip": nullableString(ip),
			"subnet": nullableString(subnet), "equipment_type": nullableString(equipmentType),
			"install_date": nullableDate(resultInstallDate), "spec_json": nullableString(specJSON),
		},
	})
	if err != nil || tx.Commit(ctx) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func parseOptionalDate(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(*value))
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableDate(value sql.NullTime) *string {
	if !value.Valid {
		return nil
	}
	formatted := value.Time.Format("2006-01-02")
	return &formatted
}
