-- Show sent campaigns and the runtime claim records available for each.
-- The bundle and its processed campaigns are materialized first so PostgreSQL
-- can use the sent-table processed_campaign_id indexes instead of scanning all
-- historical sent rows. Excel-targeted campaigns intentionally have no bundle
-- allocation claim.

WITH bundle_campaigns AS MATERIALIZED (
    SELECT id, phase, status, spec
    FROM campaigns
    WHERE bundle_id = 347 -- :'bundle_id'::integer
), bundle_processed_campaigns AS MATERIALIZED (
    SELECT processed.id, processed.campaign_id
    FROM processed_campaigns AS processed
    JOIN bundle_campaigns AS campaign ON campaign.id = processed.campaign_id
), sent_campaigns AS (
    SELECT processed.campaign_id, COUNT(*) AS sent_message_count
    FROM bundle_processed_campaigns AS processed
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id

    UNION ALL

    SELECT processed.campaign_id, COUNT(*)
    FROM bundle_processed_campaigns AS processed
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id

    UNION ALL

    SELECT processed.campaign_id, COUNT(*)
    FROM bundle_processed_campaigns AS processed
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id

    UNION ALL

    SELECT processed.campaign_id, COUNT(*)
    FROM bundle_processed_campaigns AS processed
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id
), sent_counts AS (
    SELECT campaign_id, SUM(sent_message_count) AS sent_message_count
    FROM sent_campaigns
    GROUP BY campaign_id
), materialized_claims AS (
    SELECT selection.campaign_id, COUNT(*) AS materialized_audience_count
    FROM bundle_audience_selections AS selection
    JOIN bundle_audience_selection_members AS member
      ON member.selection_id = selection.id
     AND member.bundle_id = selection.bundle_id
    WHERE selection.bundle_id = 347 -- :'bundle_id'::integer
    GROUP BY selection.campaign_id
), active_test_claims AS (
    SELECT reservation.campaign_id, COUNT(*) AS active_test_audience_count
    FROM campaign_targeting_test_sample_reservations AS reservation
    WHERE reservation.bundle_id = 347 -- :'bundle_id'::integer
      AND reservation.state = 'active'
    GROUP BY reservation.campaign_id
), active_execution_claims AS (
    SELECT reservation.campaign_id, COUNT(*) AS active_execution_audience_count
    FROM campaign_targeting_execution_reservations AS reservation
    WHERE reservation.bundle_id = 347 -- :'bundle_id'::integer
      AND reservation.state = 'active'
    GROUP BY reservation.campaign_id
), committed_execution_claims AS (
    SELECT calculation.campaign_id, COUNT(*) AS committed_execution_audience_count
    FROM campaign_targeting_execution_calculations AS calculation
    JOIN campaign_targeting_execution_calculation_members AS member
      ON member.calculation_id = calculation.id
    WHERE calculation.bundle_id = 347 -- :'bundle_id'::integer
      AND calculation.status = 'committed'
    GROUP BY calculation.campaign_id
)
SELECT
    campaign.id AS campaign_id,
    campaign.phase,
    campaign.status,
    CASE
        WHEN lower(btrim(coalesce(campaign.spec->>'audience_targeting_method', '')))
             IN ('standard', 'smart_targeting', 'excel')
            THEN lower(btrim(campaign.spec->>'audience_targeting_method'))
        WHEN nullif(btrim(campaign.spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
            THEN 'excel'
        ELSE 'standard'
    END AS targeting_method,
    sent.sent_message_count,
    COALESCE(materialized.materialized_audience_count, 0) AS materialized_audience_count,
    COALESCE(test_claim.active_test_audience_count, 0) AS active_test_audience_count,
    COALESCE(execution_claim.active_execution_audience_count, 0) AS active_execution_audience_count,
    COALESCE(committed_claim.committed_execution_audience_count, 0) AS committed_execution_audience_count
FROM sent_counts AS sent
JOIN bundle_campaigns AS campaign ON campaign.id = sent.campaign_id
LEFT JOIN materialized_claims AS materialized ON materialized.campaign_id = campaign.id
LEFT JOIN active_test_claims AS test_claim ON test_claim.campaign_id = campaign.id
LEFT JOIN active_execution_claims AS execution_claim ON execution_claim.campaign_id = campaign.id
LEFT JOIN committed_execution_claims AS committed_claim ON committed_claim.campaign_id = campaign.id
ORDER BY targeting_method, campaign.phase, campaign.id;
