BEGIN;

DROP INDEX IF EXISTS idx_campaigns_bundle_capacity_reservations;
CREATE INDEX idx_campaigns_bundle_capacity_reservations
    ON campaigns (bundle_id, status, id) INCLUDE (num_audience)
    WHERE status IN ('approved', 'running', 'executed');

COMMIT;
