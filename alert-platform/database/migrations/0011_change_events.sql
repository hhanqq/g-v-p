-- История изменений: структурированный, аддитивный audit trail поверх
-- существующего тонкого audit_log (actor/action/target/detail, без
-- before/after). Не заменяет audit_log — /api/audit и Audit.tsx его
-- продолжают читать как раньше. change_events используется картой
-- объекта/сотрудника (быстрый прямой запрос по resource_type+
-- resource_id) и служит источником для relay в Kafka/ClickHouse
-- (низкоуровневый поиск по условиям, см. internal/changelog).
CREATE TABLE IF NOT EXISTS change_events (
    id SERIAL PRIMARY KEY,
    event_id UUID NOT NULL DEFAULT gen_random_uuid(),
    occurred_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    actor VARCHAR(128) NOT NULL,
    actor_role VARCHAR(32) NOT NULL,
    action VARCHAR(64) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    resource_id VARCHAR(256) NOT NULL,
    before_json TEXT,
    after_json TEXT,
    result VARCHAR(16) NOT NULL DEFAULT 'success',
    detail TEXT,
    synced_at TIMESTAMP WITHOUT TIME ZONE
);

CREATE INDEX IF NOT EXISTS ix_change_events_resource ON change_events(resource_type, resource_id, id DESC);
CREATE INDEX IF NOT EXISTS ix_change_events_unsynced ON change_events(id) WHERE synced_at IS NULL;
