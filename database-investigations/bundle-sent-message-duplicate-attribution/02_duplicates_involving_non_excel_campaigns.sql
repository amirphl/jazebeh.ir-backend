-- Return duplicate phone numbers involving any non-Excel-targeted campaign.
-- Zero rows means every record for every duplicate phone is from an
-- Excel-targeted campaign. It does not prove the duplicate was in an Excel
-- file; the cause can still be a retry within an Excel-targeted campaign.
-- Usage with psql: \set bundle_id 347

WITH sent_messages AS (
    SELECT campaign.id AS campaign_id, campaign.spec, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.spec, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.spec, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, campaign.spec, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347--:'bundle_id'::integer
), classified_messages AS (
    SELECT
        campaign_id,
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
)
SELECT
    normalized_phone_number,
    COUNT(*) AS sent_message_count,
    ARRAY_AGG(DISTINCT campaign_id ORDER BY campaign_id) AS campaign_ids,
    ARRAY_AGG(DISTINCT targeting_method ORDER BY targeting_method) AS targeting_methods
FROM classified_messages
WHERE normalized_phone_number <> ''
GROUP BY normalized_phone_number
HAVING COUNT(*) > 1
   AND BOOL_OR(targeting_method <> 'excel')
ORDER BY sent_message_count DESC, normalized_phone_number;
