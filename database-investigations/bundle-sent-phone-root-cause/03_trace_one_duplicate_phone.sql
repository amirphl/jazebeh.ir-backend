-- Trace one duplicate phone through sent records, profile identity, processed
-- audience IDs, and the materialized bundle ledger. Replace target_phone.

WITH target AS (
    SELECT '989001384826'::varchar AS target_phone
), bundle_campaigns AS MATERIALIZED (
    SELECT id, phase, status, spec
    FROM campaigns
    WHERE bundle_id = 347 -- :'bundle_id'::integer
), sent_messages AS (
    SELECT campaign.id AS campaign_id, campaign.phase, campaign.status, campaign.spec,
           processed.id AS processed_campaign_id, processed.is_current,
           processed.audience_ids, 'sms'::text AS platform,
           sent.id AS sent_message_id, sent.tracking_id::text AS tracking_id,
           sent.status::text AS send_status, sent.phone_number, sent.created_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN target ON TRUE
    JOIN sent_sms AS sent
      ON sent.processed_campaign_id = processed.id
     AND sent.phone_number = target.target_phone

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec,
           processed.id, processed.is_current, processed.audience_ids, 'bale',
           sent.id, sent.tracking_id, sent.status::text, sent.phone_number, sent.created_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN target ON TRUE
    JOIN sent_bale_messages AS sent
      ON sent.processed_campaign_id = processed.id
     AND sent.phone_number = target.target_phone

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec,
           processed.id, processed.is_current, processed.audience_ids, 'rubika',
           sent.id, sent.tracking_id, sent.status::text, sent.phone_number, sent.created_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN target ON TRUE
    JOIN sent_rubika_messages AS sent
      ON sent.processed_campaign_id = processed.id
     AND sent.phone_number = target.target_phone

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec,
           processed.id, processed.is_current, processed.audience_ids, 'splus',
           sent.id, sent.tracking_id, sent.status::text, sent.phone_number, sent.created_at
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN target ON TRUE
    JOIN sent_splus_messages AS sent
      ON sent.processed_campaign_id = processed.id
     AND sent.phone_number = target.target_phone
), profile AS (
    SELECT audience.id AS audience_id, audience.uid, audience.phone_number
    FROM audience_profiles AS audience
    JOIN target ON audience.phone_number = target.target_phone
)
SELECT
    message.phone_number,
    profile.audience_id,
    profile.uid AS audience_uid,
    message.campaign_id,
    message.phase AS campaign_phase,
    message.status AS campaign_status,
    CASE
        WHEN lower(btrim(coalesce(message.spec->>'audience_targeting_method', '')))
             IN ('standard', 'smart_targeting', 'excel')
            THEN lower(btrim(message.spec->>'audience_targeting_method'))
        WHEN nullif(btrim(message.spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
            THEN 'excel'
        ELSE 'standard'
    END AS targeting_method,
    message.spec->>'target_audience_excel_file_uuid' AS excel_file_uuid,
    message.processed_campaign_id,
    message.is_current AS processed_campaign_is_current,
    profile.audience_id = ANY(message.audience_ids) AS profile_id_in_processed_audience_ids,
    EXISTS (
        SELECT 1
        FROM bundle_audience_selection_members AS member
        JOIN bundle_audience_selections AS selection
          ON selection.id = member.selection_id
         AND selection.bundle_id = member.bundle_id
        WHERE member.bundle_id = 347 -- :'bundle_id'::integer
          AND member.audience_id = profile.audience_id
    ) AS profile_id_has_materialized_bundle_claim,
    message.platform,
    message.sent_message_id,
    message.tracking_id,
    message.send_status,
    message.created_at
FROM sent_messages AS message
LEFT JOIN profile ON profile.phone_number = message.phone_number
ORDER BY message.created_at, message.campaign_id, message.sent_message_id;
