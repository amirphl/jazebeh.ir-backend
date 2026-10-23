-- A scheduler can lose the allocation response after PostgreSQL and the
-- redirect service have committed. Preserve one ordered allocation per exact
-- request so a retry can return/re-publish the existing codes instead of
-- inserting another large batch.

BEGIN;

ALTER TABLE short_links
    ADD COLUMN IF NOT EXISTS allocation_key CHAR(64),
    ADD COLUMN IF NOT EXISTS allocation_position INTEGER;

CREATE UNIQUE INDEX IF NOT EXISTS uk_short_links_allocation_position
    ON short_links (allocation_key, allocation_position)
    WHERE allocation_key IS NOT NULL AND allocation_position IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_short_links_allocation_key
    ON short_links (allocation_key, allocation_position)
    WHERE allocation_key IS NOT NULL;

COMMIT;
