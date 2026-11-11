BEGIN;

CREATE TABLE IF NOT EXISTS campaign_targeting_execution_calculations (
    id BIGSERIAL PRIMARY KEY,
    campaign_id INTEGER NOT NULL REFERENCES campaigns(id),
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    requested_audience_count BIGINT NOT NULL CHECK (requested_audience_count > 0),
    selected_tag_ids BIGINT[] NOT NULL CHECK (cardinality(selected_tag_ids) > 0),
    selected_score_classes TEXT[] NOT NULL,
    selection_input_hash CHAR(64) NOT NULL CHECK (selection_input_hash ~ '^[0-9a-f]{64}$'),
    request_snapshot JSONB NOT NULL,
    allocation_fingerprint CHAR(64) NOT NULL DEFAULT REPEAT('0', 64)
        CHECK (allocation_fingerprint ~ '^[0-9a-f]{64}$'),
    calculation_version INTEGER NOT NULL CHECK (calculation_version > 0),
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'ready', 'failed', 'stale', 'committed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    committed_at TIMESTAMPTZ,
    error_code VARCHAR(128),
    error_message TEXT
);

CREATE TABLE IF NOT EXISTS campaign_targeting_execution_calculation_members (
    id BIGSERIAL PRIMARY KEY,
    calculation_id BIGINT NOT NULL REFERENCES campaign_targeting_execution_calculations(id) ON DELETE CASCADE,
    audience_id BIGINT NOT NULL,
    assigned_tag_id INTEGER NOT NULL,
    selection_order BIGINT NOT NULL CHECK (selection_order >= 0),
    audience_score NUMERIC,
    CONSTRAINT uk_execution_calculation_member UNIQUE (calculation_id, audience_id),
    CONSTRAINT uk_execution_calculation_order UNIQUE (calculation_id, selection_order)
);

CREATE INDEX IF NOT EXISTS idx_execution_calculation_campaign_created
    ON campaign_targeting_execution_calculations (campaign_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_execution_calculation_campaign_status
    ON campaign_targeting_execution_calculations (campaign_id, status, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uk_execution_calculation_one_pending
    ON campaign_targeting_execution_calculations (campaign_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_execution_calculation_pending
    ON campaign_targeting_execution_calculations (started_at ASC, created_at ASC, id ASC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_execution_calculation_committed_bundle
    ON campaign_targeting_execution_calculations (bundle_id, campaign_id, id) WHERE status = 'committed';
-- A committed calculation is queried as an active execution reservation by
-- audience ID during every subsequent candidate scan.
CREATE INDEX IF NOT EXISTS idx_execution_calculation_member_audience
    ON campaign_targeting_execution_calculation_members (audience_id, calculation_id);

COMMIT;
