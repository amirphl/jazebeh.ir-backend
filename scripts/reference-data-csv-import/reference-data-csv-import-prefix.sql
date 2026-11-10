\set ON_ERROR_STOP on
\pset pager off
\timing on

-- This is the first part of the psql input stream assembled by
-- import-yamata-reference-data-csv.sh.  Each following COPY receives exactly
-- one host CSV through psql's standard input.

SET application_name = 'yamata-reference-data-csv-import';

\echo === reference-data CSV staging started ===
SELECT clock_timestamp() AS staging_started_at;

CREATE TEMP TABLE tmp_src_layer_all_stats (
    layer1_category text,
    layer2_category text,
    layer3_category text,
    distinct_users bigint,
    calculated_at timestamptz,
    black_users bigint,
    white_users bigint,
    pink_users bigint,
    weak_white bigint,
    good_white bigint,
    best_white bigint,
    weak_black bigint,
    good_black bigint,
    best_black bigint,
    weak_pink bigint,
    good_pink bigint,
    best_pink bigint,
    stat_level text,
    scored_users bigint,
    p33 double precision,
    p66 double precision
);

COPY tmp_src_layer_all_stats (
    layer1_category, layer2_category, layer3_category, distinct_users,
    calculated_at, black_users, white_users, pink_users, weak_white,
    good_white, best_white, weak_black, good_black, best_black, weak_pink,
    good_pink, best_pink, stat_level, scored_users, p33, p66
) FROM STDIN WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');
