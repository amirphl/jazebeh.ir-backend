-- Return one pass/fail row for runtime audience claims in one bundle.
-- Replace 347 to investigate another bundle.

WITH runtime_claims AS (
    SELECT member.audience_id, selection.campaign_id
    FROM bundle_audience_selection_members AS member
    JOIN bundle_audience_selections AS selection
      ON selection.id = member.selection_id
     AND selection.bundle_id = member.bundle_id
    WHERE member.bundle_id = 347 -- :'bundle_id'::integer
      AND selection.campaign_id IS NOT NULL

    UNION ALL

    SELECT audience_id, campaign_id
    FROM campaign_targeting_test_sample_reservations
    WHERE bundle_id = 347 -- :'bundle_id'::integer
      AND state = 'active'

    UNION ALL

    SELECT audience_id, campaign_id
    FROM campaign_targeting_execution_reservations
    WHERE bundle_id = 347 -- :'bundle_id'::integer
      AND state = 'active'

    UNION ALL

    SELECT member.audience_id, calculation.campaign_id
    FROM campaign_targeting_execution_calculations AS calculation
    JOIN campaign_targeting_execution_calculation_members AS member
      ON member.calculation_id = calculation.id
    WHERE calculation.bundle_id = 347 -- :'bundle_id'::integer
      AND calculation.status = 'committed'
), claim_counts AS (
    SELECT audience_id, COUNT(DISTINCT campaign_id) AS claiming_campaign_count
    FROM runtime_claims
    GROUP BY audience_id
), totals AS (
    SELECT
        (SELECT COUNT(*) FROM runtime_claims) AS runtime_claim_row_count,
        COUNT(*) AS claimed_audience_count,
        COUNT(*) FILTER (WHERE claiming_campaign_count > 1) AS duplicate_audience_count
    FROM claim_counts
)
SELECT
    347 -- :'bundle_id'::integer
        AS bundle_id,
    runtime_claim_row_count,
    claimed_audience_count,
    duplicate_audience_count,
    runtime_claim_row_count > 0 AS has_runtime_claims,
    CASE
        WHEN runtime_claim_row_count > 0 THEN duplicate_audience_count = 0
        ELSE NULL
    END AS all_runtime_audiences_are_unique
FROM totals;
