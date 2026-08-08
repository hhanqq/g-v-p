CREATE TABLE IF NOT EXISTS ai_analysis_requests (
    id SERIAL PRIMARY KEY,
    problem_id INTEGER NOT NULL REFERENCES problems(id),
    requested_by VARCHAR(128) NOT NULL,
    reply_to_notification_id INTEGER NOT NULL REFERENCES notifications(id),
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    processed_at TIMESTAMP WITHOUT TIME ZONE
);

CREATE INDEX IF NOT EXISTS ix_ai_analysis_requests_status ON ai_analysis_requests(status);
CREATE INDEX IF NOT EXISTS ix_ai_analysis_requests_problem_id ON ai_analysis_requests(problem_id);
