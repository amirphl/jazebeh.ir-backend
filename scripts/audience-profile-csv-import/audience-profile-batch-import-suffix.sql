-- This file is appended in the same psql session as the prefix and one batch
-- of CSV rows. Any error before COMMIT rolls back the whole batch.

CREATE UNIQUE INDEX tmp_audience_profiles_batch_id_key
    ON tmp_audience_profiles_batch (id);
CREATE UNIQUE INDEX tmp_audience_profiles_batch_uid_key
    ON tmp_audience_profiles_batch (uid);
CREATE UNIQUE INDEX tmp_audience_profiles_batch_phone_number_key
    ON tmp_audience_profiles_batch (phone_number)
    WHERE phone_number IS NOT NULL;

ANALYZE tmp_audience_profiles_batch;

-- This is a plan line only. The Fish runner emits BATCH_SUCCESS only after
-- psql exits successfully, which happens after the COMMIT below.
SELECT format(
    'BATCH_PLAN rows=%s existing=%s inserts=%s updates=%s unchanged=%s skipped_newer=%s',
    count(*),
    count(target.id),
    count(*) FILTER (WHERE target.id IS NULL),
    count(*) FILTER (
        WHERE target.id IS NOT NULL
          AND target.updated_at <= source.updated_at
          AND (target.uid, target.phone_number, target.tags, target.color,
               target.updated_at, target.normalized_score)
              IS DISTINCT FROM
              (source.uid, source.phone_number, source.tags, source.color,
               source.updated_at, source.normalized_score)
    ),
    count(*) FILTER (
        WHERE target.id IS NOT NULL
          AND (target.uid, target.phone_number, target.tags, target.color,
               target.updated_at, target.normalized_score)
              IS NOT DISTINCT FROM
              (source.uid, source.phone_number, source.tags, source.color,
               source.updated_at, source.normalized_score)
    ),
    count(*) FILTER (
        WHERE target.id IS NOT NULL
          AND target.updated_at > source.updated_at
          AND (target.uid, target.phone_number, target.tags, target.color,
               target.updated_at, target.normalized_score)
              IS DISTINCT FROM
              (source.uid, source.phone_number, source.tags, source.color,
               source.updated_at, source.normalized_score)
    )
)
FROM tmp_audience_profiles_batch AS source
LEFT JOIN public.audience_profiles AS target ON target.id = source.id;

-- Do not allow an ID to take over a UID or phone number owned by another ID.
-- The SERIALIZABLE transaction makes a concurrent conflicting writer cause a
-- retryable database error rather than a silently inconsistent merge.
DO $validate_identity_mappings$
DECLARE
    uid_conflicts bigint;
    phone_conflicts bigint;
BEGIN
    SELECT count(*) INTO uid_conflicts
    FROM tmp_audience_profiles_batch AS source
    JOIN public.audience_profiles AS target ON target.uid = source.uid
    WHERE target.id <> source.id;

    SELECT count(*) INTO phone_conflicts
    FROM tmp_audience_profiles_batch AS source
    JOIN public.audience_profiles AS target
      ON target.phone_number = source.phone_number
    WHERE source.phone_number IS NOT NULL
      AND target.id <> source.id;

    IF uid_conflicts > 0 OR phone_conflicts > 0 THEN
        RAISE EXCEPTION
            'CSV identity conflicts: uid=% phone_number=%',
            uid_conflicts, phone_conflicts
            USING HINT = 'Resolve the CSV rows before resuming.';
    END IF;
END
$validate_identity_mappings$;

INSERT INTO public.audience_profiles AS target (
    id, uid, phone_number, tags, color, created_at, updated_at, normalized_score
)
SELECT
    id, uid, phone_number, tags, color, created_at, updated_at, normalized_score
FROM tmp_audience_profiles_batch
ON CONFLICT (id) DO UPDATE
SET
    uid              = EXCLUDED.uid,
    phone_number     = EXCLUDED.phone_number,
    tags             = EXCLUDED.tags,
    color            = EXCLUDED.color,
    updated_at       = EXCLUDED.updated_at,
    normalized_score = EXCLUDED.normalized_score
-- A live application update newer than the export wins. This avoids a nightly
-- import reverting a profile modified after the export was created.
WHERE target.updated_at <= EXCLUDED.updated_at
  AND (target.uid, target.phone_number, target.tags, target.color,
       target.updated_at, target.normalized_score)
      IS DISTINCT FROM
      (EXCLUDED.uid, EXCLUDED.phone_number, EXCLUDED.tags, EXCLUDED.color,
       EXCLUDED.updated_at, EXCLUDED.normalized_score);

DO $reset_audience_profile_id_sequence$
DECLARE
    sequence_name text;
    maximum_id bigint;
BEGIN
    sequence_name := pg_get_serial_sequence('public.audience_profiles', 'id');
    IF sequence_name IS NOT NULL THEN
        SELECT max(id) INTO maximum_id FROM public.audience_profiles;
        IF maximum_id IS NOT NULL THEN
            PERFORM setval(sequence_name, maximum_id, true);
        END IF;
    END IF;
END
$reset_audience_profile_id_sequence$;

COMMIT;
SELECT 'BATCH_COMMITTED';
