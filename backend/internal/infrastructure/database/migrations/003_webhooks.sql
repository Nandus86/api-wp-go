-- Registered webhooks, in addition to the legacy instances.webhook_url / WEBHOOK_URL.
-- instance_id NULL means a global webhook (receives events of every instance).
CREATE TABLE IF NOT EXISTS webhooks (
    id VARCHAR PRIMARY KEY,
    instance_id VARCHAR REFERENCES instances(id) ON DELETE CASCADE ON UPDATE CASCADE,
    url TEXT NOT NULL,
    -- Empty array = every event.
    events TEXT[] NOT NULL DEFAULT '{}',
    -- UAZAPI-style filters for "messages" events (fromMeYes, isGroupNo, wasSentByApi...).
    exclude_messages TEXT[] NOT NULL DEFAULT '{}',
    secret VARCHAR,
    headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhooks_instance ON webhooks (instance_id) WHERE enabled;

-- Outbox: one row per (event, target). The body is stored verbatim so the exact
-- bytes that were signed are the bytes that get delivered.
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id BIGSERIAL PRIMARY KEY,
    webhook_id VARCHAR,
    instance_id VARCHAR,
    event VARCHAR NOT NULL,
    url TEXT NOT NULL,
    body TEXT NOT NULL,
    status VARCHAR NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'delivering', 'retrying', 'delivered', 'failed')),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until TIMESTAMPTZ,
    enqueued_at TIMESTAMPTZ,
    last_status_code INT,
    last_error TEXT,
    duration_ms INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due ON webhook_deliveries (next_attempt_at)
    WHERE status IN ('pending', 'retrying', 'delivering');
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_instance ON webhook_deliveries (instance_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status_created ON webhook_deliveries (status, created_at);
