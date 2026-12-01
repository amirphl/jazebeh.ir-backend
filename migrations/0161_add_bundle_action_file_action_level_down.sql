BEGIN;

ALTER TABLE bundle_action_files
    DROP COLUMN IF EXISTS action_level;

COMMIT;
