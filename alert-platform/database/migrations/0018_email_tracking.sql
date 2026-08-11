-- Раздел VI доп. ТЗ: Email analytics должна быть настоящей, не
-- придуманными процентами. Open/click различаются по типу и хранят
-- собственный случайный токен — редирект клика ВСЕГДА идёт на
-- target_url, сохранённый здесь на сервере в момент отправки письма,
-- никогда не берётся из query-параметра запроса (иначе — open redirect,
-- раздел VI.26 ТЗ прямо предупреждает об этом).
CREATE TABLE IF NOT EXISTS email_tracking_links (
    id SERIAL PRIMARY KEY,
    notification_id INTEGER NOT NULL REFERENCES notifications(id),
    kind VARCHAR(16) NOT NULL,
    token VARCHAR(64) NOT NULL UNIQUE,
    target_url TEXT,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    first_hit_at TIMESTAMP WITHOUT TIME ZONE,
    hit_count INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ck_email_tracking_kind CHECK (kind IN ('open', 'click')),
    CONSTRAINT ck_email_tracking_click_has_target CHECK (kind <> 'click' OR target_url IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS ix_email_tracking_links_notification ON email_tracking_links(notification_id);
