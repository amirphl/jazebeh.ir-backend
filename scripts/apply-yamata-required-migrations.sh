#!/usr/bin/env bash

# Verify the schema required by the current app, or explicitly repair a restored
# or partially migrated database.
# Usage: apply-yamata-required-migrations.sh [--verify-only|--repair] [PROJECT_DIR]

set -Eeuo pipefail
umask 077

MODE="verify"
PROJECT_DIR="/srv/yamata"
project_dir_set=false
mode_set=false

usage() {
	cat <<'EOF'
Usage: apply-yamata-required-migrations.sh [--verify-only|--repair] [PROJECT_DIR]

  --verify-only  Check required schema objects without changing the database
                 (default; safe for routine deployments).
  --repair       Reapply the idempotent repair migration subset, run deep data
                 checks, and advance the filesystem migration tracker.
EOF
}

for argument in "$@"; do
	case "$argument" in
		--verify-only)
			if [[ "$mode_set" == true && "$MODE" != verify ]]; then
				printf '[required-migrations] ERROR: --verify-only and --repair are mutually exclusive\n' >&2
				exit 2
			fi
			MODE="verify"
			mode_set=true
			;;
		--repair)
			if [[ "$mode_set" == true && "$MODE" != repair ]]; then
				printf '[required-migrations] ERROR: --verify-only and --repair are mutually exclusive\n' >&2
				exit 2
			fi
			MODE="repair"
			mode_set=true
			;;
		--help|-h)
			usage
			exit 0
			;;
		--*)
			printf '[required-migrations] ERROR: Unknown option: %s\n' "$argument" >&2
			usage >&2
			exit 2
			;;
		*)
			if [[ "$project_dir_set" == true ]]; then
				printf '[required-migrations] ERROR: Multiple project directories supplied\n' >&2
				usage >&2
				exit 2
			fi
			PROJECT_DIR="$argument"
			project_dir_set=true
			;;
	esac
done

readonly MODE PROJECT_DIR
readonly POSTGRES_CONTAINER="yamata-postgres-beta"

log() {
	printf '[required-migrations] %s\n' "$*"
}

die() {
	printf '[required-migrations] ERROR: %s\n' "$*" >&2
	exit 1
}

[[ -d "$PROJECT_DIR/migrations" ]] || die "Missing migrations directory: $PROJECT_DIR/migrations"

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	die "Docker is unavailable or requires an interactive sudo login"
fi
readonly DOCKER

[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null || true)" == true ]] ||
	die "PostgreSQL container is not running: $POSTGRES_CONTAINER"

if [[ "$MODE" == repair ]]; then
	if "${DOCKER[@]}" inspect yamata-campaign-scheduler-beta >/dev/null 2>&1 &&
		[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' yamata-campaign-scheduler-beta)" == true ]]; then
		die "Stop yamata-campaign-scheduler-beta before repairing migrations"
	fi
	if "${DOCKER[@]}" inspect yamata-app-beta >/dev/null 2>&1 &&
		[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' yamata-app-beta)" == true ]]; then
		die "Stop yamata-app-beta before repairing migrations"
	fi
fi

DB_USER="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER)"
DB_NAME="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB)"
readonly DB_USER DB_NAME

psql_scalar() {
	"${DOCKER[@]}" exec "$POSTGRES_CONTAINER" psql -X -At \
		-U "$DB_USER" -d "$DB_NAME" -c "$1"
}

apply_file() {
	local migration_file="$1"
	log "Applying $(basename "$migration_file")"
	"${DOCKER[@]}" exec -i "$POSTGRES_CONTAINER" \
		psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" <"$migration_file"
}

advance_migration_tracker() {
	local verified_migration="$1"
	local tracking_file="$PROJECT_DIR/.migration_tracker_beta"
	local tracker_value="$verified_migration"
	local existing_value=""
	local temporary=""

	if [[ -e "$tracking_file" ]]; then
		[[ ! -L "$tracking_file" ]] || die "Refusing symlinked migration tracker"
		[[ -f "$tracking_file" ]] || die "Migration tracker is not a regular file"
		IFS= read -r existing_value <"$tracking_file" || true
		[[ "$existing_value" =~ ^[0-9]{4}_[A-Za-z0-9_]+\.sql$ ]] ||
			die "Migration tracker is empty or malformed"
		[[ "$(awk 'END { print NR }' "$tracking_file")" -eq 1 ]] ||
			die "Migration tracker must contain exactly one filename"
		[[ -f "$PROJECT_DIR/migrations/$existing_value" ]] ||
			die "Migration tracker references an unavailable migration: $existing_value"

		# The general migrator may already have advanced beyond the subset this
		# helper verifies. Never move its valid tracker backward.
		if [[ "$existing_value" > "$tracker_value" ]]; then
			tracker_value="$existing_value"
		fi
	fi

	temporary="$(mktemp "$PROJECT_DIR/.migration_tracker_beta.XXXXXX")"
	printf '%s\n' "$tracker_value" >"$temporary"
	chmod 600 "$temporary"
	mv -f -- "$temporary" "$tracking_file"
	log "Migration tracker is at $tracker_value"
}

[[ "$(psql_scalar "SELECT to_regclass('public.audience_profiles') IS NOT NULL;")" == t ]] ||
	die "Missing public.audience_profiles"
[[ "$(psql_scalar "SELECT to_regclass('public.bundle_audience_selections') IS NOT NULL;")" == t ]] ||
	die "Migration 0111 is missing: public.bundle_audience_selections does not exist"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='bundle_tag_evaluation_runs'
		  AND column_name='id'
		  AND data_type='bigint'
	);")" == t ]] ||
	die "Migration 0119 must already be applied; it is destructive and is not auto-applied"

