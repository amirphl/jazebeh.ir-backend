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
