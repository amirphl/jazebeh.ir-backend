\set ON_ERROR_STOP on
\pset pager off
\timing on

-- This file is intentionally only the first part of a psql input stream.
-- Do not execute it directly. import-yamata-audience-profiles-csv.sh writes
-- the host CSV immediately after the COPY command, then supplies the suffix.

SET application_name = 'yamata-audience-csv-import';

\echo === audience CSV staging started ===
SELECT clock_timestamp() AS staging_started_at;

CREATE TEMP TABLE tmp_audience_profiles (
    id               bigint NOT NULL,
    uid              varchar(255) NOT NULL,
    phone_number     varchar(20),
    tags             integer[] NOT NULL,
    color            varchar(20) NOT NULL,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    normalized_score double precision
);

-- The Bash helper streams audience_profiles.csv here through psql's STDIN.
COPY tmp_audience_profiles (
    id,
    uid,
    phone_number,
    tags,
    color,
    created_at,
    updated_at,
    normalized_score
) FROM STDIN WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');
