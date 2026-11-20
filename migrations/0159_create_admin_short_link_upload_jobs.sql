-- Durable CSV upload state. CSV data is retained only to resume the exact
-- allocation after an external redirect-service outage.
BEGIN;
CREATE TABLE IF NOT EXISTS admin_short_link_upload_jobs (
    id VARCHAR(36) PRIMARY KEY,
    allocation_key CHAR(64) NOT NULL UNIQUE,
    scenario_id BIGINT NOT NULL,
    scenario_name TEXT NOT NULL,
    domain VARCHAR(255) NOT NULL,
    csv_data BYTEA NOT NULL,
    status VARCHAR(20) NOT NULL,
    total_rows INTEGER NOT NULL DEFAULT 0,
    created INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    published INTEGER NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,
    last_error TEXT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_admin_short_link_upload_job_status CHECK (status IN ('pending','processing','retrying','completed'))
);
CREATE INDEX IF NOT EXISTS idx_admin_short_link_upload_jobs_due ON admin_short_link_upload_jobs (next_attempt_at) WHERE status IN ('pending','retrying');
COMMIT;
