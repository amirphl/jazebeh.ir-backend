-- Exact-capacity rows must retain the delivery eligibility used for their
-- count. SMS color eligibility depends on the selected provider, not merely
-- the SMS platform.

BEGIN;

ALTER TABLE campaign_targeting_capacity_calculations
    ADD COLUMN IF NOT EXISTS allowed_colors TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

COMMIT;
