-- Overall Smart Targeting CTR is a Bundle/tag metric. Rebuild the former
-- global per-tag rows from the authoritative per-Campaign materialization.

BEGIN;

ALTER TABLE tag_overall_performance_summaries
    ADD COLUMN IF NOT EXISTS bundle_id INTEGER REFERENCES bundles(id) ON DELETE CASCADE;

ALTER TABLE tag_overall_performance_summaries
    DROP CONSTRAINT IF EXISTS tag_overall_performance_summaries_pkey;

DELETE FROM tag_overall_performance_summaries;

ALTER TABLE tag_overall_performance_summaries
    ALTER COLUMN bundle_id SET NOT NULL;

ALTER TABLE tag_overall_performance_summaries
    ADD PRIMARY KEY (bundle_id, tag_id);

INSERT INTO tag_overall_performance_summaries (
    bundle_id,
    tag_id,
    total_selected_count,
    total_sent_count,
    total_delivered_count,
    total_click_count,
    calculation_version
)
SELECT
    performance.bundle_id,
    performance.tag_id,
    SUM(performance.selected_count),
    SUM(performance.sent_count),
    SUM(performance.delivered_count),
    SUM(performance.click_count),
    3
FROM campaign_tag_test_performances AS performance
GROUP BY performance.bundle_id, performance.tag_id;

COMMIT;
