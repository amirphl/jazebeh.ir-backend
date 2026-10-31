-- Support the bundle tag-list's scheduler-time usage marker. The index keeps
-- the EXISTS probe constrained by bundle and assigned tag, then supplies the
-- campaign join key without visiting unrelated attribution rows.

BEGIN;

CREATE INDEX IF NOT EXISTS idx_campaign_audience_tag_bundle_assigned_campaign
    ON campaign_audience_tag_attributions (bundle_id, assigned_tag_id, campaign_id);

COMMIT;
