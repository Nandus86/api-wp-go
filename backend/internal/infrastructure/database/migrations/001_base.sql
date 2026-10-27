-- Base schema. Written to be idempotent so it upgrades databases created by
-- older versions (which ran CREATE TABLE IF NOT EXISTS at boot).

CREATE TABLE IF NOT EXISTS instances (
    id VARCHAR PRIMARY KEY,
    name VARCHAR NOT NULL,
    jid VARCHAR,
    webhook_url VARCHAR,
    proxy_uri VARCHAR
);

ALTER TABLE instances ADD COLUMN IF NOT EXISTS webhook_url VARCHAR;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS proxy_uri VARCHAR;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS api_key VARCHAR;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE instances ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE instances ADD COLUMN IF NOT EXISTS paired_at TIMESTAMPTZ;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS last_connected_at TIMESTAMPTZ;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS last_disconnected_at TIMESTAMPTZ;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Instances that were already paired before this migration get a paired_at so
-- send pacing does not treat them as brand-new numbers.
UPDATE instances SET paired_at = NOW() - INTERVAL '365 days'
 WHERE paired_at IS NULL AND COALESCE(jid, '') <> '';

CREATE INDEX IF NOT EXISTS idx_instances_api_key ON instances (api_key);

CREATE TABLE IF NOT EXISTS message_stats (
    id SERIAL PRIMARY KEY,
    instance_id VARCHAR NOT NULL,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    direction VARCHAR NOT NULL,
    count INT DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_message_stats_ts ON message_stats (timestamp);
CREATE INDEX IF NOT EXISTS idx_message_stats_instance_ts ON message_stats (instance_id, timestamp);
