#!/usr/bin/env bash
set -Eeuo pipefail
set +x
umask 077
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
environment=""
username=""
confirmed=false
while (($#)); do
	case "$1" in
	--environment)
		[[ $# -ge 2 ]] || exit 2
		environment="$2"
		shift 2
		;;
	--username)
		[[ $# -ge 2 ]] || exit 2
		username="$2"
		shift 2
		;;
	--confirm-bootstrap)
		confirmed=true
		shift
		;;
	--help | -h)
		echo 'Usage: bootstrap-mock-admin.sh --environment development|staging --username USER --confirm-bootstrap'
		exit 0
		;;
	*)
		echo "Unknown option: $1" >&2
		exit 2
		;;
	esac
done
[[ "$environment" == development || "$environment" == staging ]] || {
	echo 'Choose development or staging' >&2
	exit 1
}
[[ "$username" =~ ^[A-Za-z0-9_.@-]{1,100}$ && "$confirmed" == true ]] || {
	echo 'A valid username and --confirm-bootstrap are required' >&2
	exit 1
}
[[ -t 0 ]] || {
	echo 'An interactive terminal is required for the password' >&2
	exit 1
}
command -v htpasswd >/dev/null 2>&1 || {
	echo 'Install Apache htpasswd with bcrypt support (apache2-utils on Ubuntu) and retry' >&2
	exit 1
}
source "$script_dir/load-yamata-env.sh"
load_yamata_env_file "$root/.env.$environment"
[[ "${APP_ENV:-}" == "$environment" && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Environment file must select the requested mock environment' >&2
	exit 1
}
source "$script_dir/guard-environment.sh"
reject_production_like_target "${DB_NAME:?}" "${DB_HOST:-postgres-beta}"
mobile=""
IFS=',' read -r -a mappings <<<"${ADMIN_2FA_MOBILES:-}"
for mapping in "${mappings[@]}"; do
	[[ "${mapping%%:*}" == "$username" && "$mapping" == *:* ]] && mobile="${mapping#*:}"
done
[[ "$mobile" =~ ^\+?989[0-9]{9}$ ]] || {
	echo "Set ADMIN_2FA_MOBILES=$username:<test-mobile> with a valid 989... mobile in .env.$environment before bootstrap" >&2
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
	[[ "$project" == yamata-staging ]] || {
		echo 'PostgreSQL container is not owned by yamata-staging' >&2
		exit 1
	}
	psql_cmd=(docker exec -i yamata-postgres-beta psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME")
	tracker=yamata_staging_migrations
fi
[[ "$("${psql_cmd[@]}" -Atc "SELECT to_regclass('public.$tracker') IS NOT NULL")" == t ]] || {
	echo 'Run environment migrations before bootstrapping' >&2
	exit 1
}
[[ "$("${psql_cmd[@]}" -Atc "SELECT count(*) FROM $tracker WHERE NOT complete")" == 0 ]] || {
	echo 'Resolve incomplete migrations before bootstrapping' >&2
	exit 1
}
printf 'Create initial superadmin %s in %s. This grants full admin access.\n' "$username" "$environment"
read -r -s -p 'Password (12–72 bytes): ' password
printf '\n'
read -r -s -p 'Confirm password: ' password_again
printf '\n'
[[ "$password" == "$password_again" && ${#password} -ge 12 ]] || {
	echo 'Passwords must match and be at least 12 characters' >&2
	exit 1
}
bytes="$(printf '%s' "$password" | LC_ALL=C wc -c | tr -d ' ')"
[[ "$bytes" -le 72 ]] || {
	echo 'Password exceeds the bcrypt 72-byte limit' >&2
	exit 1
}
encoded="$(printf '%s\n' "$password" | htpasswd -niBC 12 "$username")"
unset password password_again
hash="${encoded#*:}"
[[ "$hash" == \$2[aby]\$12\$* ]] || {
	echo 'htpasswd did not produce a bcrypt hash' >&2
	exit 1
}
{
	printf "\\\\set admin_username '%s'\n\\\\set admin_hash '%s'\n" "$username" "$hash"
	sed -n '1,$p' "$script_dir/bootstrap-mock-admin.sql"
} | "${psql_cmd[@]}" -f -
unset hash
unset encoded
echo 'Initial administrator created. Restart the backend after changing ADMIN_2FA_MOBILES.'
