-- A committed execution calculation is an active audience claim even though
-- its members are stored separately from the legacy execution-reservation
-- table. Extend the Bundle-scoped Smart Targeting claim guard so every Smart
-- write path preserves one-audience-per-Bundle semantics. Excel allocations
-- are intentionally independent.

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
        PERFORM pg_advisory_xact_lock(845172, NEW.bundle_id);
        IF EXISTS (
            SELECT 1 FROM bundle_audience_selection_members AS member
            JOIN bundle_audience_selections AS selection ON selection.id = member.selection_id
            JOIN campaigns AS campaign ON campaign.id = selection.campaign_id
            WHERE member.bundle_id = NEW.bundle_id AND member.audience_id = NEW.audience_id
              AND selection.campaign_id <> NEW.campaign_id
              AND LOWER(BTRIM(COALESCE(campaign.spec->>'audience_targeting_method', ''))) <> 'excel'
        ) OR EXISTS (
            SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved
            WHERE test_reserved.bundle_id = NEW.bundle_id AND test_reserved.audience_id = NEW.audience_id
              AND test_reserved.state = 'active' AND test_reserved.campaign_id <> NEW.campaign_id
        ) OR EXISTS (
            SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved
            WHERE execution_reserved.bundle_id = NEW.bundle_id AND execution_reserved.audience_id = NEW.audience_id
              AND execution_reserved.state = 'active' AND execution_reserved.campaign_id <> NEW.campaign_id
        ) OR EXISTS (
            SELECT 1
            FROM campaign_targeting_execution_calculation_members AS committed_member
            JOIN campaign_targeting_execution_calculations AS committed
              ON committed.id = committed_member.calculation_id
            WHERE committed.bundle_id = NEW.bundle_id AND committed_member.audience_id = NEW.audience_id
              AND committed.status = 'committed' AND committed.campaign_id <> NEW.campaign_id
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
    PERFORM pg_advisory_xact_lock(845172, NEW.bundle_id);
    IF EXISTS (
        SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved
        WHERE test_reserved.bundle_id = NEW.bundle_id AND test_reserved.audience_id = NEW.audience_id
          AND test_reserved.state = 'active' AND test_reserved.campaign_id <> owner_campaign_id
    ) OR EXISTS (
        SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved
        WHERE execution_reserved.bundle_id = NEW.bundle_id AND execution_reserved.audience_id = NEW.audience_id
          AND execution_reserved.state = 'active' AND execution_reserved.campaign_id <> owner_campaign_id
    ) OR EXISTS (
        SELECT 1
        FROM campaign_targeting_execution_calculation_members AS committed_member
        JOIN campaign_targeting_execution_calculations AS committed
          ON committed.id = committed_member.calculation_id
        WHERE committed.bundle_id = NEW.bundle_id AND committed_member.audience_id = NEW.audience_id
          AND committed.status = 'committed' AND committed.campaign_id <> owner_campaign_id
    ) THEN
        RAISE EXCEPTION 'bundle audience claim conflict for bundle %, audience %', NEW.bundle_id, NEW.audience_id
            USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_committed_execution_calculation_claim()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status <> 'committed' OR OLD.status = 'committed' THEN
        RETURN NEW;
    END IF;

    PERFORM pg_advisory_xact_lock(845172, NEW.bundle_id);
    IF EXISTS (
        SELECT 1
        FROM campaign_targeting_execution_calculation_members AS member
        WHERE member.calculation_id = NEW.id
          AND (
              EXISTS (SELECT 1 FROM bundle_audience_selection_members AS materialized
                      JOIN bundle_audience_selections AS materialized_selection ON materialized_selection.id = materialized.selection_id
                      JOIN campaigns AS materialized_campaign ON materialized_campaign.id = materialized_selection.campaign_id
                      WHERE materialized.bundle_id = NEW.bundle_id AND materialized.audience_id = member.audience_id
                        AND LOWER(BTRIM(COALESCE(materialized_campaign.spec->>'audience_targeting_method', ''))) <> 'excel')
              OR EXISTS (SELECT 1 FROM bundle_audience_exclusions AS excluded
                         WHERE excluded.bundle_id = NEW.bundle_id AND excluded.audience_id = member.audience_id)
              OR EXISTS (SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved
                         WHERE test_reserved.bundle_id = NEW.bundle_id AND test_reserved.audience_id = member.audience_id
                           AND test_reserved.state = 'active' AND test_reserved.campaign_id <> NEW.campaign_id)
              OR EXISTS (SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved
                         WHERE execution_reserved.bundle_id = NEW.bundle_id AND execution_reserved.audience_id = member.audience_id
                           AND execution_reserved.state = 'active' AND execution_reserved.campaign_id <> NEW.campaign_id)
              OR EXISTS (
                  SELECT 1
                  FROM campaign_targeting_execution_calculation_members AS committed_member
                  JOIN campaign_targeting_execution_calculations AS committed
                    ON committed.id = committed_member.calculation_id
                  WHERE committed_member.audience_id = member.audience_id
                    AND committed.bundle_id = NEW.bundle_id AND committed.status = 'committed'
                    AND committed.id <> NEW.id
              )
          )
    ) THEN
        RAISE EXCEPTION 'committed execution calculation conflicts with an existing Bundle audience claim'
            USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_guard_committed_execution_calculation_claim
    ON campaign_targeting_execution_calculations;
CREATE TRIGGER trg_guard_committed_execution_calculation_claim
BEFORE UPDATE OF status ON campaign_targeting_execution_calculations
FOR EACH ROW EXECUTE FUNCTION guard_committed_execution_calculation_claim();

COMMIT;
