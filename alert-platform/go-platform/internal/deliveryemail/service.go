// Package deliveryemail — тонкий исходящий адаптер Email, второй канал
// доставки (раздел VII ТЗ). Знает только контракт delivery_outbox
// (channel='email') и SMTP — маршрутизацию, шаблоны, доменные решения
// принимает Go planner, этот пакет их не пересчитывает (тот же принцип
// границы, что у Python-адаптера TrueConf, services/delivery_trueconf/
// outbox.py — claim/retry-логика здесь зеркалирует его 1:1).
package deliveryemail

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html"
	"log"
	"net/smtp"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	FromAddress  string
	BatchSize    int
	MaxAttempts  int
	StuckTimeout time.Duration
	// TrackingBaseURL — публичный домен консоли (например
	// https://xn--80aebrvrg.xn--p1acf/console), на который указывают
	// пиксель открытия и click-редиректы в письме. Пусто — tracking
	// отключён, письмо уходит как plain text без пикселя/подмены ссылок
	// (честно: без публичного домена получатель не смог бы загрузить
	// пиксель или пройти по редиректу).
	TrackingBaseURL string
}

type Service struct {
	pool   *pgxpool.Pool
	config Config
}

func New(pool *pgxpool.Pool, config Config) *Service {
	if config.BatchSize <= 0 {
		config.BatchSize = 20
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 8
	}
	if config.StuckTimeout <= 0 {
		config.StuckTimeout = 120 * time.Second
	}
	return &Service{pool: pool, config: config}
}

type outboxCommand struct {
	id                    int64
	notificationID        int64
	recipient             string
	subject               *string
	bodyHTML              *string
	text                  string
	attempts              int
	replyToNotificationID *int64
}

// Tick — один проход: сначала возвращает зависшие "processing" по TTL
// (тот же паттерн, что и claim_batch в outbox.py — без фильтра по
// каналу, безвредно: строки другого канала эта же проверка вернула бы
// в pending, но их туда и так положил бы claim того канала), затем
// клеймит партию channel='email' через FOR UPDATE SKIP LOCKED и
// отправляет каждую письмом.
func (service *Service) Tick(ctx context.Context) (int, error) {
	if err := service.releaseStuck(ctx); err != nil {
		return 0, err
	}
	ids, err := service.claimBatch(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := service.deliverOne(ctx, id); err != nil {
			log.Printf("delivery-email: command=%d: %v", id, err)
		}
	}
	return len(ids), nil
}

func (service *Service) releaseStuck(ctx context.Context) error {
	_, err := service.pool.Exec(ctx, `
		UPDATE delivery_outbox SET status='pending', claimed_at=NULL
		WHERE status='processing' AND claimed_at < $1`,
		time.Now().UTC().Add(-service.config.StuckTimeout),
	)
	return err
}

func (service *Service) claimBatch(ctx context.Context) ([]int64, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	rows, err := tx.Query(ctx, `
		SELECT id FROM delivery_outbox
		WHERE channel='email' AND status='pending' AND available_at <= $1
		ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, service.config.BatchSize)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_outbox SET status='processing', claimed_at=$1, attempts=attempts+1
		WHERE id = ANY($2)`, now, ids); err != nil {
		return nil, err
	}
	return ids, tx.Commit(ctx)
}

func (service *Service) deliverOne(ctx context.Context, id int64) error {
	var command outboxCommand
	var subject, bodyHTML sql.NullString
	var replyTo sql.NullInt64
	err := service.pool.QueryRow(ctx, `
		SELECT id, notification_id, recipient, subject, body_html, text, attempts, reply_to_notification_id
		FROM delivery_outbox WHERE id=$1 AND status='processing'`, id,
	).Scan(&command.id, &command.notificationID, &command.recipient, &subject, &bodyHTML, &command.text, &command.attempts, &replyTo)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	if subject.Valid {
		command.subject = &subject.String
	}
	if bodyHTML.Valid {
		command.bodyHTML = &bodyHTML.String
	}
	if replyTo.Valid {
		command.replyToNotificationID = &replyTo.Int64
	}
	if command.recipient == "" {
		return service.retry(ctx, command, "email recipient is empty")
	}

	if service.config.TrackingBaseURL != "" {
		tracked, err := service.buildTrackedHTML(ctx, command)
		if err != nil {
			return err
		}
		command.bodyHTML = &tracked
	}

	sendErr := service.send(command)
	if sendErr != nil {
		return service.retry(ctx, command, sendErr.Error())
	}

	now := time.Now().UTC()
	_, err = service.pool.Exec(ctx, `
		UPDATE delivery_outbox SET status='sent', sent_at=$1, claimed_at=NULL, last_error=NULL WHERE id=$2`,
		now, command.id)
	if err != nil {
		return err
	}
	_, err = service.pool.Exec(ctx, `
		UPDATE notifications SET status='sent', sent_at=$1, error=NULL WHERE id=$2`,
		now, command.notificationID)
	return err
}

