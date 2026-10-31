DROP TRIGGER IF EXISTS campaign_enqueue_refund_reconciliation_on_executed ON campaigns;
DROP FUNCTION IF EXISTS yamata_enqueue_campaign_refund_reconciliation();

DROP INDEX CONCURRENTLY IF EXISTS idx_campaigns_executed_refund_backfill;