if [[ "$MODE" == repair ]]; then
	log "Repair mode: applying the required migration subset"
	apply_file "$PROJECT_DIR/migrations/0114_add_normalized_score_to_audience_profiles.sql"
	apply_file "$PROJECT_DIR/migrations/0120_create_campaign_selected_tags.sql"
	apply_file "$PROJECT_DIR/migrations/0121_backfill_campaign_audience_targeting_method.sql"
	apply_file "$PROJECT_DIR/migrations/0122_create_campaign_targeting_capacity_calculations.sql"
	apply_file "$PROJECT_DIR/migrations/0123_add_bundle_audience_selection_to_processed_campaigns.sql"
	# Do not replay 0124 here: its transitional index is removed by 0127. A
	# restored database that needs repair should go directly to the final schema.
	apply_file "$PROJECT_DIR/migrations/0125_create_src_reference.sql"
	apply_file "$PROJECT_DIR/migrations/0126_optimize_campaign_audience_selection.sql"
	apply_file "$PROJECT_DIR/migrations/0127_normalize_bundle_audience_allocations.sql"
	apply_file "$PROJECT_DIR/migrations/0128_smart_targeting_phase_preparation.sql"
	apply_file "$PROJECT_DIR/migrations/0129_create_payam_sms_send_responses.sql"
	apply_file "$PROJECT_DIR/migrations/0130_create_campaign_targeting_test_sampling_calculations.sql"
	apply_file "$PROJECT_DIR/migrations/0131_optimize_postgres_audience_maintenance.sql"
	apply_file "$PROJECT_DIR/migrations/0132_create_tag_test_performance_reports.sql"
	apply_file "$PROJECT_DIR/migrations/0133_decouple_smart_targeting_test_sampling.sql"
	apply_file "$PROJECT_DIR/migrations/0134_create_bundle_audience_exclusions.sql"
	# Keep the repair sequence contiguous: the tracker is advanced through 0136
	# below, so 0135 must be applied rather than silently skipped later.
	apply_file "$PROJECT_DIR/migrations/0135_external_short_link_sync.sql"
	apply_file "$PROJECT_DIR/migrations/0136_version_smart_targeting_capacity_eligibility.sql"
else
	log "Verification mode: no migrations will be applied"
fi

constraint_exists="$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='public.processed_campaigns'::regclass
		  AND conname='processed_campaigns_single_audience_selection'
	);")"
[[ "$constraint_exists" == t ]] ||
	die "Migration 0123 is incomplete: processed_campaigns_single_audience_selection is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.campaign_targeting_capacity_calculations') IS NOT NULL;")" == t ]] ||
	die "Migration 0122 is incomplete: campaign_targeting_capacity_calculations is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.campaign_selected_tags') IS NOT NULL;")" == t ]] ||
	die "Migration 0120 is incomplete: campaign_selected_tags is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.src_reference') IS NOT NULL;")" == t ]] ||
	die "Migration 0125 is incomplete: src_reference is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.idx_audience_profiles_campaign_id_phone') IS NOT NULL;")" == t ]] ||
	die "Migration 0126 is incomplete: optimized audience lookup index is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.bundle_audience_selection_members') IS NOT NULL;")" == t ]] ||
	die "Migration 0127 is incomplete: bundle_audience_selection_members is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='processed_campaigns'
		  AND column_name='is_current'
	);")" == t ]] ||
	die "Migration 0127 is incomplete: processed campaign current-row marker is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='sent_bale_messages'
		  AND column_name='is_current'
	);")" == t ]] ||
	die "Migration 0127 is incomplete: Bale current-row marker is missing"
