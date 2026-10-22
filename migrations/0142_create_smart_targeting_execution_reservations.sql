-- Persist the concrete Smart Targeting Execution audience while the campaign
-- waits for approval/scheduling.  These rows are a releasable reservation;
-- the normal bundle ledger remains the permanent materialized allocation.

BEGIN;

CREATE TABLE IF NOT EXISTS campaign_targeting_execution_reservations (
    id BIGSERIAL PRIMARY KEY,
    campaign_id INTEGER NOT NULL REFERENCES campaigns(id),
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    audience_id BIGINT NOT NULL REFERENCES audience_profiles(id),
    assigned_tag_id INTEGER NOT NULL REFERENCES tags(id),
    selection_order BIGINT NOT NULL CHECK (selection_order >= 0),
    audience_score NUMERIC,
    state VARCHAR(16) NOT NULL CHECK (state IN ('active', 'released', 'materialized')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    released_at TIMESTAMPTZ,
    materialized_at TIMESTAMPTZ,
    CONSTRAINT uk_campaign_targeting_execution_reservation_member UNIQUE (campaign_id, audience_id),
    CONSTRAINT uk_campaign_targeting_execution_reservation_order UNIQUE (campaign_id, selection_order)
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_campaign_targeting_execution_reservation_active_audience
    ON campaign_targeting_execution_reservations (bundle_id, audience_id)
    WHERE state = 'active';
CREATE INDEX IF NOT EXISTS idx_campaign_targeting_execution_reservations_campaign_state
    ON campaign_targeting_execution_reservations (campaign_id, state);

COMMIT;
