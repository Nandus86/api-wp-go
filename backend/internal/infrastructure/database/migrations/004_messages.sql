-- Message history (inbound and outbound). raw keeps the protobuf so media can be
-- downloaded later and messages can be quoted, edited or revoked by id.
CREATE TABLE IF NOT EXISTS messages (
    instance_id VARCHAR NOT NULL,
    id VARCHAR NOT NULL,
    chat_jid VARCHAR NOT NULL,
    sender_jid VARCHAR,
    from_me BOOLEAN NOT NULL DEFAULT FALSE,
    is_group BOOLEAN NOT NULL DEFAULT FALSE,
    type VARCHAR NOT NULL,
    text TEXT,
    push_name VARCHAR,
    content JSONB,
    raw BYTEA,
    media_key VARCHAR,
    media_mime VARCHAR,
    status VARCHAR NOT NULL DEFAULT 'received',
    sent_by_api BOOLEAN NOT NULL DEFAULT FALSE,
    job_id VARCHAR,
    edited BOOLEAN NOT NULL DEFAULT FALSE,
    timestamp TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, id)
);

CREATE INDEX IF NOT EXISTS idx_messages_chat ON messages (instance_id, chat_jid, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_messages_ts ON messages (timestamp);
CREATE INDEX IF NOT EXISTS idx_messages_instance_ts ON messages (instance_id, timestamp DESC);

-- Bulk campaigns ("folders" in UAZAPI's /sender API).
CREATE TABLE IF NOT EXISTS message_batches (
    id VARCHAR PRIMARY KEY,
    instance_id VARCHAR NOT NULL,
    info TEXT,
    status VARCHAR NOT NULL DEFAULT 'scheduled'
        CHECK (status IN ('scheduled', 'running', 'paused', 'done', 'cancelled')),
    total INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_message_batches_instance ON message_batches (instance_id, created_at DESC);

-- Every queued send is tracked here; RabbitMQ only carries it to a worker.
CREATE TABLE IF NOT EXISTS message_jobs (
    id VARCHAR PRIMARY KEY,
    instance_id VARCHAR NOT NULL,
    batch_id VARCHAR REFERENCES message_batches(id) ON DELETE CASCADE,
    queue VARCHAR NOT NULL,
    payload JSONB NOT NULL,
    recipient VARCHAR,
    status VARCHAR NOT NULL DEFAULT 'queued'
        CHECK (status IN ('scheduled', 'queued', 'sending', 'retrying', 'sent', 'failed', 'cancelled', 'paused')),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    enqueued_at TIMESTAMPTZ,
    locked_until TIMESTAMPTZ,
    wa_message_id VARCHAR,
    last_error TEXT,
    track_source VARCHAR,
    track_id VARCHAR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_message_jobs_due ON message_jobs (scheduled_at)
    WHERE status IN ('scheduled', 'queued', 'retrying', 'sending');
CREATE INDEX IF NOT EXISTS idx_message_jobs_instance ON message_jobs (instance_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_message_jobs_batch ON message_jobs (batch_id);
CREATE INDEX IF NOT EXISTS idx_message_jobs_track ON message_jobs (instance_id, track_source, track_id);