[[ "$(psql_scalar "
	SELECT NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='bundle_audience_selections'
		  AND column_name='audience_ids'
	);")" == t ]] ||
	die "Migration 0127 is incomplete: legacy bundle audience arrays still exist"
[[ "$(psql_scalar "
	SELECT COALESCE((
		SELECT indisunique AND pg_get_expr(indpred, indrelid) = 'is_current'
		FROM pg_index
		WHERE indexrelid=to_regclass('public.uk_processed_campaigns_campaign_id')
	), FALSE);")" == t ]] ||
	die "Migration 0127 is incomplete: current processed campaign uniqueness is missing"
[[ "$(psql_scalar "
	SELECT COALESCE((
		SELECT indisunique AND pg_get_expr(indpred, indrelid) = 'is_current'
		FROM pg_index
		WHERE indexrelid=to_regclass('public.uk_sent_bale_messages_processed_tracking')
	), FALSE);")" == t ]] ||
	die "Migration 0127 is incomplete: current Bale tracking uniqueness is missing"
if [[ "$MODE" == repair ]]; then
	log "Repair mode: validating data invariants"
	[[ "$(psql_scalar "
		SELECT NOT EXISTS (
			SELECT campaign_id FROM processed_campaigns
			GROUP BY campaign_id
			HAVING COUNT(*) FILTER (WHERE is_current) <> 1
		);")" == t ]] ||
		die "Migration 0127 is inconsistent: a processed campaign has no current checkpoint"
	[[ "$(psql_scalar "
		SELECT NOT EXISTS (
			SELECT processed_campaign_id, tracking_id FROM sent_bale_messages
			GROUP BY processed_campaign_id, tracking_id
			HAVING COUNT(*) FILTER (WHERE is_current) <> 1
		);")" == t ]] ||
		die "Migration 0127 is inconsistent: a Bale tracking checkpoint has no current row"
	[[ "$(psql_scalar "
		SELECT NOT EXISTS (
			SELECT 1 FROM bundle_audience_selections AS selection
			WHERE selection.audience_count <> (
				SELECT COUNT(*) FROM bundle_audience_selection_members AS member
				WHERE member.selection_id=selection.id
			)
		);")" == t ]] ||
		die "Migration 0127 is inconsistent: allocation counts do not match the member ledger"
fi
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='public.bundle_audience_selection_members'::regclass
		  AND conname='fk_bundle_aud_sel_member_selection_bundle'
	);")" == t ]] ||
	die "Migration 0127 is incomplete: selection/bundle ledger integrity is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.idx_processed_campaigns_capacity_reservation_materialized') IS NULL;")" == t ]] ||
	die "Migration 0127 is incomplete: obsolete capacity reservation index still exists"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaigns'
		  AND column_name='sample_size_per_tag'
	);")" == t ]] ||
	die "Migration 0128 is incomplete: campaigns.sample_size_per_tag is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaign_selected_tags'
		  AND column_name='selection_order'
	);")" == t ]] ||
	die "Migration 0128 is incomplete: campaign_selected_tags.selection_order is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='bundle_audience_selection_members'
		  AND column_name='selection_order'
	);")" == t ]] ||
	die "Migration 0128 is incomplete: bundle audience selection order is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.campaign_audience_tag_attributions') IS NOT NULL;")" == t ]] ||
	die "Migration 0128 is incomplete: campaign_audience_tag_attributions is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.payam_sms_send_responses') IS NOT NULL;")" == t ]] ||
	die "Migration 0129 is incomplete: payam_sms_send_responses is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.campaign_targeting_test_sampling_calculations') IS NOT NULL;")" == t ]] ||
	die "Migration 0130 is incomplete: campaign_targeting_test_sampling_calculations is missing"
[[ "$(psql_scalar "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements');")" == t ]] ||
	die "Migration 0131 is incomplete: pg_stat_statements is not installed"
[[ "$(psql_scalar "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='pgstattuple');")" == t ]] ||
	die "Migration 0131 is incomplete: pgstattuple is not installed"
[[ "$(psql_scalar "SELECT to_regclass('public.idx_audience_profiles_uid') IS NULL;")" == t ]] ||
	die "Migration 0131 is incomplete: redundant audience UID index still exists"
[[ "$(psql_scalar "SELECT to_regclass('public.idx_audience_profiles_phone_number') IS NULL;")" == t ]] ||
	die "Migration 0131 is incomplete: redundant audience phone index still exists"
[[ "$(psql_scalar "SELECT to_regclass('public.campaign_tag_test_reports') IS NOT NULL;")" == t ]] ||
	die "Migration 0132 is incomplete: campaign_tag_test_reports is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.campaign_tag_test_performances') IS NOT NULL;")" == t ]] ||
	die "Migration 0132 is incomplete: campaign_tag_test_performances is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.tag_test_phase_performance_summaries') IS NOT NULL;")" == t ]] ||
	die "Migration 0132 is incomplete: tag_test_phase_performance_summaries is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.tag_test_performance_scheduler_state') IS NOT NULL;")" == t ]] ||
	die "Migration 0132 is incomplete: tag_test_performance_scheduler_state is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.bundle_audience_exclusions') IS NOT NULL;")" == t ]] ||
	die "Migration 0134 is incomplete: bundle_audience_exclusions is missing"
