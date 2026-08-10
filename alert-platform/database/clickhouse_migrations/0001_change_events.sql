-- Аналитическое хранилище истории изменений для low-code поиска
-- (раздел «История изменений»). Postgres change_events остаётся
-- источником правды и обслуживает быструю карту объекта/сотрудника —
-- эта таблица только для кросс-сущностного поиска по условиям.
-- Параллельная, не Python-migrate.py конвенция: применяется отдельным
-- one-shot Go-бинарём cmd/clickhouse-migrate, тот же паттерн, что у
-- migrate/kb-index — идемпотентно, IF NOT EXISTS.

CREATE DATABASE IF NOT EXISTS alerting;

CREATE TABLE IF NOT EXISTS alerting.change_events
(
    event_id      String,
    occurred_at   DateTime64(3),
    actor         String,
    actor_role    LowCardinality(String),
    action        LowCardinality(String),
    resource_type LowCardinality(String),
    resource_id   String,
    result        LowCardinality(String),
    detail        String,
    before_json   String,
    after_json    String
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (resource_type, occurred_at, resource_id, event_id);
