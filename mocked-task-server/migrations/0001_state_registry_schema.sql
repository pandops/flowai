-- +goose Up
CREATE TABLE IF NOT EXISTS executor_records (
    executor_id          TEXT PRIMARY KEY,
    executor_type        TEXT NOT NULL,
    routing_target       TEXT NOT NULL,
    capacity             INTEGER NOT NULL,
    running_child_count  INTEGER NOT NULL DEFAULT 0,
    metadata             JSONB,
    registered_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS executor_events (
    event_id    UUID PRIMARY KEY,
    executor_id TEXT NOT NULL,
    type        TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload     JSONB
);

CREATE INDEX IF NOT EXISTS idx_executor_events_executor_id
    ON executor_events (executor_id, occurred_at);

CREATE TABLE IF NOT EXISTS task_events (
    event_id    UUID PRIMARY KEY,
    task_id     UUID NOT NULL,
    executor_id TEXT NOT NULL,
    source      TEXT NOT NULL,
    type        TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload     JSONB
);

CREATE INDEX IF NOT EXISTS idx_task_events_task_id
    ON task_events (task_id, occurred_at);

-- +goose Down
DROP TABLE IF EXISTS task_events;
DROP TABLE IF EXISTS executor_events;
DROP TABLE IF EXISTS executor_records;