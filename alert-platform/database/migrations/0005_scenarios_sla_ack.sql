ALTER TABLE problems ADD COLUMN IF NOT EXISTS acknowledged_at TIMESTAMP WITHOUT TIME ZONE;
ALTER TABLE problems ADD COLUMN IF NOT EXISTS acknowledged_by VARCHAR(128);

CREATE TABLE IF NOT EXISTS scenario_runs (
    id SERIAL PRIMARY KEY,
    scenario_id INTEGER NOT NULL REFERENCES scenarios(id),
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    current_node_id VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'running',
    step_entered_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    notified_count INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT uq_scenario_run_scenario_problem UNIQUE (scenario_id, problem_id)
);

CREATE TABLE IF NOT EXISTS sla_breach_notices (
    id SERIAL PRIMARY KEY,
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    sla_rule_id INTEGER NOT NULL REFERENCES sla_rules(id),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    CONSTRAINT uq_sla_breach_notice_problem UNIQUE (problem_id)
);

CREATE INDEX IF NOT EXISTS ix_scenario_runs_scenario_id ON scenario_runs(scenario_id);
CREATE INDEX IF NOT EXISTS ix_scenario_runs_problem_id ON scenario_runs(problem_id);
CREATE INDEX IF NOT EXISTS ix_sla_breach_notices_problem_id ON sla_breach_notices(problem_id);
