-- Раздел 8-16 доп. ТЗ: доступ был плоским (LDAP-сессия + один бинарный
-- is_admin), теперь — role presets + индивидуальные grant/deny поверх
-- роли + scope данных. Роли и атомарные permissions — фиксированный
-- список в Go (internal/rbac), не отдельная таблица: набор ролей
-- закрыт (ТЗ явно перечисляет 7), а не редактируется администратором
-- произвольно. Индивидуальные исключения и scope — per-user, поэтому
-- живут в таблицах.
CREATE TABLE IF NOT EXISTS platform_users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(128) NOT NULL UNIQUE,
    role VARCHAR(32) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS user_permission_overrides (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES platform_users(id) ON DELETE CASCADE,
    permission VARCHAR(64) NOT NULL,
    effect VARCHAR(8) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    CONSTRAINT ck_user_permission_override_effect CHECK (effect IN ('grant', 'deny')),
    CONSTRAINT uq_user_permission_override UNIQUE (user_id, permission)
);

-- scope_type: site | subsidiary | service | equipment_type | object_id —
-- те же измерения, что уже существуют в cmdb_objects/cmdb_ownership/
-- cmdb_service_objects, не новая таксономия.
CREATE TABLE IF NOT EXISTS user_scopes (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES platform_users(id) ON DELETE CASCADE,
    scope_type VARCHAR(32) NOT NULL,
    scope_value VARCHAR(256) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    CONSTRAINT ck_user_scope_type CHECK (scope_type IN ('site', 'subsidiary', 'service', 'equipment_type', 'object_id')),
    CONSTRAINT uq_user_scope UNIQUE (user_id, scope_type, scope_value)
);
CREATE INDEX IF NOT EXISTS ix_user_scopes_user_id ON user_scopes(user_id);

-- BI service accounts (раздел 40-46 доп. ТЗ) — отдельная авторизация
-- токеном, не браузерная LDAP-сессия; scope переиспользует те же
-- user_scopes через platform_users-подобную запись без LDAP-идентичности.
CREATE TABLE IF NOT EXISTS bi_service_accounts (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL UNIQUE,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    token_prefix VARCHAR(12) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by VARCHAR(128),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    last_used_at TIMESTAMP WITHOUT TIME ZONE
);

CREATE TABLE IF NOT EXISTS bi_service_account_scopes (
    id SERIAL PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES bi_service_accounts(id) ON DELETE CASCADE,
    scope_type VARCHAR(32) NOT NULL,
    scope_value VARCHAR(256) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    CONSTRAINT ck_bi_scope_type CHECK (scope_type IN ('site', 'subsidiary', 'service', 'equipment_type', 'object_id'))
);
CREATE INDEX IF NOT EXISTS ix_bi_service_account_scopes_account ON bi_service_account_scopes(account_id);
