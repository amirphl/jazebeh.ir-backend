\set ON_ERROR_STOP on
BEGIN;

-- Insert missing fixtures without overwriting changes made in the environment.
INSERT INTO account_types (type_name, display_name, description) VALUES
  ('individual', 'Individual', 'Personal individual account'),
  ('independent_company', 'Independent Company', 'Independent business company account'),
  ('marketing_agency', 'Marketing Agency', 'Marketing agency account')
ON CONFLICT (type_name) DO NOTHING;

-- Fixture identities are created once.  In particular, never rotate an
-- existing staging password during an otherwise harmless deployment retry.
INSERT INTO admins (uuid, username, password_hash, roles, is_active)
VALUES (gen_random_uuid(), convert_from(decode(:'seed_admin_username_hex', 'hex'), 'UTF8'), crypt(convert_from(decode(:'seed_admin_password_hex', 'hex'), 'UTF8'), gen_salt('bf', 12)), ARRAY['superadmin'], true)
ON CONFLICT (username) DO NOTHING;

INSERT INTO bots (uuid, username, password_hash, is_active)
VALUES (gen_random_uuid(), convert_from(decode(:'bot_username_hex', 'hex'), 'UTF8'), crypt(convert_from(decode(:'bot_password_hex', 'hex'), 'UTF8'), gen_salt('bf', 12)), true)
ON CONFLICT (username) DO NOTHING;

INSERT INTO tags (name, display_title, audience_persona, audience_count, is_active)
SELECT
  format('seed-tag-%s', n),
  format('Seed audience tag %s', n),
  format('Audience interested in category %s, segment %s', ((n - 1) / 10) + 1, ((n - 1) % 10) + 1),
  5000 + n * 137,
  n <= 90
FROM generate_series(1, 100) AS n
ON CONFLICT (name) DO NOTHING;

-- The hierarchy deliberately derives from the generated tag names/IDs, so
-- it remains valid if serial IDs differ between development and staging.
INSERT INTO src_reference (id, src_address, layer1_category, layer2_category, layer3_category, tag_count)
SELECT
  id,
  format('mock://audience/category-%s/topic-%s', ((n - 1) / 10) + 1, ((n - 1) % 10) + 1),
  format('Category %s', ((n - 1) / 10) + 1),
  format('Interest %s', ((n - 1) / 10) + 1),
  format('Topic %s', ((n - 1) % 10) + 1),
  1
FROM (SELECT id, substring(name FROM 10)::integer AS n FROM tags WHERE name ~ '^seed-tag-[0-9]+$') AS seeded
ON CONFLICT (id) DO NOTHING;

INSERT INTO src_layer_all_stats (
  layer1_category, layer2_category, layer3_category, distinct_users,
  calculated_at, black_users, white_users, pink_users, weak_white,
  good_white, best_white, weak_black, good_black, best_black,
  weak_pink, good_pink, best_pink, stat_level, scored_users, p33, p66
)
SELECT
  format('Category %s', category), format('Interest %s', category), format('Topic %s', topic),
  7000 + category * 100 + topic, CURRENT_TIMESTAMP,
  350, 6500 + category * 100 + topic, 150,
  2000, 2500, 2000, 100, 150, 100, 50, 50, 50,
  'layer3', 7000 + category * 100 + topic, 33.0, 66.0
FROM generate_series(1, 10) AS category CROSS JOIN generate_series(1, 10) AS topic
WHERE NOT EXISTS (
  SELECT 1 FROM src_layer_all_stats existing
  WHERE existing.layer1_category = format('Category %s', category)
    AND existing.layer2_category = format('Interest %s', category)
    AND existing.layer3_category = format('Topic %s', topic)
    AND existing.stat_level = 'layer3'
);

INSERT INTO segment_price_factors (platform, level3, price_factor)
SELECT platform, format('Topic %s', topic), 1.0 + ((topic - 1) % 4) * 0.1
FROM (VALUES ('sms'), ('rubika'), ('bale'), ('splus')) AS platforms(platform)
CROSS JOIN generate_series(1, 10) AS topic
WHERE NOT EXISTS (
  SELECT 1 FROM segment_price_factors existing
  WHERE existing.platform = platforms.platform AND existing.level3 = format('Topic %s', topic)
);

INSERT INTO line_numbers (uuid, name, line_number, provider, price_factor, priority, is_active)
SELECT gen_random_uuid(), format('Mock sender %s', n), '9891201' || lpad(n::text, 4, '0'),
  CASE WHEN n % 2 = 0 THEN 'candoo' ELSE 'payamsms' END,
  CASE WHEN n % 2 = 0 THEN 2.0 ELSE 1.0 END, n, n > 3
FROM generate_series(1, 20) AS n
ON CONFLICT (line_number) DO NOTHING;

-- Price columns are unsigned integers, therefore 1.2/1.4/1.6/1.8 are stored
-- as 120/140/160/180 minor units, one deterministic value per platform.
INSERT INTO platform_base_prices (platform, price)
VALUES ('sms', 120), ('rubika', 140), ('bale', 160), ('splus', 180)
ON CONFLICT (platform) WHERE deleted_at IS NULL DO NOTHING;

INSERT INTO page_prices (platform, price)
SELECT platform, price
FROM (VALUES ('sms'::varchar, 120::bigint), ('rubika'::varchar, 140::bigint), ('bale'::varchar, 160::bigint), ('splus'::varchar, 180::bigint)) AS desired(platform, price)
WHERE NOT EXISTS (
  SELECT 1 FROM page_prices existing
  WHERE existing.platform = desired.platform AND existing.price = desired.price
);

INSERT INTO sequence_counters (name, last_value)
VALUES ('sms_tracking_id', repeat('0', 16))
ON CONFLICT (name) DO NOTHING;

-- 100,000 deterministic fixture audiences: sequential external IDs and phone
-- numbers, exactly seven existing generated tags per audience on average,
-- 95% white, and a repeatable normalized score in the inclusive [0,100] range.
INSERT INTO audience_profiles (uid, phone_number, tags, color, normalized_score)
SELECT
  format('mock-audience-%s', n),
  (989120000000::bigint + n)::text,
  (SELECT array_agg(tag.id ORDER BY offset_n)
   FROM generate_series(0, CASE n % 3 WHEN 0 THEN 6 WHEN 1 THEN 5 ELSE 7 END) AS offset_n
   JOIN tags AS tag ON tag.name = format('seed-tag-%s', ((n * 17 + offset_n * 13) % 100) + 1)),
  CASE WHEN n % 20 = 0 THEN 'black' ELSE 'white' END,
  ((n * 37) % 10001)::double precision / 100.0
FROM generate_series(1, 100000) AS n
ON CONFLICT (uid) DO NOTHING;

COMMIT;
