#!/usr/bin/env bash
# Idempotent, non-production fixture data for local development and staging.
set -Eeuo pipefail
set +x
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
environment=""

while (($#)); do
	case "$1" in
		--environment) environment="${2:?Missing environment}"; shift 2 ;;
		--help|-h) echo 'Usage: seed-mock-reference-data.sh --environment development|staging'; exit 0 ;;
		*) echo "Unknown option: $1" >&2; exit 2 ;;
	esac
done

[[ "$environment" == development || "$environment" == staging ]] || {
	echo 'Use development or staging only' >&2
	exit 1
}

source "$script_dir/load-yamata-env.sh"
load_yamata_env_file "$root/.env.$environment"
[[ "${APP_ENV:-}" == "$environment" && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Refusing to seed a non-mock environment' >&2
	exit 1
}
source "$script_dir/guard-environment.sh"
reject_production_like_target "${DB_NAME:?}" "${DB_HOST:-postgres-beta}"

: "${BOT_USERNAME:?}" "${BOT_PASSWORD:?}" "${MOCK_SEED_ADMIN_USERNAME:?}" "${MOCK_SEED_ADMIN_PASSWORD:?}"
[[ "$MOCK_SEED_ADMIN_PASSWORD" != CHANGE_ME && "$BOT_PASSWORD" != CHANGE_ME ]] || {
	echo 'Mock seed credentials must be set before seeding' >&2
	exit 1
}

if [[ "$environment" == development ]]; then
	: "${DB_HOST:?}" "${DB_PORT:?}" "${DB_USER:?}" "${DB_PASSWORD:?}"
	export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGDATABASE="$DB_NAME" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD" PGSSLMODE="${DB_SSL_MODE:-disable}"
	psql_cmd=(psql -X -v ON_ERROR_STOP=1)
	tracker=yamata_development_migrations
else
	: "${DB_USER:?}"
	project="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' yamata-postgres-beta 2>/dev/null || true)"
	[[ "$project" == yamata-staging ]] || { echo 'PostgreSQL container is not owned by yamata-staging' >&2; exit 1; }
	psql_cmd=(docker exec -i yamata-postgres-beta psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME")
	tracker=yamata_staging_migrations
fi

[[ "$("${psql_cmd[@]}" -Atc "SELECT to_regclass('public.$tracker') IS NOT NULL")" == t ]] || {
	echo 'Run migrations before seeding mock data' >&2
	exit 1
}
[[ "$("${psql_cmd[@]}" -Atc "SELECT count(*) FROM $tracker WHERE NOT complete")" == 0 ]] || {
	echo 'Refusing to seed while a migration is incomplete' >&2
	exit 1
}

# Send hex-encoded values through stdin, never through psql or docker arguments.
# Hex encoding also makes arbitrary quotes and newlines safe in psql variables.
hex_value() { printf '%s' "$1" | od -An -v -tx1 | tr -d ' \n'; }
{
	printf "\\set seed_admin_username_hex '%s'\n" "$(hex_value "$MOCK_SEED_ADMIN_USERNAME")"
	printf "\\set seed_admin_password_hex '%s'\n" "$(hex_value "$MOCK_SEED_ADMIN_PASSWORD")"
	printf "\\set bot_username_hex '%s'\n" "$(hex_value "$BOT_USERNAME")"
	printf "\\set bot_password_hex '%s'\n" "$(hex_value "$BOT_PASSWORD")"
	cat "$script_dir/seed-mock-reference-data.sql"
} | "${psql_cmd[@]}" -f -

echo "Mock reference data is ready for $environment."
