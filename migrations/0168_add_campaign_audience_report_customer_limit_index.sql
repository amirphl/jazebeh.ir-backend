-- The per-customer submission limit counts retained jobs before their output
-- expires. This index keeps that check bounded as report history grows.
CREATE INDEX idx_campaign_audience_report_jobs_customer_status
ON campaign_audience_report_jobs (customer_id, status);
