CREATE TEMP TABLE tmp_tags (
    id bigint NOT NULL,
    name varchar(255) NOT NULL,
    is_active boolean NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    display_title text,
    audience_persona text,
    audience_count bigint
);

COPY tmp_tags (
    id, name, is_active, created_at, updated_at, display_title,
    audience_persona, audience_count
) FROM STDIN WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');

\echo === reference-data CSVs staged; validating source keys ===
SELECT clock_timestamp() AS validation_started_at;

CREATE UNIQUE INDEX tmp_src_reference_id_key ON tmp_src_reference (id);
CREATE UNIQUE INDEX tmp_tags_id_key ON tmp_tags (id);
CREATE UNIQUE INDEX tmp_tags_name_key ON tmp_tags (name);

DO $validate_reference_data$
DECLARE
    tag_name_conflicts bigint;
    missing_tag_references bigint;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM tmp_src_layer_all_stats) THEN
        RAISE EXCEPTION 'src_layer_all_stats CSV contains no data rows';
    END IF;

    -- An update by id cannot safely take a name already owned by another tag.
    SELECT count(*) INTO tag_name_conflicts
    FROM tmp_tags AS source
    JOIN public.tags AS target ON target.name = source.name
    WHERE target.id <> source.id;

    -- src_reference deliberately has no database FK, but reject a CSV set
    -- whose references cannot resolve after the tag merge.
    SELECT count(*) INTO missing_tag_references
    FROM tmp_src_reference AS reference
    LEFT JOIN tmp_tags AS staged_tag ON staged_tag.id = reference.id
    LEFT JOIN public.tags AS existing_tag ON existing_tag.id = reference.id
    WHERE staged_tag.id IS NULL AND existing_tag.id IS NULL;

    IF tag_name_conflicts > 0 OR missing_tag_references > 0 THEN
        RAISE EXCEPTION
            'reference-data validation failed: tag_name_conflicts=% missing_tag_references=%',
            tag_name_conflicts, missing_tag_references
            USING HINT = 'Resolve tag id/name identity conflicts and ensure every src_reference id exists in tags.';
    END IF;
END
$validate_reference_data$;

SELECT
    (SELECT count(*) FROM tmp_src_layer_all_stats) AS stats_source_rows,
    (SELECT count(*) FROM tmp_src_reference) AS reference_source_rows,
    (SELECT count(*) FROM tmp_tags) AS tags_source_rows,
    (SELECT count(*) FROM tmp_src_reference AS source
       LEFT JOIN public.src_reference AS target ON target.id = source.id
       WHERE target.id IS NULL) AS reference_inserts,
    (SELECT count(*) FROM tmp_tags AS source
       LEFT JOIN public.tags AS target ON target.id = source.id
       WHERE target.id IS NULL) AS tag_inserts;

\echo === validation passed; atomic reference-data merge starting ===
SELECT clock_timestamp() AS merge_started_at;

BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = 0;

-- These source tables are read by campaign targeting. EXCLUSIVE permits
-- ordinary reads but prevents concurrent writers and row-locking readers.
LOCK TABLE public.tags, public.src_reference, public.src_layer_all_stats IN EXCLUSIVE MODE;

INSERT INTO public.tags AS target (
    id, name, is_active, created_at, updated_at, display_title,
    audience_persona, audience_count
)
SELECT
    id, name, is_active, created_at, updated_at, display_title,
    audience_persona, audience_count
FROM tmp_tags
ON CONFLICT (id) DO UPDATE
SET
    name             = EXCLUDED.name,
    is_active        = EXCLUDED.is_active,
    updated_at       = EXCLUDED.updated_at,
    display_title    = EXCLUDED.display_title,
    audience_persona = EXCLUDED.audience_persona,
    audience_count   = EXCLUDED.audience_count
WHERE
    (target.name, target.is_active, target.updated_at, target.display_title,
     target.audience_persona, target.audience_count)
    IS DISTINCT FROM
    (EXCLUDED.name, EXCLUDED.is_active, EXCLUDED.updated_at, EXCLUDED.display_title,
     EXCLUDED.audience_persona, EXCLUDED.audience_count);

INSERT INTO public.src_reference AS target (
    id, src_address, layer1_category, layer2_category, layer3_category,
    tag_count
)
SELECT
    id, src_address, layer1_category, layer2_category, layer3_category,
    tag_count
FROM tmp_src_reference
ON CONFLICT (id) DO UPDATE
SET
    src_address      = EXCLUDED.src_address,
    layer1_category  = EXCLUDED.layer1_category,
    layer2_category  = EXCLUDED.layer2_category,
    layer3_category  = EXCLUDED.layer3_category,
    tag_count        = EXCLUDED.tag_count
WHERE
    (target.src_address, target.layer1_category, target.layer2_category,
     target.layer3_category, target.tag_count)
    IS DISTINCT FROM
    (EXCLUDED.src_address, EXCLUDED.layer1_category, EXCLUDED.layer2_category,
     EXCLUDED.layer3_category, EXCLUDED.tag_count);

-- This table has no stable key. Replacing it as one transaction gives the
-- application either the old complete statistics set or the new complete set.
DELETE FROM public.src_layer_all_stats;
INSERT INTO public.src_layer_all_stats (
    layer1_category, layer2_category, layer3_category, distinct_users,
    calculated_at, black_users, white_users, pink_users, weak_white,
    good_white, best_white, weak_black, good_black, best_black, weak_pink,
    good_pink, best_pink, stat_level, scored_users, p33, p66
)
SELECT
    layer1_category, layer2_category, layer3_category, distinct_users,
    calculated_at, black_users, white_users, pink_users, weak_white,
    good_white, best_white, weak_black, good_black, best_black, weak_pink,
    good_pink, best_pink, stat_level, scored_users, p33, p66
FROM tmp_src_layer_all_stats;

DO $reset_tags_id_sequence$
DECLARE
    sequence_name text;
    maximum_id bigint;
BEGIN
    sequence_name := pg_get_serial_sequence('public.tags', 'id');
    IF sequence_name IS NOT NULL THEN
        SELECT max(id) INTO maximum_id FROM public.tags;
        IF maximum_id IS NOT NULL THEN
            PERFORM setval(sequence_name, maximum_id, true);
        END IF;
    END IF;
END
$reset_tags_id_sequence$;

COMMIT;

\echo === reference-data merge committed ===
SELECT clock_timestamp() AS merge_committed_at;

\echo === vacuum/analyze started ===
VACUUM (ANALYZE) public.tags;
VACUUM (ANALYZE) public.src_reference;
VACUUM (ANALYZE) public.src_layer_all_stats;

\echo === reference-data CSV import complete ===
SELECT clock_timestamp() AS import_completed_at;
