BEGIN;

DROP INDEX IF EXISTS idx_execution_reservations_header_state;
ALTER TABLE campaign_targeting_execution_reservations DROP COLUMN IF EXISTS header_id;
DROP INDEX IF EXISTS idx_execution_reservation_headers_bundle_state;
DROP TABLE IF EXISTS campaign_targeting_execution_reservation_headers;
ALTER TABLE campaign_targeting_capacity_calculations DROP COLUMN IF EXISTS phase;
ALTER TABLE campaigns DROP COLUMN IF EXISTS smart_targeting_execution_reservation_version;

COMMIT;
