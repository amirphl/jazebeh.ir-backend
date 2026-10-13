-- Complete customer wallet audit trail from a UTC timestamp onward.
--
-- Replace params.from_at before running. Set params.customer_id to a customer
-- ID to limit the report, or leave it NULL for all customers. Transactions are
-- the immutable financial ledger. The embedded
-- before/after JSON documents are the authoritative balance states recorded
-- with each wallet mutation.

WITH params AS (
    SELECT
        TIMESTAMPTZ '2026-08-25 00:00:00+00' AS from_at,
        NULL::bigint AS customer_id -- e.g. 123; NULL means all customers
),
ledger AS (
    SELECT
        t.*,
        t.metadata ->> 'source' AS source,
        t.metadata ->> 'operation' AS operation,
        NULLIF(t.metadata ->> 'campaign_id', '')::bigint AS campaign_id,
        NULLIF(t.metadata ->> 'payment_request_id', '')::bigint AS payment_request_id,
        NULLIF(t.metadata ->> 'crypto_payment_request_id', '')::bigint AS crypto_payment_request_id,
        NULLIF(t.metadata ->> 'admin_id', '')::bigint AS admin_id,
        t.metadata ->> 'deposit_receipt_uuid' AS deposit_receipt_uuid
    FROM transactions AS t
    CROSS JOIN params AS p
    WHERE t.deleted_at IS NULL
      AND t.created_at >= p.from_at
      AND (p.customer_id IS NULL OR t.customer_id = p.customer_id)
)
SELECT
    l.created_at AS event_at,
    l.type AS transaction_type,
    l.status AS transaction_status,
    l.amount,
    l.source,
    l.operation,
    l.campaign_id,
    campaign.spec,
	campaign.spec ->> 'title' AS campaign_name,
    campaign.status AS campaign_status,
    l.balance_before ->> 'free' AS free_balance_before,
    l.balance_after ->> 'free' AS free_balance_after,
    l.balance_before ->> 'credit' AS credit_balance_before,
    l.balance_after ->> 'credit' AS credit_balance_after,
    l.balance_before ->> 'frozen' AS frozen_balance_before,
    l.balance_after ->> 'frozen' AS frozen_balance_after,
    l.balance_before ->> 'spent_on_campaign' AS spent_on_campaign_before,
    l.balance_after ->> 'spent_on_campaign' AS spent_on_campaign_after,
    l.metadata AS transaction_metadata,
	l.id AS transaction_id,
    l.uuid AS transaction_uuid,
    l.correlation_id,
	l.currency,
    l.customer_id,
    c.uuid AS customer_uuid,
    c.company_name,
    c.representative_first_name,
    c.representative_last_name,
    c.representative_mobile,
    c.email,
    l.wallet_id,
    w.uuid AS wallet_uuid,
    campaign.uuid AS campaign_uuid,
    l.admin_id,
    l.description,
    l.balance_before,
    l.balance_after,
    l.external_reference,
    l.external_trace,
    l.external_rrn,
    l.external_masked_pan,
    l.payment_request_id,
    payment.uuid AS payment_request_uuid,
    payment.invoice_number,
    payment.status AS payment_request_status,
    payment.payment_reference,
    l.crypto_payment_request_id,
    crypto_request.uuid AS crypto_payment_request_uuid,
    crypto_request.status AS crypto_payment_request_status,
    crypto_request.coin,
    crypto_request.network,
    crypto_request.provider_request_id,
    l.deposit_receipt_uuid,
    receipt.status AS deposit_receipt_status,
    receipt.status_reason AS deposit_receipt_status_reason,
    receipt.reviewer_id AS deposit_receipt_reviewer_id    
FROM ledger AS l
JOIN customers AS c
  ON c.id = l.customer_id
JOIN wallets AS w
  ON w.id = l.wallet_id
LEFT JOIN campaigns AS campaign
  ON campaign.id = l.campaign_id
LEFT JOIN payment_requests AS payment
  ON payment.id = l.payment_request_id
 AND payment.deleted_at IS NULL
LEFT JOIN crypto_payment_requests AS crypto_request
  ON crypto_request.id = l.crypto_payment_request_id
 AND crypto_request.deleted_at IS NULL
LEFT JOIN deposit_receipts AS receipt
  ON receipt.uuid::text = l.deposit_receipt_uuid
 AND receipt.deleted_at IS NULL
ORDER BY l.created_at, l.id;
