BEGIN;

-- Serves the fixed newest-first action-file API ordering without a sort step.
CREATE INDEX IF NOT EXISTS idx_bundle_action_files_bundle_created_desc
    ON bundle_action_files (bundle_id, created_at DESC, id DESC);

COMMIT;
