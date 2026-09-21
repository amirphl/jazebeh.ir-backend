BEGIN;

ALTER TABLE tag_overall_performance_summaries
    DROP CONSTRAINT IF EXISTS tag_overall_performance_summaries_bundle_id_fkey;

ALTER TABLE tag_overall_performance_summaries
    ADD CONSTRAINT tag_overall_performance_summaries_bundle_id_fkey
    FOREIGN KEY (bundle_id) REFERENCES bundles(id);

COMMIT;
