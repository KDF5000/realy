ALTER TABLE relay_runs
    ADD COLUMN IF NOT EXISTS current_attempt_id TEXT;

UPDATE relay_runs r
SET current_attempt_id = a.id
FROM relay_attempts a
WHERE a.run_id = r.id AND r.current_attempt_id IS NULL;

ALTER TABLE relay_runs
    ALTER COLUMN current_attempt_id SET NOT NULL;

ALTER TABLE relay_attempts
    DROP CONSTRAINT IF EXISTS relay_attempts_run_id_key;

ALTER TABLE relay_attempts
    ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS relay_attempts_run_number_idx
    ON relay_attempts (run_id, number DESC);

CREATE INDEX IF NOT EXISTS relay_attempts_expired_running_idx
    ON relay_attempts (lease_expires_at)
    WHERE status = 'running';
