BEGIN;
DROP INDEX IF EXISTS idx_admin_short_link_upload_jobs_lease_expires_at;
DROP INDEX IF EXISTS uk_admin_short_link_upload_jobs_scenario_id;
ALTER TABLE admin_short_link_upload_jobs DROP CONSTRAINT IF EXISTS chk_admin_short_link_upload_job_status;
ALTER TABLE admin_short_link_upload_jobs
    ADD CONSTRAINT chk_admin_short_link_upload_job_status CHECK (status IN ('pending', 'processing', 'retrying', 'completed'));
ALTER TABLE admin_short_link_upload_jobs
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_expires_at;
COMMIT;
