-- WhatsApp Business labels, kept in sync from app-state events (whatsmeow does not persist them).
CREATE TABLE IF NOT EXISTS labels (
    instance_id VARCHAR NOT NULL,
    id VARCHAR NOT NULL,
    name VARCHAR NOT NULL DEFAULT '',
    color INT NOT NULL DEFAULT 0,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, id)
);

CREATE TABLE IF NOT EXISTS chat_labels (
    instance_id VARCHAR NOT NULL,
    chat_jid VARCHAR NOT NULL,
    label_id VARCHAR NOT NULL,
    PRIMARY KEY (instance_id, chat_jid, label_id)
);
