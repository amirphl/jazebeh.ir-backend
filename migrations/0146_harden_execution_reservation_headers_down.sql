BEGIN;

DROP TRIGGER IF EXISTS trg_guard_execution_reservation_member_immutable ON campaign_targeting_execution_reservations;
DROP TRIGGER IF EXISTS trg_guard_execution_reservation_header_immutable ON campaign_targeting_execution_reservation_headers;
DROP FUNCTION IF EXISTS guard_execution_reservation_member_immutable();
DROP FUNCTION IF EXISTS guard_execution_reservation_header_immutable();
ALTER TABLE campaign_targeting_execution_reservations
    DROP CONSTRAINT IF EXISTS fk_execution_reservation_member_header_owner;
ALTER TABLE campaign_targeting_execution_reservation_headers
    DROP CONSTRAINT IF EXISTS uq_execution_reservation_header_owner;
ALTER TABLE campaign_targeting_execution_reservation_headers
    DROP CONSTRAINT IF EXISTS campaign_targeting_execution_reservation_headers_selection_input_version_check,
    DROP CONSTRAINT IF EXISTS campaign_targeting_execution_reservation_headers_allocation_fingerprint_version_check,
    DROP COLUMN IF EXISTS selection_input_version,
    DROP COLUMN IF EXISTS allocation_fingerprint_version;

COMMIT;
