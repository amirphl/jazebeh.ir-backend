-- Row-per-attempt audit report for every duplicate sent audience in a bundle.
-- One audience/phone appears once for each message attempt, so filter by
-- audience_id or normalized_phone_number to investigate a specific customer.

WITH bundle_campaigns AS MATERIALIZED (
    SELECT id, phase, status, spec
    FROM campaigns
    WHERE bundle_id = 347 -- :'bundle_id'::integer
), sent_messages AS MATERIALIZED (
    SELECT
        campaign.id AS campaign_id,
        campaign.phase AS campaign_phase,
        campaign.status AS campaign_status,
        campaign.spec,
        processed.id AS processed_campaign_id,
        processed.is_current AS processed_campaign_is_current,
        processed.bundle_audience_selection_id,
        'sms'::text AS platform,
        sent.id AS sent_message_id,
        sent.tracking_id::text AS tracking_id,
        sent.status::text AS send_status,
        sent.parts_delivered,
        sent.phone_number,
        sent.created_at AS sent_at,
        sent.updated_at AS sent_updated_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec,
           processed.id, processed.is_current, processed.bundle_audience_selection_id,
           'bale', sent.id, sent.tracking_id, sent.status::text, sent.parts_delivered,
           sent.phone_number, sent.created_at, sent.updated_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec,
           processed.id, processed.is_current, processed.bundle_audience_selection_id,
           'rubika', sent.id, sent.tracking_id, sent.status::text, sent.parts_delivered,
           sent.phone_number, sent.created_at, sent.updated_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec,
           processed.id, processed.is_current, processed.bundle_audience_selection_id,
           'splus', sent.id, sent.tracking_id, sent.status::text, sent.parts_delivered,
           sent.phone_number, sent.created_at, sent.updated_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
), normalized_messages AS (
    SELECT
        message.*,
        regexp_replace(btrim(message.phone_number), '[^0-9]', '', 'g')
            AS normalized_phone_number
    FROM sent_messages AS message
    WHERE nullif(btrim(message.phone_number), '') IS NOT NULL
), duplicate_audiences AS (
    SELECT
        normalized_phone_number,
        COUNT(*) AS duplicate_sent_message_count,
        COUNT(DISTINCT campaign_id) AS duplicate_campaign_count,
        COUNT(DISTINCT processed_campaign_id) AS duplicate_processed_campaign_count,
        MIN(sent_at) AS first_sent_at,
        MAX(sent_at) AS last_sent_at
    FROM normalized_messages
    WHERE normalized_phone_number <> ''
    GROUP BY normalized_phone_number
    HAVING COUNT(*) > 1
), duplicate_attempts AS (
    SELECT
        message.*,
        duplicate.duplicate_sent_message_count,
        duplicate.duplicate_campaign_count,
        duplicate.duplicate_processed_campaign_count,
        duplicate.first_sent_at,
        duplicate.last_sent_at,
        ROW_NUMBER() OVER (
            PARTITION BY message.normalized_phone_number
            ORDER BY message.sent_at, message.campaign_id, message.sent_message_id
        ) AS attempt_number
    FROM normalized_messages AS message
    JOIN duplicate_audiences AS duplicate
      ON duplicate.normalized_phone_number = message.normalized_phone_number
)
SELECT
    347 -- :'bundle_id'::integer
        AS bundle_id,
    audience.id AS audience_id,
    audience.uid AS audience_uid,
    attempt.normalized_phone_number,
    attempt.phone_number AS stored_phone_number,
    attempt.duplicate_sent_message_count,
    attempt.duplicate_campaign_count,
    attempt.duplicate_processed_campaign_count,
    attempt.first_sent_at,
    attempt.last_sent_at,
    attempt.attempt_number,
    attempt.campaign_id,
    attempt.campaign_phase,
    attempt.campaign_status,
    attempt.spec->>'title' AS campaign_title,
    CASE
        WHEN lower(btrim(coalesce(attempt.spec->>'audience_targeting_method', '')))
             IN ('standard', 'smart_targeting', 'excel')
            THEN lower(btrim(attempt.spec->>'audience_targeting_method'))
        WHEN nullif(btrim(attempt.spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
            THEN 'excel'
        ELSE 'standard'
    END AS targeting_method,
    attempt.spec->>'target_audience_excel_file_uuid' AS excel_file_uuid,
    attempt.processed_campaign_id,
    attempt.processed_campaign_is_current,
    attempt.bundle_audience_selection_id AS processed_bundle_audience_selection_id,
    owner.campaign_id AS materialized_owner_campaign_id,
    owner.id AS materialized_owner_selection_id,
    attempt.platform,
    attempt.sent_message_id,
    attempt.tracking_id,
    attempt.send_status,
    attempt.parts_delivered,
    attempt.sent_at,
    attempt.sent_updated_at,
    CASE
        WHEN audience.id IS NULL THEN 'sent_phone_has_no_exact_audience_profile'
        WHEN owner.campaign_id IS NULL THEN 'audience_has_no_materialized_bundle_claim'
        WHEN owner.campaign_id = attempt.campaign_id THEN 'sent_by_its_materialized_owner'
        ELSE 'sent_by_campaign_other_than_materialized_owner'
    END AS allocation_relationship
FROM duplicate_attempts AS attempt
LEFT JOIN audience_profiles AS audience ON audience.phone_number = attempt.phone_number
LEFT JOIN bundle_audience_selection_members AS member
  ON member.bundle_id = 347 -- :'bundle_id'::integer
 AND member.audience_id = audience.id
LEFT JOIN bundle_audience_selections AS owner
  ON owner.id = member.selection_id
 AND owner.bundle_id = member.bundle_id
ORDER BY attempt.normalized_phone_number, attempt.attempt_number;
