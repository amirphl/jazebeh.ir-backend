#!/usr/bin/env bash
set -Eeuo pipefail
set +x
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
[[ "${APP_ENV:-}" == staging && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Refusing migrations outside staging mock mode' >&2
	exit 1
}
: "${DB_USER:?}" "${DB_NAME:?}"
project="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' yamata-postgres-beta 2>/dev/null || true)"
[[ "$project" == yamata-staging ]] || {
	echo 'Refusing to migrate a PostgreSQL container not owned by yamata-staging' >&2
	exit 1
}
source "$script_dir/migration-manifest.sh"
load_migration_manifest "$root"
psql_cmd=(docker exec -i yamata-postgres-beta psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME")
exists="$("${psql_cmd[@]}" -Atc "SELECT to_regclass('public.yamata_staging_migrations') IS NOT NULL")"
if [[ "$exists" != t ]]; then
	count="$("${psql_cmd[@]}" -Atc "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p')")"
	[[ "$count" == 0 ]] || {
		echo 'Staging database is not empty and has no migration tracker' >&2
		exit 1
	}
	"${psql_cmd[@]}" -c 'CREATE TABLE yamata_staging_migrations (name text PRIMARY KEY, complete boolean NOT NULL DEFAULT false)'
fi
if [[ "$("${psql_cmd[@]}" -Atc 'SELECT count(*) FROM yamata_staging_migrations WHERE NOT complete')" != 0 ]]; then
	echo 'Retrying previously interrupted staging migration(s).' >&2
fi
for name in "${MIGRATION_FILES[@]}"; do
	complete="$("${psql_cmd[@]}" -v migration="$name" -At <<<'SELECT COALESCE((SELECT complete::text FROM yamata_staging_migrations WHERE name = :'"'"'migration'"'"'), '"'"'missing'"'"');')"
	[[ "$complete" == true ]] && continue
	[[ "$complete" == missing || "$complete" == false ]] || { echo "Invalid migration state for $name" >&2; exit 1; }
	printf 'Applying %s\n' "$name"
	"${psql_cmd[@]}" -v migration="$name" <<<'INSERT INTO yamata_staging_migrations(name, complete) VALUES (:'"'"'migration'"'"', false) ON CONFLICT (name) DO UPDATE SET complete = false;'
	"${psql_cmd[@]}" -f "/migrations/$name"
	"${psql_cmd[@]}" -v migration="$name" <<<'UPDATE yamata_staging_migrations SET complete=true WHERE name=:'"'"'migration'"'"';'
done
