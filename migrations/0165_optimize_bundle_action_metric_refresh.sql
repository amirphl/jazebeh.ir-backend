-- The action-file metric refresh joins every immutable selected audience to
-- delivery history. These indexes match that join order and keep the lookup
-- selective for a single Bundle/campaign/selection.
--
-- This migration intentionally runs outside a transaction so index creation
-- does not block action-file uploads or message/status ingestion.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_bundle_audience_members_bundle_selection_audience
    ON bundle_audience_selection_members (bundle_id, selection_id, audience_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_campaign_audience_tag_bundle_campaign_audience
    ON campaign_audience_tag_attributions (bundle_id, campaign_id, audience_id)
    INCLUDE (assigned_tag_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_processed_campaigns_current_campaign_selection
    ON processed_campaigns (campaign_id, bundle_audience_selection_id)
    WHERE is_current AND bundle_audience_selection_id IS NOT NULL;

ANALYZE bundle_audience_selection_members;
ANALYZE campaign_audience_tag_attributions;
ANALYZE processed_campaigns;
