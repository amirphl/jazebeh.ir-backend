-- Find Excel files reused by more than one sending campaign in this bundle.
-- Reusing a file is expected to resend its recipients unless Excel recipients
-- are checked against the bundle's materialized audience ledger.

WITH excel_campaigns AS (
    SELECT
        campaign.id AS campaign_id,
        campaign.phase,
        campaign.status,
        campaign.spec->>'target_audience_excel_file_uuid' AS excel_file_uuid
    FROM campaigns AS campaign
    WHERE campaign.bundle_id = 347 -- :'bundle_id'::integer
      AND (
          lower(btrim(coalesce(campaign.spec->>'audience_targeting_method', ''))) = 'excel'
          OR (
              nullif(btrim(campaign.spec->>'audience_targeting_method'), '') IS NULL
              AND nullif(btrim(campaign.spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
          )
      )
      AND nullif(btrim(campaign.spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
), sent_campaigns AS (
    SELECT processed.campaign_id, MIN(sent.created_at) AS first_sent_at,
           MAX(sent.created_at) AS last_sent_at, COUNT(*) AS sent_message_count
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id

    UNION ALL

    SELECT processed.campaign_id, MIN(sent.created_at), MAX(sent.created_at), COUNT(*)
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id

    UNION ALL

    SELECT processed.campaign_id, MIN(sent.created_at), MAX(sent.created_at), COUNT(*)
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id

    UNION ALL

    SELECT processed.campaign_id, MIN(sent.created_at), MAX(sent.created_at), COUNT(*)
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    GROUP BY processed.campaign_id
), sent_counts AS (
    SELECT campaign_id, MIN(first_sent_at) AS first_sent_at, MAX(last_sent_at) AS last_sent_at,
           SUM(sent_message_count) AS sent_message_count
    FROM sent_campaigns
    GROUP BY campaign_id
)
SELECT
    excel.excel_file_uuid,
    COUNT(*) AS sending_campaign_count,
    ARRAY_AGG(excel.campaign_id ORDER BY excel.campaign_id) AS campaign_ids,
    ARRAY_AGG(excel.phase ORDER BY excel.campaign_id) AS phases,
    SUM(sent.sent_message_count) AS sent_message_count,
    MIN(sent.first_sent_at) AS first_sent_at,
    MAX(sent.last_sent_at) AS last_sent_at
FROM excel_campaigns AS excel
JOIN sent_counts AS sent ON sent.campaign_id = excel.campaign_id
GROUP BY excel.excel_file_uuid
HAVING COUNT(*) > 1
ORDER BY sending_campaign_count DESC, sent_message_count DESC, excel.excel_file_uuid;
