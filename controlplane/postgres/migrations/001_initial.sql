CREATE TABLE IF NOT EXISTS realy_nodes (
    id TEXT PRIMARY KEY,
    labels JSONB NOT NULL DEFAULT '{}'::jsonb,
    runtimes JSONB NOT NULL DEFAULT '[]'::jsonb,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    capacity INTEGER NOT NULL CHECK (capacity > 0),
    last_seen TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS realy_runs (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    runtime JSONB NOT NULL,
    source JSONB NOT NULL,
    request JSONB NOT NULL,
    status TEXT NOT NULL,
    result JSONB,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS realy_attempts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE,
    number INTEGER NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    status TEXT NOT NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS realy_events (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    type TEXT NOT NULL,
    data JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, sequence)
);

CREATE INDEX IF NOT EXISTS realy_runs_queue_idx
    ON realy_runs (created_at, id)
    WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS realy_attempts_node_active_idx
    ON realy_attempts (node_id, status)
    WHERE status IN ('leased', 'running');
