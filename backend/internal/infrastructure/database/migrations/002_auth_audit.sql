-- API keys: only a SHA-256 hash of each key is stored. The plaintext is shown once at creation.
CREATE TABLE IF NOT EXISTS api_keys (
    id VARCHAR PRIMARY KEY,
    name VARCHAR NOT NULL,
    key_prefix VARCHAR NOT NULL,
    key_hash VARCHAR NOT NULL UNIQUE,
    role VARCHAR NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    -- Empty array = every instance (including ones created later).
    allowed_instances TEXT[] NOT NULL DEFAULT '{}',
    created_by VARCHAR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_role ON api_keys (role) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_type VARCHAR NOT NULL,
    actor_id VARCHAR,
    actor_name VARCHAR,
    method VARCHAR NOT NULL,
    path VARCHAR NOT NULL,
    route VARCHAR,
    instance_id VARCHAR,
    status_code INT NOT NULL,
    ip VARCHAR,
    request_id VARCHAR,
    details JSONB
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_instance ON audit_logs (instance_id, created_at DESC);
