#!/usr/bin/env bash

# Restore the canonical scheduler-runtime dump while preserving audience_profiles.
# Selection members, Smart Targeting attribution, status/runtime tables,
# audience-spec sources, and sequence counters are included.
# sent_rubika_messages is excluded because it is absent from the source dataset.
# Usage: restore-yamata-scheduler-runtime-data.sh DUMP_FILE [PROJECT_DIR]

set -Eeuo pipefail
umask 077

readonly DUMP_FILE="${1:-}"
readonly PROJECT_DIR="${2:-/srv/yamata}"
readonly POSTGRES_CONTAINER="yamata-postgres-beta"

TABLES=(
	audience_selections
	bundle_audience_selections
	bundle_audience_selection_members
	campaign_audience_tag_attributions
	bale_status_results
	rubika_status_results
	campaign_status_jobs
	processed_campaigns
	payam_sms_send_responses
	sent_bale_messages
	sent_sms
	sent_splus_messages
	sms_status_results
	splus_status_results
	sequence_counters
	src_layer_all_stats
	src_reference
)
readonly TABLES

log() {
	printf '[scheduler-restore] %s\n' "$*"
}

die() {
	printf '[scheduler-restore] ERROR: %s\n' "$*" >&2
	exit 1
}

[[ -n "$DUMP_FILE" ]] || die "Usage: $(basename "$0") DUMP_FILE [PROJECT_DIR]"
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
	die "Stop yamata-app-beta before restoring scheduler runtime data"
fi

DB_USER="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER)"
DB_NAME="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB)"
readonly DB_USER DB_NAME
[[ -n "$DB_USER" && -n "$DB_NAME" ]] || die "Could not resolve target database credentials"

for preserved_table in audience_profiles; do
	exists="$(
		"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
			-U "$DB_USER" -d "$DB_NAME" \
			-c "SELECT to_regclass('public.$preserved_table') IS NOT NULL;"
	)"
	[[ "$exists" == t ]] || die "Preserved target table does not exist: $preserved_table"
done

log "Checking target schema and confirming restore tables are empty"
for table in "${TABLES[@]}"; do
	exists="$(
		"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
			-U "$DB_USER" -d "$DB_NAME" \
			-c "SELECT to_regclass('public.$table') IS NOT NULL;"
	)"
	[[ "$exists" == t ]] || die "Target table does not exist: $table"

	row_count="$(
		"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
			-U "$DB_USER" -d "$DB_NAME" \
			-c "SELECT count(*) FROM public.$table;"
	)"
	log "$table: $row_count"
	if [[ "$table" == sequence_counters ]]; then
		log "sequence_counters will be atomically replaced"
	else
		[[ "$row_count" == 0 ]] || die "Table is not empty: $table"
	fi
done

bundle_column="$(
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
		-U "$DB_USER" -d "$DB_NAME" \
		-c "SELECT count(*) FROM information_schema.columns
		    WHERE table_schema='public'
		      AND table_name='processed_campaigns'
		      AND column_name='bundle_audience_selection_id';"
)"
[[ "$bundle_column" == 1 ]] ||
	die "Migration 0123 is missing: processed_campaigns.bundle_audience_selection_id does not exist"

normalized_schema_columns="$(
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
		-U "$DB_USER" -d "$DB_NAME" \
		-c "SELECT COUNT(*) FROM information_schema.columns
		    WHERE table_schema='public'
		      AND (table_name, column_name) IN (
		        ('processed_campaigns', 'is_current'),
		        ('sent_bale_messages', 'is_current'),
		        ('bundle_audience_selection_members', 'selection_order')
		      );"
)"
[[ "$normalized_schema_columns" == 3 ]] ||
	die "Migrations through 0129 must be applied before restoring scheduler runtime data"

log "Starting atomic import while preserving audience_profiles"
{
	printf 'TRUNCATE TABLE public.sequence_counters;\n'

	pv --numeric --interval 10 "$DUMP_FILE" 2> >(
		while IFS= read -r progress; do
			printf '[scheduler-restore] progress=%s%%\n' "$progress" >&2
		done
	) | python3 "$COPY_FILTER" - "${TABLES[@]}"

	cat <<'SQL'
DO $sequence_reset$
DECLARE
    target_table text;
    sequence_name text;
    maximum_id bigint;
BEGIN
    FOREACH target_table IN ARRAY ARRAY[
        'audience_selections',
	        'bundle_audience_selections',
	        'bundle_audience_selection_members',
	        'campaign_audience_tag_attributions',
        'bale_status_results',
        'rubika_status_results',
        'campaign_status_jobs',
        'processed_campaigns',
	    'payam_sms_send_responses',
        'sent_bale_messages',
        'sent_sms',
        'sent_splus_messages',
        'sms_status_results',
	        'splus_status_results',
	        'src_reference'
    ] LOOP
        sequence_name := pg_get_serial_sequence('public.' || target_table, 'id');
        IF sequence_name IS NOT NULL THEN
            EXECUTE format('SELECT COALESCE(max(id), 0) FROM public.%I', target_table)
                INTO maximum_id;
            PERFORM setval(sequence_name, GREATEST(maximum_id + 1, 1), false);
        END IF;
    END LOOP;
END
$sequence_reset$;

DO $restore_validation$
BEGIN
    IF EXISTS (
        SELECT campaign_id
        FROM public.processed_campaigns
        GROUP BY campaign_id
        HAVING COUNT(*) FILTER (WHERE is_current) <> 1
    ) THEN
        RAISE EXCEPTION 'restored processed campaigns have an invalid current-row election';
    END IF;
    IF EXISTS (
        SELECT processed_campaign_id, tracking_id
        FROM public.sent_bale_messages
        GROUP BY processed_campaign_id, tracking_id
        HAVING COUNT(*) FILTER (WHERE is_current) <> 1
    ) THEN
        RAISE EXCEPTION 'restored Bale messages have an invalid current-row election';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.bundle_audience_selections AS selection
        WHERE selection.audience_count <> (
            SELECT COUNT(*)
            FROM public.bundle_audience_selection_members AS member
            WHERE member.selection_id = selection.id
        )
    ) THEN
        RAISE EXCEPTION 'restored bundle allocation counts do not match the member ledger';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.campaign_audience_tag_attributions AS attribution
        LEFT JOIN public.bundle_audience_selection_members AS member
          ON member.selection_id = attribution.bundle_audience_selection_id
         AND member.bundle_id = attribution.bundle_id
         AND member.audience_id = attribution.audience_id
         AND member.selection_order = attribution.selection_order
        WHERE member.id IS NULL
    ) THEN
        RAISE EXCEPTION 'restored campaign audience attribution does not match the member ledger';
    END IF;
END
$restore_validation$;
SQL
} | "${DOCKER[@]}" exec -i "$POSTGRES_CONTAINER" \
	psql -X -v ON_ERROR_STOP=1 --single-transaction -U "$DB_USER" -d "$DB_NAME"

log "Analyzing restored tables"
for table in "${TABLES[@]}"; do
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -v ON_ERROR_STOP=1 \
		-U "$DB_USER" -d "$DB_NAME" -c "ANALYZE public.$table;" >/dev/null
done

log "Restore completed successfully"
