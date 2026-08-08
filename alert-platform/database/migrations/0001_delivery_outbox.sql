CREATE TABLE IF NOT EXISTS delivery_outbox (
    id SERIAL PRIMARY KEY,
    notification_id INTEGER NOT NULL UNIQUE REFERENCES notifications(id),
    contract_version INTEGER NOT NULL DEFAULT 1,
    channel VARCHAR(32) NOT NULL DEFAULT 'trueconf',
    idempotency_key VARCHAR(256) NOT NULL UNIQUE,
    recipient VARCHAR(256),
    provider_chat_id VARCHAR(128),
    reply_to_notification_id INTEGER REFERENCES notifications(id),
    text TEXT NOT NULL,
    parse_mode VARCHAR(16) NOT NULL DEFAULT 'HTML',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMP NOT NULL,
    claimed_at TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL,
    sent_at TIMESTAMP,
    CONSTRAINT ck_delivery_outbox_target CHECK (
        recipient IS NOT NULL OR provider_chat_id IS NOT NULL
    )
);

CREATE INDEX IF NOT EXISTS ix_delivery_outbox_channel
    ON delivery_outbox(channel);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_status
    ON delivery_outbox(status);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_available_at
    ON delivery_outbox(available_at);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_notification_id
    ON delivery_outbox(notification_id);
