-- Restore the former per-audience advisory-lock behavior when rolling back
-- migration 0149.

BEGIN;

CREATE OR REPLACE FUNCTION guard_bundle_audience_claim()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    owner_campaign_id INTEGER;
BEGIN
    IF TG_TABLE_NAME IN ('campaign_targeting_test_sample_reservations', 'campaign_targeting_execution_reservations') THEN
        IF NEW.state <> 'active' THEN
            RETURN NEW;
        END IF;
        PERFORM pg_advisory_xact_lock(hashtextextended(NEW.bundle_id::text || ':' || NEW.audience_id::text, 0));
        IF EXISTS (
            SELECT 1 FROM bundle_audience_selection_members AS member
            JOIN bundle_audience_selections AS selection ON selection.id = member.selection_id
            WHERE member.bundle_id = NEW.bundle_id AND member.audience_id = NEW.audience_id
              AND selection.campaign_id <> NEW.campaign_id
        ) OR EXISTS (
            SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved
            WHERE test_reserved.bundle_id = NEW.bundle_id AND test_reserved.audience_id = NEW.audience_id
              AND test_reserved.state = 'active' AND test_reserved.campaign_id <> NEW.campaign_id
        ) OR EXISTS (
            SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved
            WHERE execution_reserved.bundle_id = NEW.bundle_id AND execution_reserved.audience_id = NEW.audience_id
              AND execution_reserved.state = 'active' AND execution_reserved.campaign_id <> NEW.campaign_id
        ) THEN
            RAISE EXCEPTION 'bundle audience claim conflict for bundle %, audience %', NEW.bundle_id, NEW.audience_id
                USING ERRCODE = '23505';
        END IF;
        RETURN NEW;
    END IF;

    SELECT selection.campaign_id INTO owner_campaign_id
    FROM bundle_audience_selections AS selection
    WHERE selection.id = NEW.selection_id;
    IF owner_campaign_id IS NULL THEN
        RAISE EXCEPTION 'bundle audience selection % has no campaign owner', NEW.selection_id USING ERRCODE = '23503';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.bundle_id::text || ':' || NEW.audience_id::text, 0));
    IF EXISTS (
        SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved
        WHERE test_reserved.bundle_id = NEW.bundle_id AND test_reserved.audience_id = NEW.audience_id
          AND test_reserved.state = 'active' AND test_reserved.campaign_id <> owner_campaign_id
    ) OR EXISTS (
        SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved
        WHERE execution_reserved.bundle_id = NEW.bundle_id AND execution_reserved.audience_id = NEW.audience_id
          AND execution_reserved.state = 'active' AND execution_reserved.campaign_id <> NEW.campaign_id
    ) THEN
        RAISE EXCEPTION 'bundle audience claim conflict for bundle %, audience %', NEW.bundle_id, NEW.audience_id
            USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
