-- Classify duplicate sends by campaign and processing-attempt scope.
-- Cross-campaign duplicates cannot be caused by an API retry of one campaign.

WITH sent_messages AS (
    SELECT campaign.id AS campaign_id, processed.id AS processed_campaign_id,
           sent.id AS sent_message_id, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347 -- :'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, processed.id, sent.id, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347 -- :'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, processed.id, sent.id, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347 -- :'bundle_id'::integer

    UNION ALL

    SELECT campaign.id, processed.id, sent.id, sent.phone_number
    FROM campaigns AS campaign
    JOIN processed_campaigns AS processed ON processed.campaign_id = campaign.id
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE campaign.bundle_id = 347 -- :'bundle_id'::integer
), normalized_messages AS (
    SELECT *, regexp_replace(btrim(phone_number), '[^0-9]', '', 'g') AS normalized_phone_number
    FROM sent_messages
    WHERE nullif(btrim(phone_number), '') IS NOT NULL
), duplicate_messages AS (
    SELECT *
    FROM normalized_messages
    WHERE normalized_phone_number <> ''
      AND normalized_phone_number IN (
          SELECT normalized_phone_number
          FROM normalized_messages
          WHERE normalized_phone_number <> ''
          GROUP BY normalized_phone_number
          HAVING COUNT(*) > 1
      )
), per_processed_campaign AS (
    SELECT normalized_phone_number, processed_campaign_id, COUNT(*) AS sent_message_count
    FROM duplicate_messages
    GROUP BY normalized_phone_number, processed_campaign_id
), per_campaign AS (
    SELECT normalized_phone_number, campaign_id, COUNT(DISTINCT processed_campaign_id) AS processed_campaign_count
    FROM duplicate_messages
    GROUP BY normalized_phone_number, campaign_id
), phone_scope AS (
    SELECT
        message.normalized_phone_number,
        COUNT(*) AS sent_message_count,
        COUNT(DISTINCT message.campaign_id) AS campaign_count,
        COUNT(DISTINCT message.processed_campaign_id) AS processed_campaign_count
    FROM duplicate_messages AS message
    GROUP BY message.normalized_phone_number
)
SELECT
    scope.normalized_phone_number,
    scope.sent_message_count,
    scope.campaign_count,
    scope.processed_campaign_count,
    EXISTS (
        SELECT 1
        FROM per_processed_campaign AS processed
        WHERE processed.normalized_phone_number = scope.normalized_phone_number
          AND processed.sent_message_count > 1
    ) AS repeated_within_one_processing_attempt,
    EXISTS (
        SELECT 1
        FROM per_campaign AS campaign
        WHERE campaign.normalized_phone_number = scope.normalized_phone_number
          AND campaign.processed_campaign_count > 1
    ) AS repeated_across_processing_attempts_of_one_campaign,
    scope.campaign_count > 1 AS repeated_across_campaigns,
    CASE
        WHEN scope.campaign_count > 1 THEN 'cross_campaign_reuse'
        WHEN EXISTS (
            SELECT 1 FROM per_campaign AS campaign
            WHERE campaign.normalized_phone_number = scope.normalized_phone_number
              AND campaign.processed_campaign_count > 1
        ) THEN 'multiple_processing_attempts_of_one_campaign'
        WHEN EXISTS (
            SELECT 1 FROM per_processed_campaign AS processed
            WHERE processed.normalized_phone_number = scope.normalized_phone_number
              AND processed.sent_message_count > 1
        ) THEN 'multiple_send_rows_in_one_processing_attempt'
        ELSE 'inspect_record_history'
    END AS strongest_send_scope_evidence
FROM phone_scope AS scope
ORDER BY strongest_send_scope_evidence, scope.sent_message_count DESC,
         scope.normalized_phone_number;
