-- Return one row stating whether all sent-message phone numbers are unique.
-- Usage with psql: \set bundle_id 123

WITH sent_phone_numbers AS (
    SELECT sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347 --:'bundle_id'::integer

    UNION ALL

    SELECT sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer
), normalized_phone_numbers AS (
    SELECT regexp_replace(btrim(phone_number), '[^0-9]', '', 'g') AS normalized_phone_number
    FROM sent_phone_numbers
    WHERE NULLIF(btrim(phone_number), '') IS NOT NULL
), counts AS (
    SELECT
        COUNT(*) FILTER (WHERE normalized_phone_number <> '') AS sent_message_count,
        COUNT(DISTINCT normalized_phone_number)
            FILTER (WHERE normalized_phone_number <> '') AS distinct_phone_number_count
    FROM normalized_phone_numbers
)
SELECT
    347--:'bundle_id'::integer AS bundle_id,
    sent_message_count,
    distinct_phone_number_count,
    sent_message_count = distinct_phone_number_count AS all_sent_phone_numbers_are_unique
FROM counts;
