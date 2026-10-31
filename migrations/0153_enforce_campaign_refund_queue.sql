-- Ensure every transition to executed atomically creates refund work.
BEGIN;

CREATE OR REPLACE FUNCTION yamata_enqueue_campaign_refund_reconciliation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    scheduled_at TIMESTAMPTZ;
BEGIN
    IF NEW.status <> 'executed' OR (TG_OP = 'UPDATE' AND OLD.status = 'executed') THEN
        RETURN NEW;
    END IF;

    scheduled_at := yamata_try_timestamptz(NEW.spec->>'schedule_at');
    INSERT INTO campaign_refund_reconciliation_jobs
        (campaign_id, state, eligible_at, attempt_count, error_code, error_message, created_at, updated_at)
    VALUES (
        NEW.id,
        CASE WHEN scheduled_at IS NULL THEN 'manual_review' ELSE 'pending' END,
        COALESCE(scheduled_at, CURRENT_TIMESTAMP),
        0,
        CASE WHEN scheduled_at IS NULL THEN 'CAMPAIGN_REFUND_SCHEDULE_INVALID' END,
        CASE WHEN scheduled_at IS NULL THEN 'Executed campaign has no valid schedule_at timestamp' END,
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    )
    ON CONFLICT (campaign_id) DO NOTHING;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS campaign_enqueue_refund_reconciliation_on_executed ON campaigns;
CREATE TRIGGER campaign_enqueue_refund_reconciliation_on_executed
AFTER INSERT OR UPDATE OF status ON campaigns
FOR EACH ROW
EXECUTE FUNCTION yamata_enqueue_campaign_refund_reconciliation();

COMMIT;

-- Historical discovery scans only executed rows. This partial index prevents
-- a large table of non-executed campaigns from delaying refund recovery.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_campaigns_executed_refund_backfill
    ON campaigns (id)
    WHERE status = 'executed';
