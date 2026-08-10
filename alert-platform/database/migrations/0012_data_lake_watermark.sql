-- Раздел «История изменений» — опциональная фаза Data Lake (MinIO).
-- Отдельная таблица-закладка, а не колонка на signals: signals — WORM
-- (раздел И3, приложение никогда не выполняет UPDATE/DELETE над ней),
-- поэтому прогресс архивации в Data Lake отслеживается снаружи, тем же
-- принципом "водяного знака", что уже используют signal_queue.status и
-- change_events.synced_at.
CREATE TABLE IF NOT EXISTS data_lake_watermark (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    last_signal_id INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITHOUT TIME ZONE,
    CONSTRAINT data_lake_watermark_singleton CHECK (id = 1)
);
INSERT INTO data_lake_watermark(id, last_signal_id) VALUES (1, 0) ON CONFLICT (id) DO NOTHING;
