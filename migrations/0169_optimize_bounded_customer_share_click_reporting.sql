-- Customer-share reports require a bounded click period. This index supports
-- the per-page distinct-UID aggregation without scanning all click history.
-- It is intentionally concurrent because click ingestion is latency-sensitive.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_short_link_clicks_report_period_campaign_uid
    ON short_link_clicks (created_at, campaign_id, uid)
    INCLUDE (ip, user_agent)
    WHERE campaign_id IS NOT NULL
      AND is_test IS NOT TRUE;

ANALYZE short_link_clicks;
