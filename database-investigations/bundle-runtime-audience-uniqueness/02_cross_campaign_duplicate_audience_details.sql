-- Show every runtime claim for audiences dedicated to multiple campaigns.
-- Usage with psql: \set bundle_id 123

WITH runtime_claims AS (
    SELECT
        member.bundle_id,
        member.audience_id,
        selection.campaign_id,
        'materialized_allocation'::text AS claim_source,
        selection.id AS source_id,
        member.created_at AS claimed_at,
        NULL::text AS claim_state
    FROM bundle_audience_selection_members AS member
    JOIN bundle_audience_selections AS selection
      ON selection.id = member.selection_id
     AND selection.bundle_id = member.bundle_id
    WHERE member.bundle_id = 347--:'bundle_id'::integer
      AND selection.campaign_id IS NOT NULL

    UNION ALL

    SELECT bundle_id, audience_id, campaign_id, 'active_test_reservation',
           id, created_at, state
    FROM campaign_targeting_test_sample_reservations
    WHERE bundle_id = 347--:'bundle_id'::integer AND state = 'active'

    UNION ALL

    SELECT bundle_id, audience_id, campaign_id, 'active_execution_reservation',
           id, created_at, state
    FROM campaign_targeting_execution_reservations
    WHERE bundle_id = 347--:'bundle_id'::integer AND state = 'active'

    UNION ALL

    SELECT calculation.bundle_id, member.audience_id, calculation.campaign_id,
           'committed_execution_calculation', calculation.id,
           calculation.committed_at, calculation.status
    FROM campaign_targeting_execution_calculations AS calculation
    JOIN campaign_targeting_execution_calculation_members AS member
      ON member.calculation_id = calculation.id
    WHERE calculation.bundle_id = 347--:'bundle_id'::integer
      AND calculation.status = 'committed'
), duplicate_audiences AS (
    SELECT audience_id
    FROM runtime_claims
    GROUP BY audience_id
    HAVING COUNT(DISTINCT campaign_id) > 1
)
SELECT
    claim.bundle_id,
    claim.audience_id,
    audience.uid AS audience_uid,
    claim.campaign_id,
    campaign.phase AS campaign_phase,
    campaign.status AS campaign_status,
    claim.claim_source,
    claim.source_id,
    claim.claim_state,
    claim.claimed_at
FROM runtime_claims AS claim
JOIN duplicate_audiences AS duplicate
  ON duplicate.audience_id = claim.audience_id
LEFT JOIN audience_profiles AS audience ON audience.id = claim.audience_id
LEFT JOIN campaigns AS campaign ON campaign.id = claim.campaign_id
ORDER BY claim.audience_id, claim.claimed_at NULLS LAST,
         claim.campaign_id, claim.claim_source;
