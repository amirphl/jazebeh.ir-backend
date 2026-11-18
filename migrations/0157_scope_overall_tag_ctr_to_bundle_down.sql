BEGIN;

ALTER TABLE tag_overall_performance_summaries
    DROP CONSTRAINT IF EXISTS tag_overall_performance_summaries_pkey;

DELETE FROM tag_overall_performance_summaries;

ALTER TABLE tag_overall_performance_summaries
    DROP COLUMN IF EXISTS bundle_id;

ALTER TABLE tag_overall_performance_summaries
    ADD PRIMARY KEY (tag_id);

INSERT INTO tag_overall_performance_summaries (
    tag_id,
    total_selected_count,
    total_sent_count,
    total_delivered_count,
    total_click_count,
    calculation_version
)
SELECT
    performance.tag_id,
    SUM(performance.selected_count),
    SUM(performance.sent_count),
    SUM(performance.delivered_count),
    SUM(performance.click_count),
    3
FROM campaign_tag_test_performances AS performance
GROUP BY performance.tag_id;

COMMIT;
