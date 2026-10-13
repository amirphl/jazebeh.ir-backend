-- Invalid external events must be retained without stopping the monotonic
-- import cursor.
--
-- Rows with a usable click ID are quarantined once per (source, ID), so a page
-- retry cannot create duplicates. A row whose ID is missing or non-positive has
-- no cursor value at all - that is the one case that can neither advance the
-- cursor nor be represented by the uniqueness below - so it is retained as-is
-- rather than dropped: it is the only surviving copy of an event produced by a
-- malformed or version-skewed response.
CREATE TABLE IF NOT EXISTS external_short_link_click_dead_letters (
    id BIGSERIAL PRIMARY KEY,
    source VARCHAR(64) NOT NULL,
    external_click_id BIGINT NOT NULL,
    payload JSONB NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Converge installs that already applied the first shape of this migration
-- (positive-only CHECK plus a full unique constraint) on the same schema as a
-- fresh install, so an unusable click ID can never fail the quarantine insert
-- and take the whole import page down with it.
ALTER TABLE external_short_link_click_dead_letters
    DROP CONSTRAINT IF EXISTS external_short_link_click_dead_letters_external_click_id_check;
ALTER TABLE external_short_link_click_dead_letters
    DROP CONSTRAINT IF EXISTS uk_external_short_link_dead_letters_source_click;

CREATE UNIQUE INDEX IF NOT EXISTS uk_external_short_link_dead_letters_source_click
    ON external_short_link_click_dead_letters (source, external_click_id)
    WHERE external_click_id > 0;

CREATE INDEX IF NOT EXISTS idx_external_short_link_dead_letters_created_at
    ON external_short_link_click_dead_letters (created_at DESC);
