-- Версионирование сценариев + трасса исполнения прогонов. graph_json
-- сегодня перезаписывается на месте (updateScenario) без истории — у
-- прогона, стоящего в wait, нет способа узнать, по какому графу он
-- начал движение, если сценарий отредактировали посреди его жизни.
-- scenario_versions хранит снэпшот на каждую правку; scenario_runs
-- закрепляется за версией при создании и больше не читает "живой"
-- graph_json сценария для продолжения уже идущего прогона.
ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS scenario_versions (
    id SERIAL PRIMARY KEY,
    scenario_id INTEGER NOT NULL REFERENCES scenarios(id),
    version INTEGER NOT NULL,
    name VARCHAR(256) NOT NULL,
    description TEXT,
    graph_json TEXT NOT NULL,
    created_by VARCHAR(128),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    CONSTRAINT uq_scenario_version UNIQUE (scenario_id, version)
);

-- Бэкофилл: у каждого существующего сценария уже есть текущий
-- graph_json — фиксируем его как версию 1, иначе более старые прогоны
-- (scenario_version=1 по умолчанию ниже) не найдут свой снэпшот.
INSERT INTO scenario_versions(scenario_id, version, name, description, graph_json, created_by, created_at)
SELECT id, 1, name, description, graph_json, created_by, updated_at FROM scenarios
ON CONFLICT (scenario_id, version) DO NOTHING;

ALTER TABLE scenario_runs ADD COLUMN IF NOT EXISTS scenario_version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS scenario_run_steps (
    id BIGSERIAL PRIMARY KEY,
    run_id INTEGER NOT NULL REFERENCES scenario_runs(id) ON DELETE CASCADE,
    scenario_id INTEGER NOT NULL REFERENCES scenarios(id),
    scenario_version INTEGER NOT NULL,
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    node_id VARCHAR(64) NOT NULL,
    node_type VARCHAR(32) NOT NULL,
    branch VARCHAR(16),
    recipients_json TEXT,
    entered_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_scenario_run_steps_run_id ON scenario_run_steps(run_id, id);
CREATE INDEX IF NOT EXISTS ix_scenario_run_steps_scenario ON scenario_run_steps(scenario_id, scenario_version, node_id);
