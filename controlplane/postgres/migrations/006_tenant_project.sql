ALTER TABLE realy_runs ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE realy_runs ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE realy_runs DROP CONSTRAINT IF EXISTS realy_runs_idempotency_key_key;
ALTER TABLE realy_runs ADD CONSTRAINT realy_runs_scope_idempotency_key UNIQUE (tenant_id, project_id, idempotency_key);
