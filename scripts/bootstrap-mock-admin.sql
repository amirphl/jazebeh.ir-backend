\set ON_ERROR_STOP on
BEGIN;
LOCK TABLE admins IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM admins) THEN
    RAISE EXCEPTION 'Refusing bootstrap: administrators already exist';
  END IF;
END $$;
INSERT INTO admins (uuid, username, password_hash, roles, is_active)
VALUES (gen_random_uuid(), :'admin_username', :'admin_hash', ARRAY['superadmin'], true);
COMMIT;
