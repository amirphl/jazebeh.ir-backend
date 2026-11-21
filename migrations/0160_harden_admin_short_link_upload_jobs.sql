-- Make upload-job state match the durable processing protocol.
BEGIN;

ALTER TABLE admin_short_link_upload_jobs
    ADD COLUMN IF NOT EXISTS lease_token VARCHAR(36),
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;

ALTER TABLE admin_short_link_upload_jobs
    DROP CONSTRAINT IF EXISTS chk_admin_short_link_upload_job_status;
ALTER TABLE admin_short_link_upload_jobs
    ADD CONSTRAINT chk_admin_short_link_upload_job_status
    CHECK (status IN ('pending', 'processing', 'retrying', 'completed', 'failed'));

-- A pre-lease deployment may have been interrupted while a job was marked
-- processing. Such rows have no owner and must become eligible again.
UPDATE admin_short_link_upload_jobs
SET status = 'retrying', next_attempt_at = CURRENT_TIMESTAMP
WHERE status = 'processing' AND lease_expires_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uk_admin_short_link_upload_jobs_scenario_id
    ON admin_short_link_upload_jobs (scenario_id);
CREATE INDEX IF NOT EXISTS idx_admin_short_link_upload_jobs_lease_expires_at
    ON admin_short_link_upload_jobs (lease_expires_at) WHERE status = 'processing';

-- Existing callers used MAX(scenario_id)+1 while explicitly supplying IDs,
-- which does not advance the sequence. Start the shared allocator above all
-- IDs already reserved in either table.
SELECT setval(
    'short_links_scenario_id_seq',
    GREATEST(
        COALESCE((SELECT MAX(scenario_id) FROM short_links), 0),
        COALESCE((SELECT MAX(scenario_id) FROM admin_short_link_upload_jobs), 0)
    ) + 1,
    false
);

COMMIT;
