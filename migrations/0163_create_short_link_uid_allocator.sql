-- Replace the historical 4-5 character generator with one durable,
-- fixed-width 6-character base-36 allocator.  The production namespace has
-- already reached zzovu, so inspect from 100000 and intentionally retire the
-- remaining 5-character values rather than creating a second representation
-- such as 0zzovv for a legacy token. next_value is a scan cursor, not a
-- promise that its corresponding UID is free: ReserveSequentialUIDs checks
-- short_links in the same transaction and skips any historical six-character
-- collision (including 100000) before inserting a new mapping.

BEGIN;

CREATE TABLE IF NOT EXISTS short_link_uid_allocator (
    allocator_name TEXT PRIMARY KEY,
    next_value BIGINT NOT NULL CHECK (next_value >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO short_link_uid_allocator (allocator_name, next_value)
VALUES ('default', 60466176)
ON CONFLICT (allocator_name) DO NOTHING;

COMMIT;
