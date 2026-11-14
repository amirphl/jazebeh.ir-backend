BEGIN;

ALTER TABLE bundles ADD COLUMN IF NOT EXISTS action_data_updated_at TIMESTAMPTZ NULL;

CREATE TABLE IF NOT EXISTS bundle_action_files (
    id BIGSERIAL PRIMARY KEY,
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    original_file_name VARCHAR(255) NOT NULL,
    storage_path TEXT NOT NULL,
    content_sha256 CHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    total_row_count BIGINT NOT NULL DEFAULT 0,
    unique_uid_count BIGINT NOT NULL DEFAULT 0,
    new_action_uid_count BIGINT NOT NULL DEFAULT 0,
    duplicate_in_file_count BIGINT NOT NULL DEFAULT 0,
    duplicate_in_other_files_count BIGINT NOT NULL DEFAULT 0,
    invalid_uid_count BIGINT NOT NULL DEFAULT 0,
    outside_bundle_count BIGINT NOT NULL DEFAULT 0,
    unassigned_tag_count BIGINT NOT NULL DEFAULT 0,
    missing_delivery_count BIGINT NOT NULL DEFAULT 0,
    eligible_action_uid_count BIGINT NOT NULL DEFAULT 0,
    uploaded_by_customer_id INTEGER NOT NULL REFERENCES customers(id),
    deleted_by_customer_id INTEGER NULL REFERENCES customers(id),
    started_at TIMESTAMPTZ NULL,
    processed_at TIMESTAMPTZ NULL,
    deleted_at TIMESTAMPTZ NULL,
    error_code VARCHAR(64) NULL,
    error_message VARCHAR(255) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT bundle_action_files_status_valid CHECK (status IN ('pending','processing','processed','failed','delete_pending','deleted'))
);
CREATE INDEX IF NOT EXISTS idx_bundle_action_files_bundle_status ON bundle_action_files(bundle_id, status);
CREATE INDEX IF NOT EXISTS idx_bundle_action_files_active ON bundle_action_files(bundle_id, id) WHERE status = 'processed';
CREATE INDEX IF NOT EXISTS idx_bundle_action_files_pending ON bundle_action_files(created_at, id) WHERE status IN ('pending','delete_pending');

CREATE TABLE IF NOT EXISTS bundle_action_file_uids (
    id BIGSERIAL PRIMARY KEY,
    bundle_action_file_id BIGINT NOT NULL REFERENCES bundle_action_files(id),
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    uid VARCHAR(255) NOT NULL,
    campaign_id INTEGER NULL REFERENCES campaigns(id),
    assigned_tag_id BIGINT NULL REFERENCES tags(id),
    phase_type campaign_phase NULL,
    validation_status VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uk_bundle_action_file_uid UNIQUE (bundle_action_file_id, uid)
);
CREATE INDEX IF NOT EXISTS idx_bundle_action_file_uids_bundle_uid ON bundle_action_file_uids(bundle_id, uid);
CREATE INDEX IF NOT EXISTS idx_bundle_action_file_uids_campaign ON bundle_action_file_uids(campaign_id);
CREATE INDEX IF NOT EXISTS idx_bundle_action_file_uids_tag_phase ON bundle_action_file_uids(assigned_tag_id, phase_type);

CREATE TABLE IF NOT EXISTS bundle_action_summaries (
    bundle_id INTEGER PRIMARY KEY REFERENCES bundles(id),
    has_active_action_files BOOLEAN NOT NULL,
    action_count BIGINT NOT NULL DEFAULT 0,
    eligible_delivered_count BIGINT NOT NULL DEFAULT 0,
    bundle_avg_atr NUMERIC GENERATED ALWAYS AS (CASE WHEN NOT has_active_action_files OR eligible_delivered_count = 0 THEN NULL ELSE action_count::NUMERIC / eligible_delivered_count::NUMERIC END) STORED,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT bundle_action_summary_counts_valid CHECK (action_count >= 0 AND eligible_delivered_count >= 0 AND action_count <= eligible_delivered_count)
);
CREATE TABLE IF NOT EXISTS bundle_action_campaign_metrics (
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    campaign_id INTEGER NOT NULL REFERENCES campaigns(id),
    action_count BIGINT NOT NULL DEFAULT 0,
    eligible_delivered_count BIGINT NOT NULL DEFAULT 0,
    campaign_atr NUMERIC GENERATED ALWAYS AS (CASE WHEN eligible_delivered_count = 0 THEN NULL ELSE action_count::NUMERIC / eligible_delivered_count::NUMERIC END) STORED,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(bundle_id, campaign_id),
    CONSTRAINT bundle_action_campaign_counts_valid CHECK (action_count >= 0 AND eligible_delivered_count >= 0 AND action_count <= eligible_delivered_count)
);
CREATE TABLE IF NOT EXISTS bundle_action_tag_metrics (
    bundle_id INTEGER NOT NULL REFERENCES bundles(id),
    tag_id BIGINT NOT NULL REFERENCES tags(id),
    test_action_count BIGINT NOT NULL DEFAULT 0,
    test_eligible_delivered_count BIGINT NOT NULL DEFAULT 0,
    test_phase_avg_atr NUMERIC GENERATED ALWAYS AS (CASE WHEN test_eligible_delivered_count = 0 THEN NULL ELSE test_action_count::NUMERIC / test_eligible_delivered_count::NUMERIC END) STORED,
    overall_action_count BIGINT NOT NULL DEFAULT 0,
    overall_eligible_delivered_count BIGINT NOT NULL DEFAULT 0,
    overall_avg_atr NUMERIC GENERATED ALWAYS AS (CASE WHEN overall_eligible_delivered_count = 0 THEN NULL ELSE overall_action_count::NUMERIC / overall_eligible_delivered_count::NUMERIC END) STORED,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(bundle_id, tag_id),
    CONSTRAINT bundle_action_tag_counts_valid CHECK (test_action_count >= 0 AND test_eligible_delivered_count >= 0 AND test_action_count <= test_eligible_delivered_count AND overall_action_count >= 0 AND overall_eligible_delivered_count >= 0 AND overall_action_count <= overall_eligible_delivered_count)
);
CREATE INDEX IF NOT EXISTS idx_bundle_action_tag_metrics_test_atr ON bundle_action_tag_metrics(bundle_id, test_phase_avg_atr DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS idx_bundle_action_tag_metrics_overall_atr ON bundle_action_tag_metrics(bundle_id, overall_avg_atr DESC NULLS LAST);

ALTER TABLE campaign_selected_tags ADD COLUMN IF NOT EXISTS test_phase_avg_atr_snapshot NUMERIC NULL;
ALTER TABLE campaign_selected_tags ADD COLUMN IF NOT EXISTS overall_avg_atr_snapshot NUMERIC NULL;
COMMIT;
