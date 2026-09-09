ALTER TABLE relay_runs ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE relay_runs ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE relay_runs DROP CONSTRAINT IF EXISTS relay_runs_idempotency_key_key;
ALTER TABLE relay_runs ADD CONSTRAINT relay_runs_scope_idempotency_key UNIQUE (tenant_id, project_id, idempotency_key);
