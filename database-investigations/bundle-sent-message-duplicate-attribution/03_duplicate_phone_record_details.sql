-- Inspect every record for duplicate phones, including targeting and retry
-- context. Use this after the attribution summary to investigate a class.
-- Usage with psql: \set bundle_id 347

WITH sent_messages AS (
    SELECT campaign.id AS campaign_id, campaign.phase, campaign.status, campaign.spec,
           processed.id AS processed_campaign_id, processed.is_current,
           'sms'::text AS platform, sent.id AS sent_message_id,
           sent.tracking_id::text AS tracking_id, sent.status::text AS send_status,
           sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec, processed.id, processed.is_current,
           'bale', sent.id, sent.tracking_id, sent.status::text, sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec, processed.id, processed.is_current,
           'rubika', sent.id, sent.tracking_id, sent.status::text, sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.phase, campaign.status, campaign.spec, processed.id, processed.is_current,
           'splus', sent.id, sent.tracking_id, sent.status::text, sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer
), classified_messages AS (
    SELECT
        *,
        regexp_replace(btrim(phone_number), '[^0-9]', '', 'g') AS normalized_phone_number,
        CASE
            WHEN lower(btrim(coalesce(spec->>'audience_targeting_method', '')))
                 IN ('standard', 'smart_targeting', 'excel')
                THEN lower(btrim(spec->>'audience_targeting_method'))
            WHEN nullif(btrim(spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
                THEN 'excel'
            ELSE 'standard'
        END AS targeting_method
    FROM sent_messages
    WHERE nullif(btrim(phone_number), '') IS NOT NULL
), duplicate_phones AS (
    SELECT normalized_phone_number
    FROM classified_messages
    WHERE normalized_phone_number <> ''
    GROUP BY normalized_phone_number
    HAVING COUNT(*) > 1
)
SELECT
    message.normalized_phone_number,
    message.phone_number AS stored_phone_number,
    message.targeting_method,
    message.campaign_id,
    message.phase AS campaign_phase,
    message.status AS campaign_status,
    message.processed_campaign_id,
    message.is_current AS processed_campaign_is_current,
    message.platform,
    message.sent_message_id,
    message.tracking_id,
    message.send_status,
    message.created_at
FROM classified_messages AS message
JOIN duplicate_phones AS duplicate
  ON duplicate.normalized_phone_number = message.normalized_phone_number
ORDER BY message.normalized_phone_number, message.created_at,
         message.campaign_id, message.sent_message_id;
