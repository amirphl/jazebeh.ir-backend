BEGIN;

DROP INDEX IF EXISTS idx_short_links_allocation_key;
DROP INDEX IF EXISTS uk_short_links_allocation_position;
ALTER TABLE short_links
    DROP COLUMN IF EXISTS allocation_position,
    DROP COLUMN IF EXISTS allocation_key;

COMMIT;
