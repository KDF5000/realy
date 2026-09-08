ALTER TABLE realy_runs ADD COLUMN IF NOT EXISTS session_id TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS realy_interactions (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    state TEXT NOT NULL,
    prompt TEXT NOT NULL,
    data JSONB,
    response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
