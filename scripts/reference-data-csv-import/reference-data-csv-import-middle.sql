CREATE TEMP TABLE tmp_src_reference (
    id bigint NOT NULL,
    src_address text,
    layer1_category text,
    layer2_category text,
    layer3_category text,
    tag_count bigint
);

COPY tmp_src_reference (
    id, src_address, layer1_category, layer2_category, layer3_category,
    tag_count
) FROM STDIN WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');
