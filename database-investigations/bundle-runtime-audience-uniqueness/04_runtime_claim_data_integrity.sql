-- Find malformed permanent allocations or active reservations in one bundle.
-- These rows are not included in the cross-campaign check when they lack a
-- campaign owner, so inspect and repair them before relying on that result.
-- Usage with psql: \set bundle_id 123

SELECT
    member.bundle_id,
    member.audience_id,
    'materialized_allocation'::text AS claim_source,
    member.id AS source_id,
    selection.id AS selection_id,
    selection.campaign_id,
    CASE
        WHEN selection.id IS NULL THEN 'missing_selection'
        WHEN selection.campaign_id IS NULL THEN 'selection_has_no_campaign_owner'
        WHEN campaign.id IS NULL THEN 'campaign_missing'
        WHEN campaign.bundle_id IS DISTINCT FROM member.bundle_id THEN 'campaign_in_different_bundle'
    END AS issue
FROM bundle_audience_selection_members AS member
LEFT JOIN bundle_audience_selections AS selection
  ON selection.id = member.selection_id
 AND selection.bundle_id = member.bundle_id
LEFT JOIN campaigns AS campaign ON campaign.id = selection.campaign_id
WHERE member.bundle_id = 347--:'bundle_id'::integer
  AND (
      selection.id IS NULL
      OR selection.campaign_id IS NULL
      OR campaign.id IS NULL
      OR campaign.bundle_id IS DISTINCT FROM member.bundle_id
  )

UNION ALL

SELECT
    reservation.bundle_id,
    reservation.audience_id,
    'active_test_reservation',
    reservation.id,
    reservation.selection_id,
    reservation.campaign_id,
    CASE
        WHEN campaign.id IS NULL THEN 'campaign_missing'
        WHEN campaign.bundle_id IS DISTINCT FROM reservation.bundle_id THEN 'campaign_in_different_bundle'
        WHEN selection.id IS NULL THEN 'selection_missing'
        WHEN selection.bundle_id <> reservation.bundle_id THEN 'selection_in_different_bundle'
        WHEN selection.campaign_id <> reservation.campaign_id THEN 'selection_owned_by_different_campaign'
    END
FROM campaign_targeting_test_sample_reservations AS reservation
LEFT JOIN campaigns AS campaign ON campaign.id = reservation.campaign_id
LEFT JOIN campaign_targeting_test_sample_selections AS selection
  ON selection.id = reservation.selection_id
WHERE reservation.bundle_id = 347--:'bundle_id'::integer
  AND reservation.state = 'active'
  AND (
      campaign.id IS NULL
      OR campaign.bundle_id IS DISTINCT FROM reservation.bundle_id
      OR selection.id IS NULL
      OR selection.bundle_id <> reservation.bundle_id
      OR selection.campaign_id <> reservation.campaign_id
  )

UNION ALL

SELECT
    reservation.bundle_id,
    reservation.audience_id,
    'active_execution_reservation',
    reservation.id,
    reservation.header_id,
    reservation.campaign_id,
    CASE
        WHEN campaign.id IS NULL THEN 'campaign_missing'
        WHEN campaign.bundle_id IS DISTINCT FROM reservation.bundle_id THEN 'campaign_in_different_bundle'
        WHEN header.id IS NULL THEN 'reservation_header_missing'
        WHEN header.bundle_id <> reservation.bundle_id THEN 'header_in_different_bundle'
        WHEN header.campaign_id <> reservation.campaign_id THEN 'header_owned_by_different_campaign'
    END
FROM campaign_targeting_execution_reservations AS reservation
LEFT JOIN campaigns AS campaign ON campaign.id = reservation.campaign_id
LEFT JOIN campaign_targeting_execution_reservation_headers AS header
  ON header.id = reservation.header_id
WHERE reservation.bundle_id = 347--:'bundle_id'::integer
  AND reservation.state = 'active'
  AND (
      campaign.id IS NULL
      OR campaign.bundle_id IS DISTINCT FROM reservation.bundle_id
      OR header.id IS NULL
      OR header.bundle_id <> reservation.bundle_id
      OR header.campaign_id <> reservation.campaign_id
  )
ORDER BY audience_id, claim_source, source_id;
