-- Show every sent-message record for phone numbers duplicated in one bundle.
-- Usage with psql: \set bundle_id 123

WITH sent_messages AS (
    SELECT
        campaign.bundle_id,
        campaign.id AS campaign_id,
        processed.id AS processed_campaign_id,
        processed.is_current AS processed_campaign_is_current,
        'sms'::text AS platform,
        sent.id AS sent_message_id,
        sent.tracking_id::text AS tracking_id,
        sent.phone_number,
        sent.status::text AS status,
        sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.bundle_id, campaign.id, processed.id, processed.is_current,
           'bale', sent.id, sent.tracking_id, sent.phone_number,
           sent.status::text, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.bundle_id, campaign.id, processed.id, processed.is_current,
           'rubika', sent.id, sent.tracking_id, sent.phone_number,
           sent.status::text, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.bundle_id, campaign.id, processed.id, processed.is_current,
           'splus', sent.id, sent.tracking_id, sent.phone_number,
           sent.status::text, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
), normalized_messages AS (
    SELECT
        *,
        regexp_replace(btrim(phone_number), '[^0-9]', '', 'g') AS normalized_phone_number
    FROM sent_messages
    WHERE bundle_id = 347 -- :'bundle_id'::integer
      AND NULLIF(btrim(phone_number), '') IS NOT NULL
), duplicate_phones AS (
    SELECT normalized_phone_number
    FROM normalized_messages
    WHERE normalized_phone_number <> ''
    GROUP BY normalized_phone_number
    HAVING COUNT(*) > 1
)
SELECT
    message.bundle_id,
    message.normalized_phone_number,
    message.phone_number AS stored_phone_number,
    message.platform,
    message.campaign_id,
    message.processed_campaign_id,
    message.processed_campaign_is_current,
    message.sent_message_id,
    message.tracking_id,
    message.status,
    message.created_at
FROM normalized_messages AS message
JOIN duplicate_phones AS duplicate
  ON duplicate.normalized_phone_number = message.normalized_phone_number
ORDER BY message.normalized_phone_number, message.created_at,
         message.platform, message.sent_message_id;
