#!/usr/bin/env bash
# Source this file before production-only operational actions.
reject_production_like_target() {
	local db_name db_host
	db_name="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
	db_host="$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')"
	case "$db_name" in
	yamata | production | prod | *_production | *_prod)
		printf 'Refusing production-looking database name.\n' >&2
		return 1
		;;
	esac
	case "$db_host" in
	jazebeh.ir | *.jazebeh.ir)
		printf 'Refusing production-domain database host.\n' >&2
		return 1
		;;
	esac
}

refuse_mock_production_operation() {
	printf 'Operational environment: APP_ENV=%s EXTERNAL_SERVICES_MODE=%s\n' "${APP_ENV:-<unset>}" "${EXTERNAL_SERVICES_MODE:-live}" >&2
	[[ "${APP_ENV:-}" == production && "${EXTERNAL_SERVICES_MODE:-live}" == live ]] || {
		printf 'This operation requires APP_ENV=production and live mode.\n' >&2
		return 1
	}
	if command -v docker >/dev/null 2>&1; then
		local project
		project="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' yamata-postgres-beta 2>/dev/null || true)"
		if [[ "$project" == yamata-staging ]]; then
			printf 'This production operation refuses the staging PostgreSQL container.\n' >&2
			return 1
		fi
	fi
}
