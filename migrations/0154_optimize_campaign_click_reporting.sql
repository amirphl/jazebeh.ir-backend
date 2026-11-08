-- Serve bundle and campaign listings without repeatedly scanning the full
-- short-link click history. The reporting query groups distinct UID values by
-- campaign and filters non-test traffic plus bot metadata.
--
-- This migration intentionally runs outside a transaction: click ingestion is
-- latency-sensitive, so the index is built concurrently.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_short_link_clicks_campaign_uid_reporting
    ON short_link_clicks (campaign_id, uid)
    INCLUDE (ip, user_agent)
    WHERE campaign_id IS NOT NULL
      AND is_test IS NOT TRUE;

ANALYZE short_link_clicks;
