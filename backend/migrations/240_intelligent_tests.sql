CREATE TABLE IF NOT EXISTS test_settings (
    test_type VARCHAR(64) PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT true,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO test_settings(test_type, enabled, config) VALUES
('candy', true, '{"prompt":"A box has 24 candies. Ming takes one quarter of all candies. Hong then takes half of the remaining candies. Ming puts 3 candies back. Give a short derivation and end with a separate line: ANSWER: number","model":"","evaluator":"exact_answer","expected_answer":"12","answer_type":"number","answer_format":"answer_line","timeout_seconds":180}'::jsonb),
('svg_structure', true, '{"prompt":"Return only one standalone valid SVG that draws a pelican riding a bicycle. Include two wheels, a frame, pelican body, long beak, and pedals. Use static SVG attributes only; no scripts, styles, external resources, foreignObject, or embedded images.","model":"","evaluator":"svg_structure","expected_answer":"","timeout_seconds":300}'::jsonb)
ON CONFLICT (test_type) DO NOTHING;

CREATE TABLE IF NOT EXISTS account_tests (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    test_type VARCHAR(64) NOT NULL REFERENCES test_settings(test_type) ON DELETE RESTRICT,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    score DOUBLE PRECISION CHECK (score IS NULL OR (score >= 0 AND score <= 100)),
    result TEXT NOT NULL DEFAULT '',
    result_image TEXT NOT NULL DEFAULT '',
    input TEXT NOT NULL DEFAULT '',
    raw_response TEXT NOT NULL DEFAULT '',
    raw_truncated BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    model VARCHAR(200) NOT NULL DEFAULT '',
    config_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    evaluation JSONB NOT NULL DEFAULT '{}'::jsonb,
    requested_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    lease_token VARCHAR(64),
    lease_until TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    queue_reason TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_tests_status_check CHECK (status IN ('queued','running','completed','success','failed','cancelled','rate_limited','account_error','model_error','request_error','network_error','suspected_degradation'))
);

CREATE INDEX IF NOT EXISTS idx_account_tests_account_history ON account_tests(account_id, test_type, id DESC);
CREATE INDEX IF NOT EXISTS idx_account_tests_created ON account_tests(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_account_tests_queue ON account_tests(status, available_at, id) WHERE status IN ('queued','running');
CREATE UNIQUE INDEX IF NOT EXISTS idx_account_tests_active ON account_tests(account_id, test_type) WHERE status IN ('queued','running');

CREATE TABLE IF NOT EXISTS intelligent_test_requests (
    actor_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_key VARCHAR(100) NOT NULL,
    fingerprint VARCHAR(64) NOT NULL,
    record_ids BIGINT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(actor_id, request_key)
);

CREATE INDEX IF NOT EXISTS idx_intelligent_test_requests_created ON intelligent_test_requests(created_at DESC);
