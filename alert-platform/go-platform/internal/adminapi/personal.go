package adminapi

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/changelog"
)

type cabinetSubscription struct {
	ID          int64
	Description string
}

type cabinetOption struct {
	Value string
	Label string
}

type cabinetView struct {
	Username      string
	Token         string
	Subscriptions []cabinetSubscription
	Subsidiaries  []string
	Services      []cabinetOption
	Priorities    []string
	Suggestion    *SubscriptionSuggestion
}

var cabinetTemplate = template.Must(template.New("cabinet").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Диспетчер — личный кабинет {{.Username}}</title>
<style>
body{font-family:system-ui,sans-serif;max-width:680px;margin:40px auto;padding:0 16px;background:#0f172a;color:#e2e8f0}
a{color:#60a5fa} ul{list-style:none;padding:0} li,.panel{margin-bottom:10px;padding:12px;border:1px solid #334155;border-radius:8px;background:#111c31}
button{padding:7px 13px;cursor:pointer;border:0;border-radius:6px;background:#2563eb;color:white} form.add{display:flex;flex-direction:column;gap:10px;max-width:360px}
select{padding:8px;border-radius:6px;background:#1e293b;color:#e2e8f0;border:1px solid #475569}.muted{color:#94a3b8;font-size:13px}
</style></head><body>
<p><a href="/console/app/">← консоль</a></p>
<h1>Личный кабинет: {{.Username}}</h1>
<p class="muted">Подписка сужает уведомления по филиалу, сервису и приоритету. Пустой фильтр означает «любой».</p>
<h3>Мои подписки</h3><ul>
{{range .Subscriptions}}<li>{{.Description}} <form method="post" action="unsubscribe/{{.ID}}?token={{$.Token}}" style="display:inline"><button type="submit">Отписаться</button></form></li>
{{else}}<li class="muted">Подписок нет — уведомления по этому логину приходить не будут.</li>{{end}}
</ul>
{{if .Suggestion}}<div class="panel">
<b>Рекомендация на основе истории</b><br>
<span class="muted">{{.Suggestion.Explanation}}</span>
<form method="post" action="subscribe?token={{.Token}}" style="margin-top:8px">
<input type="hidden" name="subsidiary" value="{{.Suggestion.SubsidiaryValue}}">
<input type="hidden" name="service_id" value="{{.Suggestion.ServiceIDValue}}">
<input type="hidden" name="priority_threshold" value="{{.Suggestion.PriorityValue}}">
<button type="submit">Подписаться по рекомендации</button>
</form></div>{{end}}
<h3>Добавить подписку</h3>
<form class="add panel" method="post" action="subscribe?token={{.Token}}">
<label>Филиал<br><select name="subsidiary"><option value="">— любой —</option>{{range .Subsidiaries}}<option value="{{.}}">{{.}}</option>{{end}}</select></label>
<label>Сервис<br><select name="service_id"><option value="">— любой —</option>{{range .Services}}<option value="{{.Value}}">{{.Label}}</option>{{end}}</select></label>
<label>Минимальный приоритет<br><select name="priority_threshold"><option value="">— любой —</option>{{range .Priorities}}<option value="{{.}}">{{.}} и критичнее</option>{{end}}</select></label>
<button type="submit">Подписаться</button>
</form></body></html>`))

func (server *Server) personalCabinet(response http.ResponseWriter, request *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "me" || parts[1] == "" {
		writeError(response, http.StatusNotFound, "route not found")
		return
	}
	username, err := url.PathUnescape(parts[1])
	if err != nil || strings.Contains(username, "/") {
		writeError(response, http.StatusBadRequest, "invalid username")
		return
	}
	if request.Method == http.MethodGet && len(parts) == 2 {
		server.renderPersonalCabinet(response, request, username)
		return
	}
	if request.Method == http.MethodPost && len(parts) == 3 && parts[2] == "subscribe" {
		server.subscribePersonal(response, request, username)
		return
	}
	if request.Method == http.MethodPost && len(parts) == 4 && parts[2] == "unsubscribe" {
		server.unsubscribePersonal(response, request, username, parts[3])
		return
	}
	writeError(response, http.StatusNotFound, "route not found")
}

func (server *Server) authorizedSubscriber(request *http.Request, username string) (int64, error) {
	token := request.URL.Query().Get("token")
	var id int64
	var stored string
	err := server.pool.QueryRow(request.Context(), `SELECT id,access_token FROM subscribers WHERE trueconf_username=$1`, username).Scan(&id, &stored)
	if err != nil {
		return 0, err
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(stored)) != 1 {
		return 0, errors.New("invalid token")
	}
	return id, nil
}

func (server *Server) renderPersonalCabinet(response http.ResponseWriter, request *http.Request, username string) {
	subscriberID, err := server.authorizedSubscriber(request, username)
	if err != nil {
		writeError(response, http.StatusForbidden, "Нет доступа. Получите персональную ссылку у бота: команда /кабинет.")
		return
	}
	serviceNames := make(map[string]string)
	services := make([]cabinetOption, 0)
	rows, err := server.pool.Query(request.Context(), `SELECT id,name FROM cmdb_services ORDER BY name`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan services")
			return
		}
		serviceNames[id] = name
		services = append(services, cabinetOption{Value: id, Label: name})
	}
	rows.Close()
	subsidiaries := make([]string, 0)
	rows, err = server.pool.Query(request.Context(), `SELECT DISTINCT subsidiary FROM cmdb_ownership WHERE subsidiary<>'' ORDER BY subsidiary`)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan subsidiaries")
			return
		}
		subsidiaries = append(subsidiaries, value)
	}
	rows.Close()
	subscriptions := make([]cabinetSubscription, 0)
	rows, err = server.pool.Query(request.Context(), `SELECT id,subsidiary,service_id,priority_threshold FROM subscriptions WHERE subscriber_id=$1 ORDER BY id`, subscriberID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var id int64
		var subsidiary, serviceID, priority sql.NullString
		if err := rows.Scan(&id, &subsidiary, &serviceID, &priority); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan subscriptions")
			return
		}
		parts := []string{"любой филиал", "любой сервис", "любой приоритет"}
		if subsidiary.Valid {
			parts[0] = "филиал: " + subsidiary.String
		}
		if serviceID.Valid {
			name := serviceNames[serviceID.String]
			if name == "" {
				name = serviceID.String
			}
			parts[1] = "сервис: " + name
		}
		if priority.Valid {
			parts[2] = "приоритет ≤ " + priority.String
		}
		subscriptions = append(subscriptions, cabinetSubscription{ID: id, Description: strings.Join(parts, ", ")})
	}
	rows.Close()
	var suggestion *SubscriptionSuggestion
	if len(subscriptions) == 0 {
		suggestion, _ = server.suggestSubscription(request.Context(), subscriberID)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = cabinetTemplate.Execute(response, cabinetView{
		Username: username, Token: url.QueryEscape(request.URL.Query().Get("token")), Subscriptions: subscriptions,
		Subsidiaries: subsidiaries, Services: services, Priorities: []string{"P0", "P1", "P2", "P3"},
		Suggestion: suggestion,
	})
}

func (server *Server) subscribePersonal(response http.ResponseWriter, request *http.Request, username string) {
	subscriberID, err := server.authorizedSubscriber(request, username)
	if err != nil {
		writeError(response, http.StatusForbidden, "Нет доступа")
		return
	}
	if err := request.ParseForm(); err != nil {
		writeError(response, http.StatusBadRequest, "invalid form")
		return
	}
	subsidiary := nullableForm(request.FormValue("subsidiary"))
	serviceID := nullableForm(request.FormValue("service_id"))
	priority := nullableForm(request.FormValue("priority_threshold"))
	if priority != nil && *priority != "P0" && *priority != "P1" && *priority != "P2" && *priority != "P3" {
		writeError(response, http.StatusUnprocessableEntity, "invalid priority")
		return
	}
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	now := time.Now().UTC()
	_, err = tx.Exec(request.Context(), `INSERT INTO subscriptions(subscriber_id,subsidiary,service_id,priority_threshold,created_at) VALUES($1,$2,$3,$4,$5)`, subscriberID, subsidiary, serviceID, priority, now)
	if err == nil {
		detail := "subsidiary=" + valueOrStar(subsidiary) + " service=" + valueOrStar(serviceID) + " priority<=" + valueOrStar(priority)
		_, err = tx.Exec(request.Context(), `INSERT INTO audit_log(actor,action,detail,created_at) VALUES($1,'subscribe',$2,$3)`, username, detail, now)
	}
	if err == nil {
		err = changelog.Record(request.Context(), tx, changelog.Event{
			OccurredAt: now, Actor: username, ActorRole: "user", Action: "subscription.create",
			ResourceType: "subscription", ResourceID: strconv.FormatInt(subscriberID, 10),
			After: map[string]any{"subsidiary": subsidiary, "service_id": serviceID, "priority_threshold": priority},
		})
	}
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	redirectCabinet(response, request, username)
}

func (server *Server) unsubscribePersonal(response http.ResponseWriter, request *http.Request, username, rawID string) {
	subscriberID, err := server.authorizedSubscriber(request, username)
	if err != nil {
		writeError(response, http.StatusForbidden, "Нет доступа")
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid subscription id")
		return
	}
	tx, err := server.pool.Begin(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer func() { _ = tx.Rollback(request.Context()) }()
	var subsidiary, serviceID sql.NullString
	err = tx.QueryRow(request.Context(), `DELETE FROM subscriptions WHERE id=$1 AND subscriber_id=$2 RETURNING subsidiary,service_id`, id, subscriberID).Scan(&subsidiary, &serviceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err == nil {
		now := time.Now().UTC()
		_, err = tx.Exec(request.Context(), `INSERT INTO audit_log(actor,action,detail,created_at) VALUES($1,'unsubscribe',$2,$3)`, username, "subsidiary="+nullOrStar(subsidiary)+" service="+nullOrStar(serviceID), now)
		if err == nil {
			err = changelog.Record(request.Context(), tx, changelog.Event{
				OccurredAt: now, Actor: username, ActorRole: "user", Action: "subscription.delete",
				ResourceType: "subscription", ResourceID: strconv.FormatInt(subscriberID, 10),
				Before: map[string]any{"subscription_id": id, "subsidiary": nullableString(subsidiary), "service_id": nullableString(serviceID)},
			})
		}
	}
	if err != nil || tx.Commit(request.Context()) != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	redirectCabinet(response, request, username)
}

func nullableForm(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func valueOrStar(value *string) string {
	if value == nil {
		return "*"
	}
	return *value
}

func nullOrStar(value sql.NullString) string {
	if !value.Valid {
		return "*"
	}
	return value.String
}

func redirectCabinet(response http.ResponseWriter, request *http.Request, username string) {
	target := "/console/me/" + url.PathEscape(username) + "/?token=" + url.QueryEscape(request.URL.Query().Get("token"))
	http.Redirect(response, request, target, http.StatusSeeOther)
}

func (server *Server) personalAlerts(response http.ResponseWriter, request *http.Request, path string) {
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, err := url.PathUnescape(strings.Trim(strings.TrimPrefix(path, "/alerts/"), "/"))
	if err != nil || username == "" || strings.Contains(username, "/") {
		writeError(response, http.StatusBadRequest, "invalid username")
		return
	}
	if _, err := server.authorizedSubscriber(request, username); err != nil {
		writeError(response, http.StatusForbidden, "Нет доступа")
		return
	}
	rows, err := server.pool.Query(request.Context(), `
		SELECT DISTINCT problem.id,problem.priority,COALESCE(object.name,problem.object_id,'неизвестный объект'),
		       problem.symptom_class,problem.opened_at,problem.status,problem.acknowledged_at,problem.acknowledged_by,
		       problem.incident_id
		FROM problems problem
		LEFT JOIN cmdb_objects object ON object.id=problem.object_id
		JOIN subscribers subscriber ON subscriber.trueconf_username=$1 AND subscriber.active=TRUE
		JOIN subscriptions subscription ON subscription.subscriber_id=subscriber.id
		WHERE problem.status IN ('OPEN','FLAPPING')
		  AND (subscription.subsidiary IS NULL OR EXISTS(
		      SELECT 1 FROM cmdb_ownership ownership
		      WHERE ownership.object_id=problem.object_id AND ownership.subsidiary=subscription.subsidiary
		      UNION ALL
		      SELECT 1 FROM cmdb_ownership ownership JOIN cmdb_service_objects service_object ON service_object.service_id=ownership.service_id
		      WHERE service_object.object_id=problem.object_id AND ownership.subsidiary=subscription.subsidiary))
		  AND (subscription.service_id IS NULL OR EXISTS(
		      SELECT 1 FROM cmdb_service_objects service_object
		      WHERE service_object.object_id=problem.object_id AND service_object.service_id=subscription.service_id))
		  AND (subscription.priority_threshold IS NULL OR problem.priority IS NULL OR problem.priority !~ '^P[0-9]+$'
		       OR SUBSTRING(problem.priority FROM 2)::INTEGER<=SUBSTRING(subscription.priority_threshold FROM 2)::INTEGER)
		ORDER BY problem.opened_at DESC`, username)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	var items strings.Builder
	for rows.Next() {
		var id int64
		var objectName, symptom, status string
		var priority sql.NullString
		var openedAt time.Time
		var acknowledgedAt sql.NullTime
		var acknowledgedBy sql.NullString
		var incidentID sql.NullInt64
		if err := rows.Scan(&id, &priority, &objectName, &symptom, &openedAt, &status, &acknowledgedAt, &acknowledgedBy, &incidentID); err != nil {
			writeError(response, http.StatusInternalServerError, "scan alerts")
			return
		}
		ack := ""
		if acknowledgedAt.Valid {
			ack = fmt.Sprintf(`<div class="muted">отреагировал: %s, %s</div>`, html.EscapeString(acknowledgedBy.String), acknowledgedAt.Time.Format("2006-01-02 15:04:05"))
		}
		fmt.Fprintf(&items, `<li><b>%s</b> · %s · %s<br><span class="muted">открыт %s · %s</span>%s</li>`, html.EscapeString(nullableStringValue(priority, "?")), html.EscapeString(objectName), html.EscapeString(symptom), openedAt.Format("2006-01-02 15:04:05"), html.EscapeString(status), ack)
	}
	if items.Len() == 0 {
		items.WriteString(`<li class="muted">Сейчас нет алертов, адресованных вам.</li>`)
	}
	token := url.QueryEscape(request.URL.Query().Get("token"))
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(response, `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Диспетчер — текущие алерты</title><style>body{font-family:system-ui,sans-serif;max-width:680px;margin:40px auto;padding:0 16px;background:#0f172a;color:#e2e8f0}a{color:#60a5fa}ul{list-style:none;padding:0}li{margin-bottom:10px;padding:12px;border:1px solid #334155;border-radius:8px;background:#111c31}.muted{color:#94a3b8;font-size:13px}</style></head><body><p><a href="/console/me/%s/?token=%s">← личный кабинет подписок</a></p><h1>Текущие алерты: %s</h1><p class="muted">Алерты, соответствующие вашим подпискам.</p><ul>%s</ul></body></html>`, url.PathEscape(username), token, html.EscapeString(username), items.String())
}
