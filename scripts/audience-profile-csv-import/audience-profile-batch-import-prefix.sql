\set ON_ERROR_STOP on
\pset pager off
\pset format unaligned
\pset tuples_only on
\timing on

SET application_name = 'yamata-audience-batch-csv-import';

BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = :'batch_timeout_ms';

CREATE TEMP TABLE tmp_audience_profiles_batch (
    id               bigint NOT NULL,
    uid              varchar(255) NOT NULL,
    phone_number     varchar(20),
    tags             integer[] NOT NULL,
    color            varchar(20) NOT NULL,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    normalized_score double precision
) ON COMMIT DROP;

COPY tmp_audience_profiles_batch (
    id,
    uid,
    phone_number,
    tags,
    color,
    created_at,
    updated_at,
    normalized_score
) FROM STDIN WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');
