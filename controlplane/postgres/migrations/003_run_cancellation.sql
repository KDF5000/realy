ALTER TABLE relay_runs
    ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cancel_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cancel_requested_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS relay_runs_deadline_idx
    ON relay_runs (deadline_at)
    WHERE deadline_at IS NOT NULL AND status IN ('queued', 'running', 'cancelling');
