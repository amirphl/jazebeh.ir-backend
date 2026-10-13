-- An Excel upload is an explicit campaign input.  Several Excel campaigns in
-- the same Bundle may therefore contain the same audience. Excel allocation is
-- independent of Smart Targeting: neither feature blocks the other. Smart
-- Targeting claims remain exclusive only against other non-Excel allocations.

BEGIN;

ALTER TABLE bundle_audience_selection_members
    DROP CONSTRAINT IF EXISTS uk_bundle_aud_sel_member_bundle_audience;
DROP INDEX IF EXISTS uk_bundle_aud_sel_member_bundle_audience;
CREATE INDEX IF NOT EXISTS idx_bundle_aud_sel_member_bundle_audience
    ON bundle_audience_selection_members (bundle_id, audience_id);

CREATE OR REPLACE FUNCTION guard_bundle_audience_claim()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    owner_campaign_id INTEGER;
    owner_is_excel BOOLEAN;
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

    SELECT selection.campaign_id,
           LOWER(BTRIM(COALESCE(campaign.spec->>'audience_targeting_method', ''))) = 'excel'
      INTO owner_campaign_id, owner_is_excel
    FROM bundle_audience_selections AS selection
    JOIN campaigns AS campaign ON campaign.id = selection.campaign_id
    WHERE selection.id = NEW.selection_id;
    IF owner_campaign_id IS NULL THEN
        RAISE EXCEPTION 'bundle audience selection % has no campaign owner', NEW.selection_id USING ERRCODE = '23503';
    END IF;
    IF owner_is_excel THEN
        RETURN NEW;
    END IF;

    PERFORM pg_advisory_xact_lock(845172, NEW.bundle_id);
    IF EXISTS (
        SELECT 1
        FROM bundle_audience_selection_members AS member
        JOIN bundle_audience_selections AS selection ON selection.id = member.selection_id
        JOIN campaigns AS campaign ON campaign.id = selection.campaign_id
        WHERE member.bundle_id = NEW.bundle_id AND member.audience_id = NEW.audience_id
          AND selection.campaign_id <> owner_campaign_id
          AND LOWER(BTRIM(COALESCE(campaign.spec->>'audience_targeting_method', ''))) <> 'excel'
    ) OR EXISTS (
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

COMMIT;
