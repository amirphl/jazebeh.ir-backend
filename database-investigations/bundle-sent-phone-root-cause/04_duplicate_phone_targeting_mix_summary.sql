-- Summarize which targeting methods participate in duplicate sent phones.
-- This is a compact version of the targeting-attribution query for finding
-- whether the issue is limited to Excel or also involves Smart Targeting.

WITH bundle_campaigns AS MATERIALIZED (
    SELECT id, spec
    FROM campaigns
    WHERE bundle_id = 347 -- :'bundle_id'::integer
), sent_messages AS (
    SELECT campaign.spec, sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.spec, sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.spec, sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.spec, sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
), classified_messages AS (
    SELECT
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
), duplicate_phone_methods AS (
    SELECT
        normalized_phone_number,
        ARRAY_AGG(DISTINCT targeting_method ORDER BY targeting_method) AS targeting_methods,
        COUNT(*) AS sent_message_count
    FROM classified_messages
    WHERE normalized_phone_number <> ''
    GROUP BY normalized_phone_number
    HAVING COUNT(*) > 1
)
SELECT
    targeting_methods,
    COUNT(*) AS duplicate_phone_count,
    SUM(sent_message_count) AS sent_message_count_for_duplicate_phones
FROM duplicate_phone_methods
GROUP BY targeting_methods
ORDER BY duplicate_phone_count DESC, targeting_methods;
