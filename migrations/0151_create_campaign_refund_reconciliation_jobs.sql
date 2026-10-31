-- Make under-delivery refunds durable, schedulable work rather than a side
-- effect of a customer's campaign-list request.
BEGIN;

CREATE TABLE IF NOT EXISTS campaign_refund_reconciliation_jobs (
    campaign_id BIGINT PRIMARY KEY REFERENCES campaigns(id) ON DELETE CASCADE,
    state VARCHAR(32) NOT NULL DEFAULT 'pending',
    eligible_at TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_code VARCHAR(64),
    error_message VARCHAR(1024),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT campaign_refund_reconciliation_job_state_valid CHECK (
        state IN ('pending', 'processing', 'retry', 'completed', 'manual_review')
    ),
    CONSTRAINT campaign_refund_reconciliation_job_attempt_nonnegative CHECK (attempt_count >= 0),
    CONSTRAINT campaign_refund_reconciliation_job_processing_lease CHECK (
        state <> 'processing' OR (started_at IS NOT NULL AND lease_expires_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_campaign_refund_reconciliation_jobs_claimable
    ON campaign_refund_reconciliation_jobs (state, next_attempt_at, eligible_at, campaign_id)
    WHERE state IN ('pending', 'retry', 'processing');

COMMIT;
