-- Count Excel sends whose resolved profile already belongs to another
-- materialized campaign in the same bundle. Each returned row is a bypass of
-- the bundle's runtime audience-ID uniqueness rule by the Excel delivery path.

WITH excel_campaigns AS MATERIALIZED (
    SELECT campaign.id AS campaign_id,
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
), excel_sent_messages AS (
    SELECT campaign.campaign_id, campaign.excel_file_uuid, sent.phone_number
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.campaign_id, campaign.excel_file_uuid, sent.phone_number
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.campaign_id, campaign.excel_file_uuid, sent.phone_number
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT campaign.campaign_id, campaign.excel_file_uuid, sent.phone_number
    FROM excel_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.campaign_id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
)
SELECT
    excel.excel_file_uuid,
    excel.campaign_id AS excel_campaign_id,
    owner.campaign_id AS materialized_owner_campaign_id,
    COUNT(*) AS overlapping_sent_message_count,
    COUNT(DISTINCT audience.id) AS overlapping_audience_count
FROM excel_sent_messages AS excel
JOIN audience_profiles AS audience ON audience.phone_number = excel.phone_number
JOIN bundle_audience_selection_members AS member
  ON member.bundle_id = 347 -- :'bundle_id'::integer
 AND member.audience_id = audience.id
JOIN bundle_audience_selections AS owner
  ON owner.id = member.selection_id
 AND owner.bundle_id = member.bundle_id
WHERE owner.campaign_id <> excel.campaign_id
GROUP BY excel.excel_file_uuid, excel.campaign_id, owner.campaign_id
ORDER BY overlapping_audience_count DESC, overlapping_sent_message_count DESC,
         excel.campaign_id, owner.campaign_id;
