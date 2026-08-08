CREATE TABLE IF NOT EXISTS groups (
    id SERIAL PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    description TEXT,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS group_members (
    id SERIAL PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id),
    CONSTRAINT uq_group_member UNIQUE (group_id, subscriber_id)
);

-- NULL на оси = не сужает (тот же паттерн, что cmdb_ownership/subscriptions).
-- Хотя бы одна ось обязана быть задана, иначе строка ничего не значит.
CREATE TABLE IF NOT EXISTS group_equipment_scope (
    id SERIAL PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    object_id VARCHAR(256) REFERENCES cmdb_objects(id),
    equipment_type VARCHAR(64),
    site VARCHAR(64),
    CONSTRAINT ck_group_scope_not_empty CHECK (
        object_id IS NOT NULL OR equipment_type IS NOT NULL OR site IS NOT NULL
    )
);

CREATE INDEX IF NOT EXISTS ix_group_members_group_id ON group_members(group_id);
CREATE INDEX IF NOT EXISTS ix_group_members_subscriber_id ON group_members(subscriber_id);
CREATE INDEX IF NOT EXISTS ix_group_equipment_scope_group_id ON group_equipment_scope(group_id);
CREATE INDEX IF NOT EXISTS ix_group_equipment_scope_object_id ON group_equipment_scope(object_id);
