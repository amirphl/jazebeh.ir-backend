CREATE TABLE campaign_audience_report_jobs (
 id UUID PRIMARY KEY, customer_id BIGINT NOT NULL REFERENCES customers(id), campaign_ids BIGINT[] NOT NULL,
 status VARCHAR(16) NOT NULL, row_count BIGINT NOT NULL DEFAULT 0, sheet_count INTEGER NOT NULL DEFAULT 0, byte_size BIGINT NOT NULL DEFAULT 0,
 output_path TEXT, error_code VARCHAR(64), error_message TEXT, attempts INTEGER NOT NULL DEFAULT 0, lease_token UUID, lease_expires_at TIMESTAMPTZ,
 started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, expires_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CONSTRAINT chk_campaign_audience_report_job_status CHECK (status IN ('pending','processing','completed','failed','expired'))
);
CREATE INDEX idx_campaign_audience_report_jobs_due ON campaign_audience_report_jobs (created_at) WHERE status IN ('pending','processing');
CREATE INDEX idx_campaign_audience_report_jobs_expiry ON campaign_audience_report_jobs (expires_at) WHERE status='completed';