[[ "$(psql_scalar "SELECT to_regclass('public.external_short_link_sync_state') IS NOT NULL;")" == t ]] ||
	die "Migration 0135 is incomplete: external_short_link_sync_state is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='short_links'
		  AND column_name='external_published_at'
	);")" == t ]] ||
	die "Migration 0135 is incomplete: short_links external publication state is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='short_link_clicks'
		  AND column_name='external_click_id'
	);")" == t ]] ||
	die "Migration 0135 is incomplete: short-link external click state is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaign_targeting_capacity_calculations'
		  AND column_name='apply_bundle_audience_exclusions'
		  AND data_type='boolean'
		  AND is_nullable='NO'
		  AND column_default='false'
	);")" == t ]] ||
	die "Migration 0136 is incomplete: capacity exclusion input is missing or invalid"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaign_targeting_capacity_calculations'
		  AND column_name='calculation_version'
		  AND column_default='3'
	);")" == t ]] ||
	die "Migration 0136 is incomplete: capacity calculation version default is not 3"
[[ "$(psql_scalar "
	SELECT COALESCE((
		SELECT indisunique AND indisvalid AND indisready
		FROM pg_index
		WHERE indexrelid=to_regclass('public.bundle_audience_exclusions_pkey')
	), FALSE);")" == t ]] ||
	die "Migration 0134 is incomplete: Bundle audience exclusion pair uniqueness is missing"
[[ "$(psql_scalar "
	SELECT COALESCE((
		SELECT indisvalid AND indisready
		FROM pg_index
		WHERE indexrelid=to_regclass('public.idx_bundle_audience_exclusions_audience')
	), FALSE);")" == t ]] ||
	die "Migration 0134 is incomplete: Bundle audience exclusion reverse index is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaign_targeting_test_sampling_calculations'
		  AND column_name='allocation_fingerprint'
		  AND data_type='character'
		  AND character_maximum_length=64
		  AND is_nullable='NO'
	);")" == t ]] ||
	die "Migration 0133 is incomplete: Test sampling allocation fingerprint is missing or invalid"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conrelid='public.campaign_targeting_test_sampling_calculations'::regclass
		  AND conname='campaign_targeting_test_sampling_allocation_fingerprint_valid'
		  AND contype='c'
		  AND convalidated
	);")" == t ]] ||
	die "Migration 0133 is incomplete: Test sampling allocation fingerprint constraint is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaign_targeting_test_sampling_calculations'
		  AND column_name='calculation_version'
		  AND column_default='2'
	);")" == t ]] ||
	die "Migration 0133 is incomplete: Test sampling calculation version default is not 2"
[[ "$(psql_scalar "
	SELECT COALESCE((
		SELECT indisvalid AND indisready
		FROM pg_index
		WHERE indexrelid=to_regclass('public.idx_short_link_clicks_campaign_phone')
	), FALSE);")" == t ]] ||
	die "Migration 0132 is incomplete: the Campaign/phone click index is missing or invalid"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='public.campaign_tag_test_performances'::regclass
		  AND conname='uk_campaign_tag_test_performance'
		  AND contype='u'
	);")" == t ]] ||
	die "Migration 0132 is incomplete: per-Campaign tag uniqueness is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='public.tag_test_phase_performance_summaries'::regclass
		  AND conname='uk_tag_test_phase_performance_bundle_tag'
		  AND contype='u'
	);")" == t ]] ||
	die "Migration 0132 is incomplete: Bundle/tag summary uniqueness is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='campaign_tag_test_performances'
		  AND column_name='test_campaign_ctr'
		  AND is_generated='ALWAYS'
	);")" == t ]] ||
	die "Migration 0132 is incomplete: generated per-Campaign CTR is missing"
[[ "$(psql_scalar "
	SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='tag_test_phase_performance_summaries'
		  AND column_name='test_phase_avg_ctr'
		  AND is_generated='ALWAYS'
	);")" == t ]] ||
	die "Migration 0132 is incomplete: generated aggregate Test CTR is missing"

if [[ "$MODE" == repair ]]; then
	advance_migration_tracker '0136_version_smart_targeting_capacity_eligibility.sql'
	log "Required schema repaired and verified"
else
	log "Required schema verified"
fi
