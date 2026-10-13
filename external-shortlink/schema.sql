-- Do not wrap this file in an explicit transaction. The legacy backfill below
-- can run for a long time; an outer transaction would retain the exclusive
-- locks acquired by ALTER TABLE and block normal redirect traffic until it
-- finishes. psql autocommit still makes the DO block itself atomic.

CREATE TABLE IF NOT EXISTS links (
    link_id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL UNIQUE,
    long_url VARCHAR(4096) NOT NULL,
    short_url VARCHAR(4096),
    source_link_id BIGINT,
    campaign_id BIGINT,
    client_id BIGINT,
    scenario_id BIGINT,
    scenario_name VARCHAR(512),
    phone_number VARCHAR(32),
    is_test BOOLEAN NOT NULL DEFAULT FALSE,
    source_created_at TIMESTAMPTZ,
    source_updated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT links_code_format CHECK (code ~ '^[A-Za-z0-9_-]{1,64}$'),
    CONSTRAINT links_code_not_reserved CHECK (LOWER(code) NOT IN ('api', 'healthz', 'readyz', 'metrics')),
    CONSTRAINT links_http_destination CHECK (long_url ~* '^https?://')
);

CREATE TABLE IF NOT EXISTS clicks (
    click_id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    short_code VARCHAR(64) NOT NULL,
    link_id BIGINT NOT NULL,
    long_url VARCHAR(4096) NOT NULL,
    short_url VARCHAR(4096),
    source_link_id BIGINT,
    campaign_id BIGINT,
    client_id BIGINT,
    scenario_id BIGINT,
    scenario_name VARCHAR(512),
    phone_number VARCHAR(32),
    is_test BOOLEAN NOT NULL DEFAULT FALSE,
    link_created_at TIMESTAMPTZ,
    link_updated_at TIMESTAMPTZ,
    clicked_at TIMESTAMPTZ NOT NULL,
    client_ip VARCHAR(64),
    user_agent VARCHAR(1024),
    referer VARCHAR(2048),
    acknowledged_at TIMESTAMPTZ,
    CONSTRAINT clicks_link_id_fkey FOREIGN KEY (link_id) REFERENCES links(link_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_clicks_link_id ON clicks (link_id);
-- Retention filters by timestamp and uses click_id only as a stable tie
-- breaker.  This prevents the purge job from scanning every recent row in
-- primary-key order before it reaches a retained row.  Keep these statements
-- outside an explicit transaction: CONCURRENTLY avoids blocking redirects.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_clicks_purge_expired
    ON clicks (clicked_at, click_id);
DROP INDEX CONCURRENTLY IF EXISTS idx_clicks_clicked_at;
-- Acknowledgement is represented by the singleton cursor below. Do not index
-- acknowledged_at: advancing a cursor would rewrite an index entry per click.
DROP INDEX IF EXISTS idx_clicks_acknowledged_at;
ALTER TABLE links
    ADD COLUMN IF NOT EXISTS is_test BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE clicks
    ADD COLUMN IF NOT EXISTS is_test BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- These legacy data migrations can scan every row in links and clicks. Record
-- completion so the idempotent schema import does not repeat them on deploy.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM schema_migrations
        WHERE name = '20260926_legacy_link_and_click_backfills'
    ) THEN
        -- Backfill campaign-test links created before the explicit flag existed.
        UPDATE links
        SET is_test = TRUE
        WHERE is_test = FALSE
          AND long_url ~ '(^|[^[:alnum:]_-])test-[0-9a-f]{8}([^[:alnum:]_-]|$)';

        UPDATE clicks AS click
        SET is_test = TRUE
        FROM links AS link
        WHERE click.link_id = link.link_id
          AND link.is_test = TRUE
          AND click.is_test = FALSE;

        UPDATE links
        SET short_url = 'https://' || short_url
        WHERE short_url ~ '^(jzbe\.ir|jo1n\.ir|joinsahel\.ir)/';

        UPDATE clicks
        SET short_url = 'https://' || short_url
        WHERE short_url ~ '^(jzbe\.ir|jo1n\.ir|joinsahel\.ir)/';

        INSERT INTO schema_migrations (name)
        VALUES ('20260926_legacy_link_and_click_backfills');
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS click_acknowledgements (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    through_click_id BIGINT NOT NULL DEFAULT 0,
    acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO click_acknowledgements(singleton, through_click_id)
VALUES (TRUE, 0)
ON CONFLICT (singleton) DO NOTHING;

-- New writes use daily partitions. The legacy clicks table stays read-only
-- during the online cutover and is included by the API until its acknowledged
-- retention window (EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS) has elapsed.
CREATE TABLE IF NOT EXISTS click_event_dedup (
    event_id UUID PRIMARY KEY,
    click_id BIGINT NOT NULL,
    clicked_at TIMESTAMPTZ NOT NULL
);

-- Upgrade installations that briefly ran the first PostgreSQL-only release
-- before click IDs were recorded in the deduplication table. The backfill is
-- below the partitioned table definition, because it reads clicks_v2.
ALTER TABLE click_event_dedup ADD COLUMN IF NOT EXISTS click_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_click_event_dedup_clicked_at
    ON click_event_dedup (clicked_at);

CREATE TABLE IF NOT EXISTS clicks_v2 (
    click_id BIGINT NOT NULL DEFAULT nextval('clicks_click_id_seq'),
    event_id UUID NOT NULL,
    short_code VARCHAR(64) NOT NULL,
    link_id BIGINT NOT NULL REFERENCES links(link_id) ON DELETE RESTRICT,
    long_url VARCHAR(4096) NOT NULL,
    short_url VARCHAR(4096),
    source_link_id BIGINT,
    campaign_id BIGINT,
    client_id BIGINT,
    scenario_id BIGINT,
    scenario_name VARCHAR(512),
    phone_number VARCHAR(32),
    is_test BOOLEAN NOT NULL DEFAULT FALSE,
    link_created_at TIMESTAMPTZ,
    link_updated_at TIMESTAMPTZ,
    clicked_at TIMESTAMPTZ NOT NULL,
    client_ip VARCHAR(64),
    user_agent VARCHAR(1024),
    referer VARCHAR(2048)
) PARTITION BY RANGE (clicked_at);

DO $$
DECLARE
    partition_day DATE;
    partition_name TEXT;
BEGIN
    -- Pre-create a two-week window. The installed maintenance timer extends it
    -- daily without granting the application runtime role DDL privileges.
    FOR partition_day IN SELECT generate_series(current_date - 8, current_date + 14, interval '1 day')::date LOOP
        partition_name := format('clicks_v2_%s', to_char(partition_day, 'YYYYMMDD'));
        BEGIN
            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS %I PARTITION OF clicks_v2 FOR VALUES FROM (%L) TO (%L)',
                partition_name, partition_day, partition_day + 1
            );
        EXCEPTION WHEN OTHERS THEN
            -- A default partition that already holds rows for this day refuses a
            -- new range for it. That is not fatal: the rows stay in the default
            -- partition, which serves clicks, and the maintenance timer relocates
            -- them. Aborting the whole schema import here would block a deploy.
            RAISE WARNING 'click partition % was not created: %', partition_name, SQLERRM;
        END;
    END LOOP;
END $$;

-- Safety net for the redirect path. A click insert must never fail because the
-- partition for its UTC day is missing: without this, one missed maintenance run
-- after the pre-created window is exhausted turns every redirect into a 503.
-- The maintenance timer keeps this partition empty and relocates any rows that
-- land in it; it is intentionally never dropped by retention.
CREATE TABLE IF NOT EXISTS clicks_v2_default PARTITION OF clicks_v2 DEFAULT;

CREATE INDEX IF NOT EXISTS idx_clicks_v2_click_id ON clicks_v2 (click_id);

UPDATE click_event_dedup AS dedup
SET click_id = click.click_id
FROM clicks_v2 AS click
WHERE click.event_id = dedup.event_id
  AND dedup.click_id IS NULL;
-- An orphaned dedup row would make an event retry look successful without a
-- click row. Refuse cutover instead of permitting that inconsistent state.
ALTER TABLE click_event_dedup ALTER COLUMN click_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uk_click_event_dedup_click_id
    ON click_event_dedup (click_id);

-- Cutover is one-way: the runtime may delete expired legacy rows, but it must
-- never create or modify them. An accidental old-binary restart then fails
-- loudly instead of silently mixing post-cutover writes into the legacy table.
CREATE OR REPLACE FUNCTION reject_legacy_click_write()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'legacy clicks table is read-only after partitioned click cutover';
END;
$$;

DROP TRIGGER IF EXISTS reject_legacy_click_write ON clicks;
CREATE TRIGGER reject_legacy_click_write
BEFORE INSERT OR UPDATE ON clicks
FOR EACH ROW EXECUTE FUNCTION reject_legacy_click_write();
