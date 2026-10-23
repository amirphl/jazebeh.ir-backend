-- Capacity allocation lookups retain interrupted campaigns because their
-- materialized audiences remain unavailable. Keep the partial index predicate
-- identical to the lookup predicate so PostgreSQL can use it.

BEGIN;

DROP INDEX IF EXISTS idx_campaigns_bundle_capacity_reservations;
CREATE INDEX idx_campaigns_bundle_capacity_reservations
    ON campaigns (bundle_id, status, id) INCLUDE (num_audience)
    WHERE status IN ('approved', 'running', 'interrupted', 'executed');

COMMIT;
