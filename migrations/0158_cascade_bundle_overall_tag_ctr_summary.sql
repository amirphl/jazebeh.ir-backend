-- Ensure the Bundle/tag CTR summary cannot block Bundle deletion. This also
-- repairs databases where 0157 was applied before the cascade was added.

BEGIN;

ALTER TABLE tag_overall_performance_summaries
    DROP CONSTRAINT IF EXISTS tag_overall_performance_summaries_bundle_id_fkey;

ALTER TABLE tag_overall_performance_summaries
    ADD CONSTRAINT tag_overall_performance_summaries_bundle_id_fkey
    FOREIGN KEY (bundle_id) REFERENCES bundles(id) ON DELETE CASCADE;

COMMIT;
