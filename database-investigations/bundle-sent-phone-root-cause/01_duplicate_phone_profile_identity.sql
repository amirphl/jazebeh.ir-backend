-- Fast profile-identity check for duplicate sent phones.
-- It probes audience_profiles only for duplicate sent values, so the unique
-- phone_number index is usable and the 101M-row table is never scanned.

WITH bundle_campaigns AS MATERIALIZED (
    SELECT id
    FROM campaigns
    WHERE bundle_id = 347 -- :'bundle_id'::integer
), sent_messages AS (
    SELECT sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id

    UNION ALL

    SELECT sent.phone_number
    FROM bundle_campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
), normalized_messages AS (
    SELECT phone_number,
           regexp_replace(btrim(phone_number), '[^0-9]', '', 'g') AS normalized_phone_number
    FROM sent_messages
    WHERE nullif(btrim(phone_number), '') IS NOT NULL
), duplicate_phones AS (
    SELECT normalized_phone_number, COUNT(*) AS sent_message_count
    FROM normalized_messages
    WHERE normalized_phone_number <> ''
    GROUP BY normalized_phone_number
    HAVING COUNT(*) > 1
), duplicate_phone_values AS (
    SELECT DISTINCT message.normalized_phone_number, message.phone_number
    FROM normalized_messages AS message
    JOIN duplicate_phones AS duplicate
      ON duplicate.normalized_phone_number = message.normalized_phone_number
), profile_matches AS (
    SELECT
        phone.normalized_phone_number,
        COUNT(audience.id) AS matching_profile_count,
        ARRAY_AGG(audience.id ORDER BY audience.id) FILTER (WHERE audience.id IS NOT NULL)
            AS matching_profile_ids
    FROM duplicate_phone_values AS phone
    LEFT JOIN audience_profiles AS audience
      ON audience.phone_number = phone.phone_number
    GROUP BY phone.normalized_phone_number
)
SELECT
    duplicate.normalized_phone_number,
    duplicate.sent_message_count,
    profile.matching_profile_count,
    profile.matching_profile_ids,
    CASE
        WHEN profile.matching_profile_count = 0 THEN 'sent_value_has_no_exact_audience_profile'
        WHEN profile.matching_profile_count = 1 THEN 'one_exact_audience_profile'
        ELSE 'multiple_stored_phone_formats_map_to_multiple_profiles'
    END AS profile_identity_evidence
FROM duplicate_phones AS duplicate
JOIN profile_matches AS profile
  ON profile.normalized_phone_number = duplicate.normalized_phone_number
ORDER BY profile_identity_evidence, duplicate.sent_message_count DESC,
         duplicate.normalized_phone_number;
