-- Find phone numbers used by more than one sent-message record in one bundle.
-- Usage with psql: \set bundle_id 123

WITH sent_messages AS (
    SELECT
        campaign.bundle_id,
        campaign.id AS campaign_id,
        processed.id AS processed_campaign_id,
        'sms'::text AS platform,
        sent.id AS sent_message_id,
        sent.phone_number,
        sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.bundle_id, campaign.id, processed.id, 'bale', sent.id,
           sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.bundle_id, campaign.id, processed.id, 'rubika', sent.id,
           sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.bundle_id, campaign.id, processed.id, 'splus', sent.id,
           sent.phone_number, sent.created_at
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
)
SELECT
    bundle_id,
    normalized_phone_number,
    COUNT(*) AS sent_message_count,
    COUNT(DISTINCT campaign_id) AS campaign_count,
    ARRAY_AGG(DISTINCT platform ORDER BY platform) AS platforms,
    MIN(created_at) AS first_sent_at,
    MAX(created_at) AS last_sent_at
FROM normalized_messages
WHERE normalized_phone_number <> ''
GROUP BY bundle_id, normalized_phone_number
HAVING COUNT(*) > 1
ORDER BY sent_message_count DESC, normalized_phone_number;
