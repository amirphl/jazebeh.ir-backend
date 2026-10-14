BEGIN;

ALTER TABLE campaign_targeting_capacity_calculations
    DROP COLUMN IF EXISTS allowed_colors;

COMMIT;
