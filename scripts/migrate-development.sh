#!/usr/bin/env bash
set -Eeuo pipefail
set +x
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
[[ "${APP_ENV:-}" == development && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Refusing migrations outside development mock mode' >&2
	exit 1
}
: "${PGHOST:?Set PGHOST to the development PostgreSQL host}" "${PGDATABASE:?Set PGDATABASE to the development database name}"
source "$script_dir/guard-environment.sh"
reject_production_like_target "$PGDATABASE" "$PGHOST"
[[ "$PGDATABASE" == *_development ]] || {
	echo 'Development migration database names must end in _development' >&2
	exit 1
}
case "$(printf '%s' "$PGHOST" | tr '[:upper:]' '[:lower:]')" in
localhost | 127.0.0.1 | ::1 | \[::1\]) ;;
*) [[ "${DEVELOPMENT_MIGRATIONS_ALLOW_REMOTE:-}" == 1 ]] || {
	echo 'Refusing a non-local development database; set DEVELOPMENT_MIGRATIONS_ALLOW_REMOTE=1 only for an approved development target' >&2
	exit 1
} ;;
esac
source "$script_dir/migration-manifest.sh"
load_migration_manifest "$root"
psql_cmd=(psql -X -v ON_ERROR_STOP=1)
exists="$("${psql_cmd[@]}" -Atc "SELECT to_regclass('public.yamata_development_migrations') IS NOT NULL")"
if [[ "$exists" != t ]]; then
	count="$("${psql_cmd[@]}" -Atc "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m')")"
	[[ "$count" == 0 ]] || {
		echo 'Database is not empty and has no development migration tracker; refusing to modify it' >&2
		exit 1
	}
	"${psql_cmd[@]}" -c 'CREATE EXTENSION IF NOT EXISTS "uuid-ossp"; CREATE EXTENSION IF NOT EXISTS pgcrypto; CREATE TABLE yamata_development_migrations (name text PRIMARY KEY, complete boolean NOT NULL DEFAULT false);'
fi
# A false row is a durable record that the previous invocation did not finish.
# Do not skip it forever: after the underlying SQL/data problem has been fixed,
# retry that migration before considering later files.  Migrations must remain
# safe to retry (or repair their own partial state) as documented in
# migrations/README.md.
if [[ "$("${psql_cmd[@]}" -Atc 'SELECT count(*) FROM yamata_development_migrations WHERE NOT complete')" != 0 ]]; then
	echo 'Retrying previously interrupted development migration(s).' >&2
fi
for name in "${MIGRATION_FILES[@]}"; do
	complete="$("${psql_cmd[@]}" -v migration="$name" -At <<<'SELECT COALESCE((SELECT complete::text FROM yamata_development_migrations WHERE name = :'"'"'migration'"'"'), '"'"'missing'"'"');')"
	[[ "$complete" == true ]] && continue
	[[ "$complete" == missing || "$complete" == false ]] || { echo "Invalid migration state for $name" >&2; exit 1; }
	printf 'Applying %s\n' "$name"
	"${psql_cmd[@]}" -v migration="$name" <<<'INSERT INTO yamata_development_migrations(name, complete) VALUES (:'"'"'migration'"'"', false) ON CONFLICT (name) DO UPDATE SET complete = false;'
	"${psql_cmd[@]}" -f "$root/migrations/$name"
	"${psql_cmd[@]}" -v migration="$name" <<<'UPDATE yamata_development_migrations SET complete=true WHERE name=:'"'"'migration'"'"';'
done
