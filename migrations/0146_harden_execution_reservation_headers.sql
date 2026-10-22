-- Make the execution reservation contract self-describing and immutable even
-- for direct SQL writers. These versions are deliberately separate from the
-- outer reservation schema: input-hash and allocation-fingerprint algorithms
-- evolve independently.
BEGIN;

ALTER TABLE campaign_targeting_execution_reservation_headers
    ADD COLUMN IF NOT EXISTS selection_input_version INTEGER NOT NULL DEFAULT 5,
    ADD COLUMN IF NOT EXISTS allocation_fingerprint_version INTEGER NOT NULL DEFAULT 3;

ALTER TABLE campaign_targeting_execution_reservation_headers
    DROP CONSTRAINT IF EXISTS campaign_targeting_execution_reservation_headers_selection_input_version_check,
    ADD CONSTRAINT campaign_targeting_execution_reservation_headers_selection_input_version_check
        CHECK (selection_input_version > 0),
    DROP CONSTRAINT IF EXISTS campaign_targeting_execution_reservation_headers_allocation_fingerprint_version_check,
    ADD CONSTRAINT campaign_targeting_execution_reservation_headers_allocation_fingerprint_version_check
        CHECK (allocation_fingerprint_version > 0);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'campaign_targeting_execution_reservation_headers'::regclass
          AND conname = 'uq_execution_reservation_header_owner'
    ) THEN
        ALTER TABLE campaign_targeting_execution_reservation_headers
            ADD CONSTRAINT uq_execution_reservation_header_owner
                UNIQUE (id, campaign_id, bundle_id);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'campaign_targeting_execution_reservations'::regclass
          AND conname = 'fk_execution_reservation_member_header_owner'
    ) THEN
        ALTER TABLE campaign_targeting_execution_reservations
            ADD CONSTRAINT fk_execution_reservation_member_header_owner
                FOREIGN KEY (header_id, campaign_id, bundle_id)
                REFERENCES campaign_targeting_execution_reservation_headers (id, campaign_id, bundle_id)
                ON UPDATE RESTRICT ON DELETE RESTRICT;
    END IF;
END $$;

CREATE OR REPLACE FUNCTION guard_execution_reservation_header_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.campaign_id IS DISTINCT FROM OLD.campaign_id
       OR NEW.bundle_id IS DISTINCT FROM OLD.bundle_id
       OR NEW.phase IS DISTINCT FROM OLD.phase
       OR NEW.reservation_version IS DISTINCT FROM OLD.reservation_version
       OR NEW.requested_audience_count IS DISTINCT FROM OLD.requested_audience_count
       OR NEW.candidate_generation IS DISTINCT FROM OLD.candidate_generation
       OR NEW.selection_input_version IS DISTINCT FROM OLD.selection_input_version
       OR NEW.selection_input_hash IS DISTINCT FROM OLD.selection_input_hash
       OR NEW.allocation_fingerprint_version IS DISTINCT FROM OLD.allocation_fingerprint_version
       OR NEW.allocation_fingerprint IS DISTINCT FROM OLD.allocation_fingerprint
       OR NEW.request_snapshot IS DISTINCT FROM OLD.request_snapshot
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'execution reservation header selection metadata is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.state <> 'active' OR NEW.state NOT IN ('materialized', 'released', 'stale') THEN
        RAISE EXCEPTION 'invalid execution reservation header lifecycle transition % -> %', OLD.state, NEW.state
            USING ERRCODE = '55000';
    END IF;
    IF (NEW.state = 'materialized' AND (NEW.materialized_at IS NULL OR NEW.released_at IS NOT NULL))
       OR (NEW.state IN ('released', 'stale') AND NEW.released_at IS NULL) THEN
        RAISE EXCEPTION 'invalid execution reservation header lifecycle timestamps'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_execution_reservation_member_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.header_id IS DISTINCT FROM OLD.header_id
       OR NEW.campaign_id IS DISTINCT FROM OLD.campaign_id
       OR NEW.bundle_id IS DISTINCT FROM OLD.bundle_id
       OR NEW.audience_id IS DISTINCT FROM OLD.audience_id
       OR NEW.assigned_tag_id IS DISTINCT FROM OLD.assigned_tag_id
       OR NEW.selection_order IS DISTINCT FROM OLD.selection_order
       OR NEW.audience_score IS DISTINCT FROM OLD.audience_score
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'execution reservation member selection metadata is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.state <> 'active' OR NEW.state NOT IN ('materialized', 'released') THEN
        RAISE EXCEPTION 'invalid execution reservation member lifecycle transition % -> %', OLD.state, NEW.state
            USING ERRCODE = '55000';
    END IF;
    IF (NEW.state = 'materialized' AND (NEW.materialized_at IS NULL OR NEW.released_at IS NOT NULL))
       OR (NEW.state = 'released' AND NEW.released_at IS NULL) THEN
        RAISE EXCEPTION 'invalid execution reservation member lifecycle timestamps'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_guard_execution_reservation_header_immutable
    ON campaign_targeting_execution_reservation_headers;
CREATE TRIGGER trg_guard_execution_reservation_header_immutable
BEFORE UPDATE ON campaign_targeting_execution_reservation_headers
FOR EACH ROW EXECUTE FUNCTION guard_execution_reservation_header_immutable();

DROP TRIGGER IF EXISTS trg_guard_execution_reservation_member_immutable
    ON campaign_targeting_execution_reservations;
CREATE TRIGGER trg_guard_execution_reservation_member_immutable
BEFORE UPDATE ON campaign_targeting_execution_reservations
FOR EACH ROW EXECUTE FUNCTION guard_execution_reservation_member_immutable();

COMMIT;
