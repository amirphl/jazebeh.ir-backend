-- 0166 is already deployed. Keep durable staging rows in this new migration,
-- scoped to the lease attempt so a recovered worker cannot read or delete rows
-- created by the worker whose lease expired.
CREATE TABLE campaign_audience_report_job_rows (
 job_id UUID NOT NULL REFERENCES campaign_audience_report_jobs(id) ON DELETE CASCADE,
 lease_token UUID NOT NULL,
 campaign_id BIGINT NOT NULL REFERENCES campaigns(id),
 audience_uid TEXT NOT NULL,
 short_code TEXT NOT NULL,
 PRIMARY KEY (job_id, lease_token, campaign_id, audience_uid)
);
-- The primary key already covers report paging by (job_id, lease_token,
-- campaign_id, audience_uid). This separate index supports the campaign FK.
CREATE INDEX idx_campaign_audience_report_job_rows_campaign ON campaign_audience_report_job_rows (campaign_id);
CREATE INDEX idx_campaign_audience_report_jobs_terminal_purge ON campaign_audience_report_jobs (updated_at) WHERE status IN ('failed','expired');
