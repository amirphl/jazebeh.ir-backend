-- Harden the durable campaign-refund worker: bounded one-pass backfill,
-- safe parsing of legacy schedule JSON, and database-enforced idempotency.
BEGIN;

CREATE TABLE IF NOT EXISTS campaign_refund_reconciliation_scheduler_state (
    id SMALLINT PRIMARY KEY,
    last_campaign_id BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT campaign_refund_reconciliation_scheduler_state_singleton CHECK (id = 1),
    CONSTRAINT campaign_refund_reconciliation_scheduler_state_cursor_nonnegative CHECK (last_campaign_id >= 0)
);

INSERT INTO campaign_refund_reconciliation_scheduler_state (id)
VALUES (1)
ON CONFLICT (id) DO NOTHING;

-- PostgreSQL does not provide TRY_CAST. A malformed legacy schedule must not
-- abort discovery for every other customer; callers receive NULL instead.
CREATE OR REPLACE FUNCTION yamata_try_timestamptz(value TEXT)
RETURNS TIMESTAMPTZ
LANGUAGE plpgsql
STABLE
AS $$
BEGIN
    RETURN value::timestamptz;
EXCEPTION WHEN OTHERS THEN
    RETURN NULL;
END;
$$;

-- Do not silently choose a winner if an old deployment already double-credited
-- a campaign. Stop the migration so the financial discrepancy is investigated.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM transactions
        WHERE type = 'refund'
          AND status = 'completed'
          AND metadata->>'source' = 'campaign_partial_refund'
          AND metadata->>'operation' = 'partial_undelivered_messages_refund'
        GROUP BY metadata->>'campaign_id'
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot create campaign partial-refund idempotency index: duplicate completed refunds exist; reconcile them before migration';
    END IF;
END;
$$;

COMMIT;

-- This large historical table is written by financial flows. Build without
-- blocking inserts; run_all_up executes each migration outside a wrapper txn.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_transactions_campaign_partial_refund_completed
    ON transactions ((metadata->>'campaign_id'))
    WHERE type = 'refund'
      AND status = 'completed'
      AND metadata->>'source' = 'campaign_partial_refund'
      AND metadata->>'operation' = 'partial_undelivered_messages_refund';
