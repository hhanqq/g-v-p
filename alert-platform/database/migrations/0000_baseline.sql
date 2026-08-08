-- Полный baseline схемы платформы. Миграция намеренно идемпотентна:
-- она фиксирует существующую SQLAlchemy-схему как SQL-источник истины и
-- безопасно применяется как к чистой, так и к уже развёрнутой базе.

CREATE TABLE IF NOT EXISTS audit_log (
    id SERIAL PRIMARY KEY,
    actor VARCHAR(128) NOT NULL,
    action VARCHAR(64) NOT NULL,
    target VARCHAR(256),
    detail TEXT,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS cmdb_objects (
    id VARCHAR(256) PRIMARY KEY,
    kind VARCHAR(32) NOT NULL,
    site VARCHAR(64) NOT NULL,
    name VARCHAR(256) NOT NULL,
    fqdn VARCHAR(256),
    ip VARCHAR(64),
    inventory_id VARCHAR(64),
    subnet VARCHAR(64),
    parent_switch_id VARCHAR(256),
    equipment_type VARCHAR(64),
    install_date DATE,
    spec_json TEXT
);

CREATE TABLE IF NOT EXISTS cmdb_services (
    id VARCHAR(32) PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    criticality VARCHAR(32) NOT NULL
);

CREATE TABLE IF NOT EXISTS correlation_rules (
    id SERIAL PRIMARY KEY,
    rule_id VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(256) NOT NULL,
    trigger_symptom VARCHAR(64) NOT NULL,
    cause_symptom VARCHAR(64) NOT NULL,
    match_axes VARCHAR(128) NOT NULL,
    window_s INTEGER NOT NULL,
    status VARCHAR(16) NOT NULL,
    author VARCHAR(128),
    created_from VARCHAR(64)
);

-- problems и incidents ссылаются друг на друга. Сначала создаём problems
-- без incident_id FK, затем incidents и в конце добавляем обратную связь.
CREATE TABLE IF NOT EXISTS problems (
    id SERIAL PRIMARY KEY,
    dedup_key VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL,
    object_id VARCHAR(256),
    component VARCHAR(128),
    symptom_class VARCHAR(64) NOT NULL,
    site VARCHAR(64),
    opened_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    resolved_at TIMESTAMP WITHOUT TIME ZONE,
    last_seen_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    repeat_count INTEGER NOT NULL,
    toggle_count INTEGER NOT NULL,
    closed_by_reconciliation BOOLEAN NOT NULL,
    priority VARCHAR(8),
    priority_breakdown TEXT,
    incident_id INTEGER,
    duplicate_of_problem_id INTEGER REFERENCES problems(id),
    ai_root_cause_hypothesis TEXT,
    acknowledged_at TIMESTAMP WITHOUT TIME ZONE,
    acknowledged_by VARCHAR(128)
);

CREATE TABLE IF NOT EXISTS incidents (
    id SERIAL PRIMARY KEY,
    root_problem_id INTEGER NOT NULL REFERENCES problems(id),
    priority VARCHAR(8),
    opened_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    closed_at TIMESTAMP WITHOUT TIME ZONE
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'problems'::regclass
          AND conname = 'problems_incident_id_fkey'
    ) THEN
        ALTER TABLE problems
            ADD CONSTRAINT problems_incident_id_fkey
            FOREIGN KEY (incident_id) REFERENCES incidents(id);
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS scenarios (
    id SERIAL PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    description TEXT,
    graph_json TEXT NOT NULL,
    status VARCHAR(16) NOT NULL,
    created_by VARCHAR(128),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS scenario_runs (
    id SERIAL PRIMARY KEY,
    scenario_id INTEGER NOT NULL REFERENCES scenarios(id),
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    current_node_id VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL,
    step_entered_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    notified_count INTEGER NOT NULL,
    CONSTRAINT uq_scenario_run_scenario_problem UNIQUE (scenario_id, problem_id)
);

CREATE TABLE IF NOT EXISTS signals (
    id SERIAL PRIMARY KEY,
    source_system VARCHAR(64) NOT NULL,
    source_instance VARCHAR(128) NOT NULL,
    external_id VARCHAR(256),
    received_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    raw_body TEXT NOT NULL,
    hash VARCHAR(64) NOT NULL,
    CONSTRAINT uq_signal_dedup UNIQUE (source_instance, hash)
);

CREATE TABLE IF NOT EXISTS source_instances (
    id SERIAL PRIMARY KEY,
    instance VARCHAR(128) NOT NULL UNIQUE,
    system VARCHAR(64) NOT NULL,
    site VARCHAR(64) NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS subscribers (
    id SERIAL PRIMARY KEY,
    trueconf_username VARCHAR(128) NOT NULL UNIQUE,
    display_name VARCHAR(256),
    access_token VARCHAR(64) NOT NULL UNIQUE,
    active BOOLEAN NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    full_name VARCHAR(256),
    phone VARCHAR(32),
    email VARCHAR(256),
    position VARCHAR(128)
);

CREATE TABLE IF NOT EXISTS cmdb_aliases (
    id SERIAL PRIMARY KEY,
    site VARCHAR(64) NOT NULL,
    raw_name VARCHAR(256) NOT NULL,
    object_id VARCHAR(256) NOT NULL REFERENCES cmdb_objects(id),
    CONSTRAINT uq_alias_site_name UNIQUE (site, raw_name)
);

CREATE TABLE IF NOT EXISTS cmdb_ownership (
    id SERIAL PRIMARY KEY,
    object_id VARCHAR(256) REFERENCES cmdb_objects(id),
    service_id VARCHAR(32) REFERENCES cmdb_services(id),
    subsidiary VARCHAR(64) NOT NULL
);

CREATE TABLE IF NOT EXISTS cmdb_service_objects (
    id SERIAL PRIMARY KEY,
    service_id VARCHAR(32) NOT NULL REFERENCES cmdb_services(id),
    object_id VARCHAR(256) NOT NULL REFERENCES cmdb_objects(id)
);

CREATE TABLE IF NOT EXISTS employee_availability (
    id SERIAL PRIMARY KEY,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id),
    status VARCHAR(32) NOT NULL,
    valid_from TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    valid_until TIMESTAMP WITHOUT TIME ZONE,
    source VARCHAR(32) NOT NULL,
    note TEXT,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
    id SERIAL PRIMARY KEY,
    signal_id INTEGER NOT NULL REFERENCES signals(id),
    dedup_key VARCHAR(64),
    state VARCHAR(16) NOT NULL,
    occurred_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    ingest_ts TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    symptom_class VARCHAR(64) NOT NULL,
    severity_raw VARCHAR(64),
    title TEXT NOT NULL,
    site VARCHAR(64),
    object_name_raw VARCHAR(256),
    ip_raw VARCHAR(64),
    component VARCHAR(128),
    parser_version INTEGER,
    resolved BOOLEAN NOT NULL,
    object_id VARCHAR(256),
    resolution_method VARCHAR(32),
    resolution_confidence DOUBLE PRECISION,
    problem_id INTEGER REFERENCES problems(id),
    symptom_class_source VARCHAR(16)
);

CREATE TABLE IF NOT EXISTS incident_problems (
    id SERIAL PRIMARY KEY,
    incident_id INTEGER NOT NULL REFERENCES incidents(id),
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    role VARCHAR(16) NOT NULL,
    rule_id VARCHAR(64)
);

CREATE TABLE IF NOT EXISTS notifications (
    id SERIAL PRIMARY KEY,
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    type VARCHAR(16) NOT NULL,
    recipient VARCHAR(256),
    chat_id VARCHAR(128) NOT NULL,
    message_id VARCHAR(128),
    status VARCHAR(16) NOT NULL,
    error TEXT,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    sent_at TIMESTAMP WITHOUT TIME ZONE,
    ai_summary TEXT,
    ai_recommendation TEXT,
    CONSTRAINT uq_notification_problem_type_chat
        UNIQUE (problem_id, type, chat_id)
);

CREATE TABLE IF NOT EXISTS signal_queue (
    id SERIAL PRIMARY KEY,
    signal_id INTEGER NOT NULL REFERENCES signals(id),
    status VARCHAR(32) NOT NULL,
    error TEXT,
    attempts INTEGER NOT NULL,
    enqueued_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    processed_at TIMESTAMP WITHOUT TIME ZONE
);

CREATE TABLE IF NOT EXISTS sla_rules (
    id SERIAL PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    priority VARCHAR(8) NOT NULL,
    subsidiary VARCHAR(64),
    service_id VARCHAR(32) REFERENCES cmdb_services(id),
    response_minutes INTEGER NOT NULL,
    resolution_minutes INTEGER NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS sla_breach_notices (
    id SERIAL PRIMARY KEY,
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    sla_rule_id INTEGER NOT NULL REFERENCES sla_rules(id),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    CONSTRAINT uq_sla_breach_notice_problem UNIQUE (problem_id)
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id SERIAL PRIMARY KEY,
    subscriber_id INTEGER NOT NULL REFERENCES subscribers(id),
    subsidiary VARCHAR(64),
    service_id VARCHAR(32) REFERENCES cmdb_services(id),
    priority_threshold VARCHAR(8),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

-- Создаётся также в 0001 для совместимости с уже применённой историей.
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
    available_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    claimed_at TIMESTAMP WITHOUT TIME ZONE,
    last_error TEXT,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    sent_at TIMESTAMP WITHOUT TIME ZONE,
    CONSTRAINT ck_delivery_outbox_target CHECK (
        recipient IS NOT NULL OR provider_chat_id IS NOT NULL
    )
);

CREATE INDEX IF NOT EXISTS ix_audit_log_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS ix_audit_log_created_at ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS ix_cmdb_objects_site ON cmdb_objects(site);
CREATE INDEX IF NOT EXISTS ix_cmdb_objects_ip ON cmdb_objects(ip);
CREATE INDEX IF NOT EXISTS ix_cmdb_objects_subnet ON cmdb_objects(subnet);
CREATE INDEX IF NOT EXISTS ix_cmdb_objects_parent_switch_id ON cmdb_objects(parent_switch_id);
CREATE INDEX IF NOT EXISTS ix_cmdb_aliases_site ON cmdb_aliases(site);
CREATE INDEX IF NOT EXISTS ix_cmdb_aliases_raw_name ON cmdb_aliases(raw_name);
CREATE INDEX IF NOT EXISTS ix_employee_availability_subscriber_id
    ON employee_availability(subscriber_id);
CREATE INDEX IF NOT EXISTS ix_events_dedup_key ON events(dedup_key);
CREATE INDEX IF NOT EXISTS ix_events_object_id ON events(object_id);
CREATE INDEX IF NOT EXISTS ix_events_problem_id ON events(problem_id);
CREATE INDEX IF NOT EXISTS ix_notifications_problem_id ON notifications(problem_id);
CREATE INDEX IF NOT EXISTS ix_problems_dedup_key ON problems(dedup_key);
CREATE INDEX IF NOT EXISTS ix_problems_incident_id ON problems(incident_id);
CREATE INDEX IF NOT EXISTS ix_subscriptions_subscriber_id ON subscriptions(subscriber_id);
CREATE INDEX IF NOT EXISTS ix_scenario_runs_scenario_id ON scenario_runs(scenario_id);
CREATE INDEX IF NOT EXISTS ix_scenario_runs_problem_id ON scenario_runs(problem_id);
CREATE INDEX IF NOT EXISTS ix_sla_breach_notices_problem_id ON sla_breach_notices(problem_id);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_channel ON delivery_outbox(channel);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_status ON delivery_outbox(status);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_available_at ON delivery_outbox(available_at);
CREATE INDEX IF NOT EXISTS ix_delivery_outbox_notification_id ON delivery_outbox(notification_id);
