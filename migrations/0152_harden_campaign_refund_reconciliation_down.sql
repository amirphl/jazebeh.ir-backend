DROP INDEX CONCURRENTLY IF EXISTS uq_transactions_campaign_partial_refund_completed;

BEGIN;
DROP FUNCTION IF EXISTS yamata_try_timestamptz(TEXT);
DROP TABLE IF EXISTS campaign_refund_reconciliation_scheduler_state;
COMMIT;
