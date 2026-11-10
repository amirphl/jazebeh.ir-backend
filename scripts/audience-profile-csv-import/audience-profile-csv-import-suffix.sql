-- This file is intentionally only the latter part of the psql input stream.
-- It must run in the same psql session as audience-profile-csv-import-prefix.sql,
-- because tmp_audience_profiles is a temporary table.

\echo === audience CSV staged; validating source keys ===
SELECT clock_timestamp() AS validation_started_at;

-- Fail on duplicate source identities before the target table is touched.
CREATE UNIQUE INDEX tmp_audience_profiles_id_key
    ON tmp_audience_profiles (id);

CREATE UNIQUE INDEX tmp_audience_profiles_uid_key
    ON tmp_audience_profiles (uid);

CREATE UNIQUE INDEX tmp_audience_profiles_phone_number_key
    ON tmp_audience_profiles (phone_number)
    WHERE phone_number IS NOT NULL;

ANALYZE tmp_audience_profiles;

-- Emit the planned result before the merge for the journal/log.
SELECT
    count(*) AS source_rows,
    count(target.id) AS existing_ids,
    count(*) FILTER (WHERE target.id IS NULL) AS inserts,
    count(*) FILTER (
        WHERE target.id IS NOT NULL
          AND (target.uid, target.phone_number, target.tags, target.color,
               target.updated_at, target.normalized_score)
              IS DISTINCT FROM
              (source.uid, source.phone_number, source.tags, source.color,
               source.updated_at, source.normalized_score)
    ) AS updates,
    count(*) FILTER (
        WHERE target.id IS NOT NULL
          AND (target.uid, target.phone_number, target.tags, target.color,
               target.updated_at, target.normalized_score)
              IS NOT DISTINCT FROM
              (source.uid, source.phone_number, source.tags, source.color,
               source.updated_at, source.normalized_score)
    ) AS unchanged
FROM tmp_audience_profiles AS source
LEFT JOIN public.audience_profiles AS target ON target.id = source.id;

-- ON CONFLICT (id) cannot safely reconcile uid/phone identities that refer to
-- another row. Abort before the merge if the CSV has either kind of conflict.
DO $validate_identity_mappings$
DECLARE
    uid_conflicts bigint;
    phone_conflicts bigint;
BEGIN
    SELECT count(*)
    INTO uid_conflicts
    FROM tmp_audience_profiles AS source
    JOIN public.audience_profiles AS target ON target.uid = source.uid
    WHERE target.id <> source.id;

    SELECT count(*)
    INTO phone_conflicts
    FROM tmp_audience_profiles AS source
    JOIN public.audience_profiles AS target
      ON target.phone_number = source.phone_number
    WHERE source.phone_number IS NOT NULL
      AND target.id <> source.id;

    IF uid_conflicts > 0 OR phone_conflicts > 0 THEN
        RAISE EXCEPTION
            'CSV identity conflicts: uid=% phone_number=%',
            uid_conflicts,
            phone_conflicts
            USING HINT = 'Resolve rows whose uid or phone_number belongs to a different id before retrying.';
    END IF;
END
$validate_identity_mappings$;

\echo === validation passed; atomic merge starting ===
SELECT clock_timestamp() AS merge_started_at;

BEGIN;

-- A competing profile writer makes this run fail rather than wait forever.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = 0;

-- Permit ordinary readers while blocking profile writes and SELECT ... FOR
-- UPDATE. EXCLUSIVE is deliberate: SHARE ROW EXCLUSIVE permits ROW SHARE
-- locks, which can otherwise create a tuple-lock deadlock with a live API
-- request that later upgrades to an UPDATE.
LOCK TABLE public.audience_profiles IN EXCLUSIVE MODE;

INSERT INTO public.audience_profiles AS target (
    id,
    uid,
    phone_number,
    tags,
    color,
    created_at,
    updated_at,
    normalized_score
)
SELECT
    id,
    uid,
    phone_number,
    tags,
    color,
    created_at,
    updated_at,
    normalized_score
FROM tmp_audience_profiles
ON CONFLICT (id) DO UPDATE
SET
    uid              = EXCLUDED.uid,
    phone_number     = EXCLUDED.phone_number,
    tags             = EXCLUDED.tags,
    color            = EXCLUDED.color,
    updated_at       = EXCLUDED.updated_at,
    normalized_score = EXCLUDED.normalized_score
-- Preserve the original created_at on existing profiles and avoid generating
-- dead tuples and index work for source rows that are already identical.
WHERE
    (target.uid, target.phone_number, target.tags, target.color,
     target.updated_at, target.normalized_score)
    IS DISTINCT FROM
    (EXCLUDED.uid, EXCLUDED.phone_number, EXCLUDED.tags, EXCLUDED.color,
     EXCLUDED.updated_at, EXCLUDED.normalized_score);

-- Explicit ids from the CSV do not advance the BIGSERIAL sequence.
DO $reset_audience_profile_id_sequence$
DECLARE
    sequence_name text;
    maximum_id bigint;
BEGIN
    sequence_name := pg_get_serial_sequence('public.audience_profiles', 'id');
    IF sequence_name IS NOT NULL THEN
        SELECT max(id) INTO maximum_id
        FROM public.audience_profiles;

        IF maximum_id IS NOT NULL THEN
            PERFORM setval(sequence_name, maximum_id, true);
        END IF;
    END IF;
END
$reset_audience_profile_id_sequence$;

COMMIT;

\echo === audience merge committed ===
SELECT clock_timestamp() AS merge_committed_at;

-- VACUUM cannot run inside the merge transaction. It is safe to rerun if this
-- client stops after COMMIT and before completion.
\echo === vacuum/analyze started ===
VACUUM (ANALYZE) public.audience_profiles;

\echo === audience CSV import complete ===
SELECT clock_timestamp() AS import_completed_at;
