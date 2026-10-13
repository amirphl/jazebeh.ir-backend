-- Campaign refunds posted from a UTC timestamp onward.
--
-- Replace params.from_at before running. Set params.customer_id to a customer
-- ID to limit the report, or leave it NULL for all customers. A campaign refund
-- is identified by the immutable refund transaction's metadata.campaign_id. The lateral join
-- selects the charge that funded the refund: an approved-campaign fee first,
-- otherwise the pre-approval frozen reservation.

WITH params AS (
    SELECT
        TIMESTAMPTZ '2026-08-25 00:00:00+00' AS from_at,
        27::bigint AS customer_id -- e.g. 123; NULL means all customers
),
refunds AS (
    SELECT
        t.*,
        NULLIF(t.metadata ->> 'campaign_id', '')::bigint AS campaign_id,
        t.metadata ->> 'source' AS refund_source,
        t.metadata ->> 'operation' AS refund_operation,
        t.metadata ->> 'comment' AS refund_reason,
        NULLIF(t.metadata ->> 'refund_amount', '')::bigint AS metadata_refund_amount
    FROM transactions AS t
    CROSS JOIN params AS p
    WHERE t.deleted_at IS NULL
      AND t.type = 'refund'
      AND t.status = 'completed'
      AND t.created_at >= p.from_at
      AND t.metadata ? 'campaign_id'
      AND (p.customer_id IS NULL OR t.customer_id = p.customer_id)
)
SELECT
    r.created_at AS refund_at,
    r.id AS refund_transaction_id,
    r.uuid AS refund_transaction_uuid,
    r.correlation_id,
    r.customer_id,
    c.uuid AS customer_uuid,
    c.company_name,
    c.representative_first_name,
    c.representative_last_name,
    r.wallet_id,
    w.uuid AS wallet_uuid,
    campaign.id AS campaign_id,
    campaign.uuid AS campaign_uuid,
    campaign.spec ->> 'title' AS campaign_name,
    campaign.status AS campaign_status,
    campaign.spec ->> 'platform' AS campaign_platform,
    campaign.spec ->> 'schedule_at' AS campaign_scheduled_at,
    campaign.spec ->> 'budget' AS campaign_budget,
    original.id AS original_charge_transaction_id,
    original.uuid AS original_charge_transaction_uuid,
    original.correlation_id AS original_charge_correlation_id,
    original.type AS original_charge_type,
    original.amount AS original_campaign_charge,
    original.created_at AS original_charge_at,
    original.metadata AS original_charge_metadata,
    COALESCE(r.metadata_refund_amount, r.amount) AS refunded_amount,
    r.currency,
    r.refund_source,
    r.refund_operation,
    r.refund_reason,
    r.status AS refund_status,
    r.balance_before AS refund_balance_before,
    r.balance_after AS refund_balance_after,
    r.balance_before ->> 'free' AS free_balance_before,
    r.balance_after ->> 'free' AS free_balance_after,
    r.balance_before ->> 'credit' AS credit_balance_before,
    r.balance_after ->> 'credit' AS credit_balance_after,
    r.balance_before ->> 'spent_on_campaign' AS spent_on_campaign_before,
    r.balance_after ->> 'spent_on_campaign' AS spent_on_campaign_after,
    r.metadata AS refund_metadata,
    campaign.statistics AS campaign_statistics
FROM refunds AS r
JOIN campaigns AS campaign
  ON campaign.id = r.campaign_id
JOIN customers AS c
  ON c.id = r.customer_id
JOIN wallets AS w
  ON w.id = r.wallet_id
LEFT JOIN LATERAL (
    SELECT charge.*
    FROM transactions AS charge
    WHERE charge.deleted_at IS NULL
      AND charge.customer_id = r.customer_id
      AND charge.status = 'completed'
      AND NULLIF(charge.metadata ->> 'campaign_id', '')::bigint = r.campaign_id
      AND charge.created_at <= r.created_at
      AND (
          (charge.type = 'fee'
           AND charge.metadata ->> 'source' = 'admin_campaign_approve'
           AND charge.metadata ->> 'operation' = 'approve_campaign_budget_consume')
          OR
          (charge.type = 'freeze'
           AND charge.metadata ->> 'source' = 'campaign_update'
           AND charge.metadata ->> 'operation' = 'reserve_budget')
      )
    ORDER BY
        CASE WHEN charge.type = 'fee' THEN 0 ELSE 1 END,
        charge.created_at DESC,
        charge.id DESC
    LIMIT 1
) AS original ON TRUE
ORDER BY r.created_at, r.id;
