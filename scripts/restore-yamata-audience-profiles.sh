#!/usr/bin/env bash

# Restore an audience_profiles-only plain SQL dump into the existing beta database.
# Usage: restore-yamata-audience-profiles.sh DUMP_FILE [PROJECT_DIR] [--allow-active-backend]

set -Eeuo pipefail
umask 077

readonly DUMP_FILE="${1:-}"
readonly PROJECT_DIR="${2:-/srv/yamata}"
readonly POSTGRES_CONTAINER="yamata-postgres-beta"
readonly TABLE="audience_profiles"
readonly ACTIVE_BACKEND_OPTION="${3:-}"

case "$ACTIVE_BACKEND_OPTION" in
	"") ALLOW_ACTIVE_BACKEND=false ;;
	--allow-active-backend) ALLOW_ACTIVE_BACKEND=true ;;
	*) printf '[audience-restore] ERROR: Unknown option: %s\n' "$ACTIVE_BACKEND_OPTION" >&2; exit 2 ;;
esac
readonly ALLOW_ACTIVE_BACKEND

log() {
	printf '[audience-restore] %s\n' "$*"
}

die() {
	printf '[audience-restore] ERROR: %s\n' "$*" >&2
	exit 1
}

[[ -n "$DUMP_FILE" ]] ||
	die "Usage: $(basename "$0") DUMP_FILE [PROJECT_DIR] [--allow-active-backend]"
[[ -f "$DUMP_FILE" ]] || die "Dump does not exist: $DUMP_FILE"
[[ -d "$PROJECT_DIR" ]] || die "Project directory does not exist: $PROJECT_DIR"

for command_name in awk pv python3; do
	command -v "$command_name" >/dev/null 2>&1 || die "Required command is missing: $command_name"
done

if [[ -f /usr/local/libexec/yamata-extract-pg-dump-copy.py ]]; then
	COPY_FILTER=/usr/local/libexec/yamata-extract-pg-dump-copy.py
else
	COPY_FILTER="$PROJECT_DIR/scripts/extract_pg_dump_copy.py"
fi
readonly COPY_FILTER
[[ -f "$COPY_FILTER" ]] || die "Missing safe dump filter: $COPY_FILTER"

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	die "Docker is unavailable or requires an interactive sudo login"
fi
readonly DOCKER

"${DOCKER[@]}" inspect "$POSTGRES_CONTAINER" >/dev/null 2>&1 ||
	die "PostgreSQL container does not exist: $POSTGRES_CONTAINER"
[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER")" == true ]] ||
	die "PostgreSQL container is not running"
project="$("${DOCKER[@]}" inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$POSTGRES_CONTAINER")"
[[ -n "$project" && "$project" != '<no value>' && "$project" != yamata-staging ]] ||
	die "Refusing to restore into staging or an unrecognized PostgreSQL container"

if "${DOCKER[@]}" inspect yamata-campaign-scheduler-beta >/dev/null 2>&1 &&
	[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' yamata-campaign-scheduler-beta)" == true ]]; then
	die "Dedicated campaign scheduler must be stopped before restore: yamata-campaign-scheduler-beta"
fi

if "${DOCKER[@]}" inspect yamata-app-beta >/dev/null 2>&1 &&
	[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' yamata-app-beta)" == true ]]; then
	if [[ "$ALLOW_ACTIVE_BACKEND" != true ]]; then
		die "Backend is active; stop it or explicitly pass --allow-active-backend"
	fi
	main_campaign_setting="$(
		"${DOCKER[@]}" inspect -f '{{range .Config.Env}}{{println .}}{{end}}' yamata-app-beta |
			awk -F= '$1 == "CAMPAIGN_EXECUTION_ENABLED" { value=substr($0, index($0, "=")+1) } END { print value }'
	)"
	[[ "${main_campaign_setting,,}" == false ]] ||
		die "Active backend must have CAMPAIGN_EXECUTION_ENABLED=false"
	log "WARNING: proceeding with the API backend active by explicit request"
fi

DB_USER="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER)"
DB_NAME="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB)"
readonly DB_USER DB_NAME
[[ -n "$DB_USER" && -n "$DB_NAME" ]] || die "Could not resolve target database credentials"

exists="$(
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
		-U "$DB_USER" -d "$DB_NAME" \
		-c "SELECT to_regclass('public.$TABLE') IS NOT NULL;"
)"
[[ "$exists" == t ]] || die "Target table does not exist: $TABLE"

row_count="$(
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
		-U "$DB_USER" -d "$DB_NAME" \
		-c "SELECT count(*) FROM public.$TABLE;"
)"
log "$TABLE: $row_count"
[[ "$row_count" == 0 ]] || die "Table is not empty: $TABLE"

normalized_score_column="$(
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
		-U "$DB_USER" -d "$DB_NAME" \
		-c "SELECT count(*) FROM information_schema.columns
		    WHERE table_schema='public'
		      AND table_name='audience_profiles'
		      AND column_name='normalized_score';"
)"
[[ "$normalized_score_column" == 1 ]] ||
	die "Migration 0114 is missing: audience_profiles.normalized_score does not exist"

log "Starting atomic audience_profiles import"
{
	pv --numeric --interval 10 "$DUMP_FILE" 2> >(
		while IFS= read -r progress; do
			printf '[audience-restore] progress=%s%%\n' "$progress" >&2
		done
	) | python3 "$COPY_FILTER" - "$TABLE"

	cat <<'SQL'
DO $sequence_reset$
DECLARE
    sequence_name text;
    maximum_id bigint;
BEGIN
    sequence_name := pg_get_serial_sequence('public.audience_profiles', 'id');
    IF sequence_name IS NOT NULL THEN
        SELECT COALESCE(max(id), 0) INTO maximum_id
        FROM public.audience_profiles;
        PERFORM setval(sequence_name, GREATEST(maximum_id + 1, 1), false);
    END IF;
END
$sequence_reset$;
SQL
} | "${DOCKER[@]}" exec -i "$POSTGRES_CONTAINER" \
	psql -X -v ON_ERROR_STOP=1 --single-transaction -U "$DB_USER" -d "$DB_NAME"

log "Analyzing audience_profiles"
"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -v ON_ERROR_STOP=1 \
	-U "$DB_USER" -d "$DB_NAME" -c 'ANALYZE public.audience_profiles;' >/dev/null

log "Restore completed successfully"
