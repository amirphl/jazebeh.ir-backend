-- Find audiences claimed by multiple campaigns in one bundle at runtime.
-- Usage with psql: \set bundle_id 123

WITH runtime_claims AS (
    -- Permanent allocations after either test or execution materializes.
    SELECT
        member.bundle_id,
        member.audience_id,
        selection.campaign_id,
        'materialized_allocation'::text AS claim_source
    FROM bundle_audience_selection_members AS member
    JOIN bundle_audience_selections AS selection
      ON selection.id = member.selection_id
     AND selection.bundle_id = member.bundle_id
    WHERE member.bundle_id = 347--:'bundle_id'::integer
      AND selection.campaign_id IS NOT NULL

    UNION ALL

    -- Current test-phase holds.
    SELECT bundle_id, audience_id, campaign_id, 'active_test_reservation'
    FROM campaign_targeting_test_sample_reservations
    WHERE bundle_id = 347--:'bundle_id'::integer
      AND state = 'active'

    UNION ALL

    -- Current execution holds persisted by the modern reservation path.
    SELECT bundle_id, audience_id, campaign_id, 'active_execution_reservation'
    FROM campaign_targeting_execution_reservations
    WHERE bundle_id = 347--:'bundle_id'::integer
      AND state = 'active'

    UNION ALL

    -- The runtime also treats committed execution calculations as active
    -- claims for legacy/transition scheduling paths.
    SELECT calculation.bundle_id,
           member.audience_id,
           calculation.campaign_id,
           'committed_execution_calculation'
    FROM campaign_targeting_execution_calculations AS calculation
    JOIN campaign_targeting_execution_calculation_members AS member
      ON member.calculation_id = calculation.id
    WHERE calculation.bundle_id = 347--:'bundle_id'::integer
      AND calculation.status = 'committed'
)
SELECT
    bundle_id,
    audience_id,
    COUNT(DISTINCT campaign_id) AS claiming_campaign_count,
    ARRAY_AGG(DISTINCT campaign_id ORDER BY campaign_id) AS campaign_ids,
    ARRAY_AGG(DISTINCT claim_source ORDER BY claim_source) AS claim_sources
FROM runtime_claims
GROUP BY bundle_id, audience_id
HAVING COUNT(DISTINCT campaign_id) > 1
ORDER BY claiming_campaign_count DESC, audience_id;
