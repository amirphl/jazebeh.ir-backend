-- Classify duplicate sent phone numbers by the campaigns that sent them.
-- Usage with psql: \set bundle_id 347

WITH sent_messages AS (
    SELECT campaign.id AS campaign_id, campaign.spec, processed.id AS processed_campaign_id,
           'sms'::text AS platform, sent.id AS sent_message_id,
           sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.spec, processed.id, 'bale', sent.id,
           sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.spec, processed.id, 'rubika', sent.id,
           sent.phone_number, sent.created_at
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.spec, processed.id, 'splus', sent.id,
           sent.phone_number, sent.created_at
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
    COUNT(*) AS sent_message_count,
    COUNT(DISTINCT message.campaign_id) AS campaign_count,
    COUNT(DISTINCT message.processed_campaign_id) AS processed_campaign_count,
    ARRAY_AGG(DISTINCT message.targeting_method ORDER BY message.targeting_method)
        AS targeting_methods,
    ARRAY_AGG(DISTINCT message.campaign_id ORDER BY message.campaign_id) AS campaign_ids,
    ARRAY_AGG(DISTINCT message.platform ORDER BY message.platform) AS platforms,
    CASE
        WHEN BOOL_AND(message.targeting_method = 'excel')
             AND COUNT(DISTINCT message.campaign_id) > 1
            THEN 'only_excel_campaigns_multiple_campaigns'
        WHEN BOOL_AND(message.targeting_method = 'excel')
             AND COUNT(DISTINCT message.processed_campaign_id) > 1
            THEN 'only_excel_campaigns_multiple_processing_attempts'
        WHEN BOOL_AND(message.targeting_method = 'excel')
            THEN 'only_excel_campaigns_same_processing_attempt'
        ELSE 'includes_non_excel_campaign'
    END AS attribution_class
FROM classified_messages AS message
JOIN duplicate_phones AS duplicate
  ON duplicate.normalized_phone_number = message.normalized_phone_number
GROUP BY message.normalized_phone_number
ORDER BY attribution_class, sent_message_count DESC, message.normalized_phone_number;
