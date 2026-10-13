-- This downgrade is intentionally refused when Excel duplicate allocations
-- exist: restoring the former unique constraint would otherwise discard a
-- campaign's explicit uploaded audience list.

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM bundle_audience_selection_members
        GROUP BY bundle_id, audience_id
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore bundle-wide audience uniqueness while duplicate Excel allocations exist';
    END IF;
END;
$$;

DROP INDEX IF EXISTS idx_bundle_aud_sel_member_bundle_audience;
ALTER TABLE bundle_audience_selection_members
    ADD CONSTRAINT uk_bundle_aud_sel_member_bundle_audience UNIQUE (bundle_id, audience_id);

COMMIT;
