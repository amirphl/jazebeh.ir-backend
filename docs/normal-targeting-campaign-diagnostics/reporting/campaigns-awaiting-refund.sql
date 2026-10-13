-- Campaigns that meet a refund condition but do not have its completed refund.
--
-- Replace params.from_at before running. Set params.customer_id to a customer
-- ID to limit the report, or leave it NULL for all customers. This report
-- covers the two refund paths implemented by the application:
--   * rejected/cancelled campaigns with a completed reserve or approved charge;
--   * executed campaigns, older than the 72-hour reconciliation delay, whose
--     aggregatedTotalSent is lower than NumAudience.
--
-- It intentionally includes reconciliation-error details.  Those rows need
-- manual investigation because the application marks them to avoid retries.

WITH params AS (
    SELECT
        TIMESTAMPTZ '2026-08-25 00:00:00+00' AS from_at,
        27::bigint AS customer_id -- e.g. 123; NULL means all customers
),
campaign_charges AS (
    SELECT
        t.*,
        NULLIF(t.metadata ->> 'campaign_id', '')::bigint AS campaign_id,
        t.metadata ->> 'source' AS source,
        t.metadata ->> 'operation' AS operation
    FROM transactions AS t
    CROSS JOIN params AS p
    WHERE t.deleted_at IS NULL
      AND t.status = 'completed'
      AND t.metadata ? 'campaign_id'
      AND (p.customer_id IS NULL OR t.customer_id = p.customer_id)
),
latest_charge AS (
    SELECT DISTINCT ON (campaign_id)
        campaign_id,
        id,
        uuid,
        correlation_id,
        type,
        amount,
        created_at,
        metadata
    FROM campaign_charges
    WHERE (type = 'fee'
           AND source = 'admin_campaign_approve'
           AND operation = 'approve_campaign_budget_consume')
       OR (type = 'freeze'
           AND source = 'campaign_update'
           AND operation = 'reserve_budget')
    ORDER BY campaign_id,
             CASE WHEN type = 'fee' THEN 0 ELSE 1 END,
             created_at DESC,
             id DESC
),
latest_reservation AS (
    SELECT DISTINCT ON (campaign_id)
        campaign_id,
        id,
        uuid,
        amount,
        created_at,
        metadata
    FROM campaign_charges
    WHERE type = 'freeze'
      AND source = 'campaign_update'
      AND operation = 'reserve_budget'
    ORDER BY campaign_id, created_at DESC, id DESC
),
completed_refunds AS (
    SELECT
        NULLIF(t.metadata ->> 'campaign_id', '')::bigint AS campaign_id,
        COUNT(*) AS refund_count,
        SUM(t.amount) AS refunded_amount,
        MAX(t.created_at) AS last_refund_at
    FROM transactions AS t
    CROSS JOIN params AS p
    WHERE t.deleted_at IS NULL
      AND t.type = 'refund'
      AND t.status = 'completed'
      AND t.metadata ? 'campaign_id'
      AND (p.customer_id IS NULL OR t.customer_id = p.customer_id)
    GROUP BY NULLIF(t.metadata ->> 'campaign_id', '')::bigint
),
candidate_campaigns AS (
    SELECT
        c.*,
        COALESCE(c.updated_at, c.created_at) AS candidate_at,
        NULLIF(c.statistics ->> 'aggregatedTotalSent', '')::bigint AS aggregated_total_sent
    FROM campaigns AS c
    CROSS JOIN params AS p
    WHERE COALESCE(c.updated_at, c.created_at) >= p.from_at
      AND (p.customer_id IS NULL OR c.customer_id = p.customer_id)
)
SELECT
    CASE
        WHEN c.status IN ('rejected', 'cancelled', 'cancelled-by-admin')
            THEN 'cancelled_or_rejected_charge_not_refunded'
        ELSE 'executed_underdelivery_not_refunded'
    END AS investigation_reason,
    c.candidate_at,
    c.id AS campaign_id,
    c.uuid AS campaign_uuid,
    c.spec ->> 'title' AS campaign_name,
    c.status AS campaign_status,
    c.spec ->> 'platform' AS campaign_platform,
    c.spec ->> 'schedule_at' AS scheduled_at,
    c.customer_id,
    customer.uuid AS customer_uuid,
    customer.company_name,
    customer.representative_first_name,
    customer.representative_last_name,
    c.num_audience AS intended_audience,
    c.aggregated_total_sent,
    CASE
        WHEN c.num_audience IS NOT NULL AND c.aggregated_total_sent IS NOT NULL
            THEN GREATEST(c.num_audience - c.aggregated_total_sent, 0)
    END AS missing_messages,
    charge.id AS original_charge_transaction_id,
    charge.uuid AS original_charge_transaction_uuid,
    charge.correlation_id AS original_charge_correlation_id,
    charge.type AS original_charge_type,
    charge.amount AS original_campaign_charge,
    charge.created_at AS original_charge_at,
    charge.metadata AS original_charge_metadata,
    reservation.id AS reservation_transaction_id,
    reservation.uuid AS reservation_transaction_uuid,
    reservation.amount AS reserved_campaign_budget,
    reservation.created_at AS reservation_at,
    reservation.metadata AS refund_calculation_reservation_metadata,
    refunds.refund_count AS completed_campaign_refund_count,
    refunds.refunded_amount AS completed_campaign_refund_amount,
    refunds.last_refund_at,
    c.statistics ->> 'undeliveredRefundError' AS undelivered_refund_error,
    c.statistics ->> 'undeliveredRefundErrorAt' AS undelivered_refund_error_at,
    c.statistics ->> 'undeliveredRefundErrorMsg' AS undelivered_refund_error_message,
    c.statistics AS campaign_statistics
FROM candidate_campaigns AS c
JOIN customers AS customer
  ON customer.id = c.customer_id
JOIN latest_charge AS charge
  ON charge.campaign_id = c.id
LEFT JOIN latest_reservation AS reservation
  ON reservation.campaign_id = c.id
LEFT JOIN completed_refunds AS refunds
  ON refunds.campaign_id = c.id
WHERE refunds.campaign_id IS NULL
  AND (
      c.status IN ('rejected', 'cancelled', 'cancelled-by-admin')
      OR (
          c.status = 'executed'
          AND c.spec ->> 'schedule_at' IS NOT NULL
          AND (c.spec ->> 'schedule_at')::timestamptz <= NOW() - INTERVAL '72 hours'
          AND c.num_audience IS NOT NULL
          AND c.num_audience > 0
          AND c.aggregated_total_sent IS NOT NULL
          AND c.aggregated_total_sent < c.num_audience
      )
  )
ORDER BY c.candidate_at, c.id;