func (service *Service) send(command outboxCommand) error {
	subject := "ADP: уведомление"
	if command.subject != nil && *command.subject != "" {
		subject = *command.subject
	}
	body, contentType := command.text, "text/plain"
	if command.bodyHTML != nil && *command.bodyHTML != "" {
		body, contentType = *command.bodyHTML, "text/html"
	}
	var auth smtp.Auth
	if service.config.SMTPUsername != "" {
		auth = smtp.PlainAuth("", service.config.SMTPUsername, service.config.SMTPPassword, service.config.SMTPHost)
	}
	msg := buildMessage(service.config.FromAddress, command.recipient, subject, body, contentType)
	addr := fmt.Sprintf("%s:%s", service.config.SMTPHost, service.config.SMTPPort)
	return smtp.SendMail(addr, auth, service.config.FromAddress, []string{command.recipient}, []byte(msg))
}

func buildMessage(from, to, subject, body, contentType string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("From: %s\r\n", from))
	b.WriteString(fmt.Sprintf("To: %s\r\n", to))
	b.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(fmt.Sprintf("Content-Type: %s; charset=UTF-8\r\n\r\n", contentType))
	b.WriteString(body)
	return b.String()
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

func randomToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// buildTrackedHTML — раздел VI.25-26 ТЗ: open-пиксель + click-редирект
// через собственный подписанный токен, НЕ через destination в query
// (иначе open redirect). target_url для click хранится здесь, на
// сервере, в момент отправки — сам redirect-эндпоинт (admin-console)
// никогда не читает URL из запроса пользователя.
func (service *Service) buildTrackedHTML(ctx context.Context, command outboxCommand) (string, error) {
	text := command.text
	for _, rawURL := range uniqueStrings(urlPattern.FindAllString(text, -1)) {
		token, err := randomToken()
		if err != nil {
			return "", err
		}
		if _, err := service.pool.Exec(ctx, `
			INSERT INTO email_tracking_links(notification_id, kind, token, target_url, created_at)
			VALUES ($1, 'click', $2, $3, $4)`,
			command.notificationID, token, rawURL, time.Now().UTC()); err != nil {
			return "", err
		}
		clickURL := strings.TrimRight(service.config.TrackingBaseURL, "/") + "/email-track/click/" + token
		text = strings.ReplaceAll(text, rawURL, clickURL)
	}

	openToken, err := randomToken()
	if err != nil {
		return "", err
	}
	if _, err := service.pool.Exec(ctx, `
		INSERT INTO email_tracking_links(notification_id, kind, token, created_at)
		VALUES ($1, 'open', $2, $3)`,
		command.notificationID, openToken, time.Now().UTC()); err != nil {
		return "", err
	}
	openPixel := strings.TrimRight(service.config.TrackingBaseURL, "/") + "/email-track/open/" + openToken

	var body strings.Builder
	body.WriteString("<!doctype html><html><body style=\"font-family:sans-serif;white-space:pre-wrap\">")
	body.WriteString(html.EscapeString(text))
	body.WriteString(fmt.Sprintf(`<img src="%s" width="1" height="1" alt="" style="display:none">`, html.EscapeString(openPixel)))
	body.WriteString("</body></html>")
	return body.String(), nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

// retry — то же экспоненциальное окно, что и в outbox.py::_retry
// (min(2**attempts, 60) секунд), терминальный failed после MaxAttempts.
func (service *Service) retry(ctx context.Context, command outboxCommand, errText string) error {
	if len(errText) > 2000 {
		errText = errText[:2000]
	}
	if command.attempts >= service.config.MaxAttempts {
		_, err := service.pool.Exec(ctx, `
			UPDATE delivery_outbox SET status='failed', claimed_at=NULL, last_error=$1 WHERE id=$2`,
			errText, command.id)
		if err != nil {
			return err
		}
		_, err = service.pool.Exec(ctx, `UPDATE notifications SET status='failed', error=$1 WHERE id=$2`, errText, command.notificationID)
		return err
	}
	delay := time.Duration(minInt(1<<uint(command.attempts), 60)) * time.Second
	_, err := service.pool.Exec(ctx, `
		UPDATE delivery_outbox SET status='pending', claimed_at=NULL, last_error=$1, available_at=$2 WHERE id=$3`,
		errText, time.Now().UTC().Add(delay), command.id)
	if err != nil {
		return err
	}
	_, err = service.pool.Exec(ctx, `UPDATE notifications SET status='failed', error=$1 WHERE id=$2`, errText, command.notificationID)
	return err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
