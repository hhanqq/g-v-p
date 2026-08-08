CREATE EXTENSION IF NOT EXISTS vector;

-- База знаний компании об управлении инцидентами (knowledge_base/*.md),
-- разбитая на чанки и проиндексированная локальными эмбеддингами
-- (go-platform/cmd/kb-index) для RAG в ИИ-разборе алерта по запросу
-- (internal/planner/ai_analysis.go). Датасет небольшой (десятки-сотни
-- строк) — ivfflat-индекс осознанно не заводится, последовательное
-- сравнение по <=> на таком объёме занимает доли миллисекунды.
CREATE TABLE IF NOT EXISTS kb_chunks (
    id SERIAL PRIMARY KEY,
    source VARCHAR(256) NOT NULL,
    title VARCHAR(256) NOT NULL,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    embedding vector(768) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS ix_kb_chunks_source ON kb_chunks(source);
