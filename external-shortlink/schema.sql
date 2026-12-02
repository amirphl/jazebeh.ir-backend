BEGIN;

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
CREATE INDEX IF NOT EXISTS idx_clicks_clicked_at ON clicks (clicked_at);
CREATE INDEX IF NOT EXISTS idx_clicks_acknowledged_at
    ON clicks (acknowledged_at)
    WHERE acknowledged_at IS NOT NULL;
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

COMMIT;
