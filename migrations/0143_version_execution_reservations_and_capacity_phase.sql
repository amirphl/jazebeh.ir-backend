-- Versioned execution snapshots distinguish explicitly supported legacy
-- scheduler fallback from every campaign finalized after this migration.

BEGIN;

ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS smart_targeting_execution_reservation_version INTEGER NOT NULL DEFAULT 0;

ALTER TABLE campaign_targeting_capacity_calculations
    ADD COLUMN IF NOT EXISTS phase VARCHAR(16) NOT NULL DEFAULT 'execution';

-- Old calculations are intentionally invalidated by the v5 input hash, but
-- retaining their actual phase keeps audits truthful.
UPDATE campaign_targeting_capacity_calculations AS calculation
SET phase = campaigns.phase::text
FROM campaigns
WHERE campaigns.id = calculation.campaign_id;

CREATE TABLE IF NOT EXISTS campaign_targeting_execution_reservation_headers (
    id BIGSERIAL PRIMARY KEY,
    campaign_id INTEGER NOT NULL UNIQUE REFERENCES campaigns(id),
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    phase campaign_phase NOT NULL CHECK (phase = 'execution'),
    reservation_version INTEGER NOT NULL CHECK (reservation_version > 0),
    requested_audience_count BIGINT NOT NULL CHECK (requested_audience_count > 0),
    candidate_generation INTEGER NOT NULL CHECK (candidate_generation > 0),
    selection_input_hash CHAR(64) NOT NULL,
    allocation_fingerprint CHAR(64) NOT NULL,
    request_snapshot JSONB NOT NULL,
    state VARCHAR(16) NOT NULL CHECK (state IN ('active', 'released', 'materialized', 'stale')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    released_at TIMESTAMPTZ,
    materialized_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_execution_reservation_headers_bundle_state
    ON campaign_targeting_execution_reservation_headers (bundle_id, state);

ALTER TABLE campaign_targeting_execution_reservations
    ADD COLUMN IF NOT EXISTS header_id BIGINT NOT NULL REFERENCES campaign_targeting_execution_reservation_headers(id);

CREATE INDEX IF NOT EXISTS idx_execution_reservations_header_state
    ON campaign_targeting_execution_reservations (header_id, state);

COMMIT;
