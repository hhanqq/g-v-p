-- Раздел «ADP AI» доп. ТЗ. Два разных, не взаимозаменяемых механизма:
--
-- 1. change_events (существующий Global Audit, см. internal/changelog) —
--    расширен actor_type/initiated_by, чтобы действия, инициированные
--    ADP AI от имени пользователя, попадали в ОБЩУЮ историю изменений
--    рядом с обычными admin-мутациями (цепочка user→AI→tool→domain
--    action→result видна в одном месте), а не в параллельном учёте.
--    default 'user' — существующие вызовы changelog.Record не меняются.
--
-- 2. ai_journal — отдельная, специализированная таблица для экрана
--    «Журнал» ADP AI: детали, которых в change_events нет и не должно
--    быть (duration_ms, model, explanation, статус ИИ-запроса вроде
--    CONFIRMATION_REQUIRED/DENIED). Не дублирует change_events — пишется
--    для КАЖДОГО запроса к ассистенту (успешного и нет), change_events —
--    только для значимых доменных действий.
ALTER TABLE change_events ADD COLUMN IF NOT EXISTS actor_type VARCHAR(16) NOT NULL DEFAULT 'user';
ALTER TABLE change_events ADD COLUMN IF NOT EXISTS initiated_by VARCHAR(128);
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'ck_change_events_actor_type'
    ) THEN
        ALTER TABLE change_events ADD CONSTRAINT ck_change_events_actor_type CHECK (actor_type IN ('user', 'ai'));
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS ai_journal (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    user_id INTEGER REFERENCES platform_users(id),
    username VARCHAR(128) NOT NULL,
    session_id VARCHAR(64),
    request_text TEXT NOT NULL,
    action_type VARCHAR(16) NOT NULL,
    tool_name VARCHAR(64),
    resource_type VARCHAR(32),
    resource_id VARCHAR(256),
    input_parameters TEXT,
    result_summary TEXT,
    status VARCHAR(24) NOT NULL,
    duration_ms INTEGER,
    model VARCHAR(64),
    model_version VARCHAR(64),
    explanation TEXT,
    error_code VARCHAR(64),
    error_message TEXT,
    CONSTRAINT ck_ai_journal_action_type CHECK (action_type IN ('read', 'navigate', 'write')),
    CONSTRAINT ck_ai_journal_status CHECK (status IN ('SUCCESS', 'FAILED', 'DENIED', 'CONFIRMATION_REQUIRED', 'CANCELLED'))
);
CREATE INDEX IF NOT EXISTS ix_ai_journal_created_at ON ai_journal(created_at DESC);
CREATE INDEX IF NOT EXISTS ix_ai_journal_username ON ai_journal(username);
CREATE INDEX IF NOT EXISTS ix_ai_journal_status ON ai_journal(status);
