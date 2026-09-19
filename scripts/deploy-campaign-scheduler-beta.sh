#!/usr/bin/env bash

# Run campaign execution in an isolated backend container.
# Usage: ./scripts/deploy-campaign-scheduler-beta.sh [IMAGE]

set -Eeuo pipefail
set +x # Scheduler environment values may contain credentials.
umask 077

readonly SOURCE_CONTAINER="${YAMATA_SOURCE_CONTAINER:-yamata-app-beta}"
readonly CONTAINER_NAME="${YAMATA_SCHEDULER_CONTAINER:-yamata-campaign-scheduler-beta}"
readonly LOG_VOLUME="${YAMATA_SCHEDULER_LOG_VOLUME:-yamata-campaign-scheduler-logs-beta}"
readonly INTERNAL_BOT_API_DOMAIN="${YAMATA_SCHEDULER_BOT_API_DOMAIN:-http://app-beta:8080}"

case "${1:-}" in
	--help|-h)
		printf 'Usage: %s [IMAGE]\n' "$0"
		printf 'Recreate the isolated campaign scheduler from the running API environment.\n'
		exit 0
		;;
esac

log() {
	printf '[campaign-scheduler] %s\n' "$*"
}

die() {
	printf '[campaign-scheduler] ERROR: %s\n' "$*" >&2
	exit 1
}

for container_value in "$SOURCE_CONTAINER" "$CONTAINER_NAME" "$LOG_VOLUME"; do
	[[ "$container_value" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] ||
		die "Container and volume names must use only letters, digits, dot, underscore, and hyphen"
done

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	die "Docker is unavailable or requires an interactive sudo login"
fi
readonly DOCKER

"${DOCKER[@]}" container inspect "$SOURCE_CONTAINER" >/dev/null 2>&1 ||
	die "Source backend container does not exist: $SOURCE_CONTAINER"

[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$SOURCE_CONTAINER")" == "true" ]] ||
	die "Source backend is not running: $SOURCE_CONTAINER"

source_env_value() {
	local key="$1"
	"${DOCKER[@]}" inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$SOURCE_CONTAINER" |
		awk -F= -v key="$key" '$1 == key { value=substr($0, index($0, "=")+1) } END { print value }'
}

MAIN_CAMPAIGN_SETTING="$(source_env_value CAMPAIGN_EXECUTION_ENABLED)"
[[ "${MAIN_CAMPAIGN_SETTING,,}" == "false" ]] ||
	die "$SOURCE_CONTAINER must have CAMPAIGN_EXECUTION_ENABLED=false (found: ${MAIN_CAMPAIGN_SETTING:-unset})"

MAIN_CAPACITY_SETTING="$(source_env_value SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED)"
[[ "${MAIN_CAPACITY_SETTING,,}" == "true" ]] ||
	die "$SOURCE_CONTAINER must have SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true (found: ${MAIN_CAPACITY_SETTING:-unset})"

MAIN_TEST_SAMPLING_SETTING="$(source_env_value SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED)"
MAIN_TEST_SAMPLING_SETTING="${MAIN_TEST_SAMPLING_SETTING:-$MAIN_CAPACITY_SETTING}"
[[ "${MAIN_TEST_SAMPLING_SETTING,,}" == "true" ]] ||
	die "$SOURCE_CONTAINER must have SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED=true (found: ${MAIN_TEST_SAMPLING_SETTING:-unset})"

MAIN_TAG_TEST_PERFORMANCE_SETTING="$(source_env_value TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED)"
[[ "${MAIN_TAG_TEST_PERFORMANCE_SETTING,,}" == "true" ]] ||
	die "$SOURCE_CONTAINER must have TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED=true (found: ${MAIN_TAG_TEST_PERFORMANCE_SETTING:-unset})"

MAIN_REFUND_SETTING="$(source_env_value CAMPAIGN_REFUND_RECONCILIATION_SCHEDULER_ENABLED)"
[[ "${MAIN_REFUND_SETTING,,}" == "true" ]] ||
	die "$SOURCE_CONTAINER must have CAMPAIGN_REFUND_RECONCILIATION_SCHEDULER_ENABLED=true (found: ${MAIN_REFUND_SETTING:-unset})"

MAIN_TAG_EVALUATION_SETTING="$(source_env_value SMART_TAG_EVALUATION_ENABLED)"
[[ "${MAIN_TAG_EVALUATION_SETTING,,}" == "true" ]] ||
	die "$SOURCE_CONTAINER must have SMART_TAG_EVALUATION_ENABLED=true (found: ${MAIN_TAG_EVALUATION_SETTING:-unset})"

MAIN_TAG_SCHEDULER_SETTING="$(source_env_value SMART_TAG_EVALUATION_SCHEDULER_ENABLED)"
[[ "${MAIN_TAG_SCHEDULER_SETTING,,}" == "true" ]] ||
	die "$SOURCE_CONTAINER must have SMART_TAG_EVALUATION_SCHEDULER_ENABLED=true (found: ${MAIN_TAG_SCHEDULER_SETTING:-unset})"

# The scheduler is a separate process, so it must receive its own DSN instead
# of relying on the API container's Sentry initialization.
SCHEDULER_SENTRY_DSN="$(source_env_value SENTRY_DSN)"
[[ -n "$SCHEDULER_SENTRY_DSN" ]] ||
	die "$SOURCE_CONTAINER must have SENTRY_DSN configured so scheduler errors are reported"
readonly SCHEDULER_SENTRY_DSN

NETWORK="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		"$SOURCE_CONTAINER" | awk 'NF { print; exit }'
)"
[[ -n "$NETWORK" ]] || die "Could not determine the Docker network used by $SOURCE_CONTAINER"

NETWORK_MEMBERS="$("${DOCKER[@]}" network inspect -f '{{range .Containers}}{{println .Name}}{{end}}' "$NETWORK")"
grep -Fxq 'yamata-postgres-beta' <<<"$NETWORK_MEMBERS" ||
	die "yamata-postgres-beta is not attached to $NETWORK"
grep -Fxq 'yamata-redis-beta' <<<"$NETWORK_MEMBERS" ||
	die "yamata-redis-beta is not attached to $NETWORK"

for dependency in yamata-postgres-beta yamata-redis-beta; do
	[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$dependency")" == "true" ]] ||
		die "Dependency is not running: $dependency"
done

command -v python3 >/dev/null 2>&1 || die "python3 is required for safe environment validation"

python3 -c '
import sys, urllib.parse
try:
    value = urllib.parse.urlsplit(sys.argv[1])
    port = value.port
except ValueError:
    raise SystemExit(1)
valid = (
    value.scheme in ("http", "https")
    and value.hostname
    and value.username is None
    and value.password is None
    and value.path in ("", "/")
    and not value.query
    and not value.fragment
    and (port is None or 1 <= port <= 65535)
)
raise SystemExit(0 if valid else 1)
' "$INTERNAL_BOT_API_DOMAIN" ||
	die "YAMATA_SCHEDULER_BOT_API_DOMAIN must be an HTTP(S) origin without credentials, path, or query"

PROMPT_PERSONA_SOURCE="$(
	"${DOCKER[@]}" inspect -f \
		'{{range .Mounts}}{{if eq .Destination "/SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT"}}{{.Source}}{{end}}{{end}}' \
		"$SOURCE_CONTAINER"
)"
PROMPT_SCORING_SOURCE="$(
	"${DOCKER[@]}" inspect -f \
		'{{range .Mounts}}{{if eq .Destination "/SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT"}}{{.Source}}{{end}}{{end}}' \
		"$SOURCE_CONTAINER"
)"
readonly PROMPT_PERSONA_SOURCE PROMPT_SCORING_SOURCE
[[ -f "$PROMPT_PERSONA_SOURCE" ]] ||
	die "Cannot resolve the API persona prompt bind mount"
[[ -f "$PROMPT_SCORING_SOURCE" ]] ||
	die "Cannot resolve the API tag-scoring prompt bind mount"
[[ "$PROMPT_PERSONA_SOURCE" != *','* && "$PROMPT_PERSONA_SOURCE" != *$'\n'* && "$PROMPT_PERSONA_SOURCE" != *$'\r'* ]] ||
	die "Persona prompt path contains unsupported characters"
[[ "$PROMPT_SCORING_SOURCE" != *','* && "$PROMPT_SCORING_SOURCE" != *$'\n'* && "$PROMPT_SCORING_SOURCE" != *$'\r'* ]] ||
	die "Tag-scoring prompt path contains unsupported characters"

IMAGE="${1:-$("${DOCKER[@]}" inspect -f '{{.Config.Image}}' "$SOURCE_CONTAINER")}"
readonly IMAGE
[[ "$IMAGE" =~ ^[A-Za-z0-9][A-Za-z0-9._/@:-]*$ ]] || die "Invalid Docker image reference"
"${DOCKER[@]}" image inspect "$IMAGE" >/dev/null 2>&1 || die "Image does not exist locally: $IMAGE"

RUNTIME_ENV="$(mktemp /tmp/yamata-scheduler-env.XXXXXX)"
cleanup() {
	rm -f -- "$RUNTIME_ENV"
}
trap cleanup EXIT

# Copy the backend's effective environment by default. The API is the sole
# external-short-link sync owner; the scheduler explicitly disables that
# feature below so mapping publication and click ingestion have one worker.
# The explicit omit list is limited to credentials and settings for components
# the scheduler never runs.
"${DOCKER[@]}" inspect -f '{{json .Config.Env}}' "$SOURCE_CONTAINER" |
	python3 -c 'import json,sys; values=json.load(sys.stdin); bad=[v.split("=",1)[0] for v in values if "\n" in v or "\r" in v or "\0" in v]; sys.exit("container environment contains unsupported control characters: " + ", ".join(bad) if bad else 0)'
"${DOCKER[@]}" inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$SOURCE_CONTAINER" |
	awk -F= '
		BEGIN {
			override["BOT_API_DOMAIN"] = 1
			override["CAMPAIGN_EXECUTION_ENABLED"] = 1
			override["CRYPTO_ENABLED"] = 1
			override["EXTERNAL_SHORTLINK_ENABLED"] = 1
			override["SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED"] = 1
			override["SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED"] = 1
			override["SMART_TARGETING_EXECUTION_CALCULATION_SCHEDULER_ENABLED"] = 1
			override["TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED"] = 1
			override["CAMPAIGN_REFUND_RECONCILIATION_SCHEDULER_ENABLED"] = 1
			override["SMART_TAG_EVALUATION_ENABLED"] = 1
			override["SMART_TAG_EVALUATION_SCHEDULER_ENABLED"] = 1
			override["SERVER_HOST"] = 1
			override["SERVER_PORT"] = 1
			override["SERVER_ENABLE_PPROF"] = 1
			override["SERVER_ENABLE_METRICS"] = 1
			override["METRICS_ENABLED"] = 1
			override["METRICS_ENABLE_PROMETHEUS"] = 1
			override["LOG_OUTPUT"] = 1
			override["LOG_FILE_PATH"] = 1
			override["LOG_ENABLE_ACCESS"] = 1
			override["LOG_ACCESS_PATH"] = 1
			override["LOG_AUDIT_PATH"] = 1
			override["LOG_SECURITY_PATH"] = 1
			override["SENTRY_SERVER_NAME"] = 1
			override["SENTRY_DSN"] = 1
			override["NO_PROXY"] = 1
			override["no_proxy"] = 1
			omit["GRAFANA_ADMIN_PASSWORD"] = 1
			omit["CERTBOT_EMAIL"] = 1
			omit["CERT_ALERT_PHONE"] = 1
			omit["CERT_ALERT_CERT_PATHS"] = 1
			omit["REDIS_PASSWORD"] = 1
			omit["BACKUP_INTERVAL_SECONDS"] = 1
			omit["BACKUP_RETRY_SECONDS"] = 1
			omit["BACKUP_HEALTH_MAX_AGE_SECONDS"] = 1
			omit["BACKUP_S3_BUCKET"] = 1
			omit["BACKUP_S3_ACCESS_KEY"] = 1
			omit["BACKUP_S3_SECRET_KEY"] = 1
			omit["ATIPAY_API_KEY"] = 1
			omit["ATIPAY_TERMINAL"] = 1
			omit["SENTRY_POSTGRES_DB"] = 1
			omit["SENTRY_POSTGRES_USER"] = 1
			omit["SENTRY_POSTGRES_PASSWORD"] = 1
			omit["SENTRY_GLITCHTIP_SECRET_KEY"] = 1
			omit["SENTRY_SUPERUSER_USERNAME"] = 1
			omit["SENTRY_SUPERUSER_PASSWORD"] = 1
			omit["SENTRY_SUPERUSER_EMAIL"] = 1
			omit["SENTRY_DEFAULT_FROM_EMAIL"] = 1
			omit["SENTRY_EMAIL_URL"] = 1
			omit["SENTRY_ENABLE_USER_REGISTRATION"] = 1
			omit["SENTRY_ENABLE_OPEN_USER_REGISTRATION"] = 1
			omit["ADMIN_DEPOSIT_REVIEWER"] = 1
			omit["ADMIN_2FA_MOBILES"] = 1
			omit["ADMIN_OTP_BYPASS_MOBILES"] = 1
			omit["ADMIN_LOGIN_OTP_FORWARD_MOBILE"] = 1
			omit["OXA_API_KEY"] = 1
			omit["OPENAI_API_KEY"] = 1
		}
		!($1 in override) && !($1 in omit) { print }
	' >"$RUNTIME_ENV"

cat >>"$RUNTIME_ENV" <<'EOF'
CAMPAIGN_EXECUTION_ENABLED=true
CRYPTO_ENABLED=false
EXTERNAL_SHORTLINK_ENABLED=false
SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=false
SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED=false
SMART_TARGETING_EXECUTION_CALCULATION_SCHEDULER_ENABLED=false
TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED=false
CAMPAIGN_REFUND_RECONCILIATION_SCHEDULER_ENABLED=false
SMART_TAG_EVALUATION_ENABLED=false
SMART_TAG_EVALUATION_SCHEDULER_ENABLED=false
SERVER_HOST=127.0.0.1
SERVER_PORT=8080
SERVER_ENABLE_PPROF=false
SERVER_ENABLE_METRICS=false
METRICS_ENABLED=false
METRICS_ENABLE_PROMETHEUS=false
LOG_OUTPUT=both
LOG_FILE_PATH=/var/log/yamata/campaign-scheduler.log
LOG_ENABLE_ACCESS=false
LOG_ACCESS_PATH=/var/log/yamata/campaign-scheduler-access.log
LOG_AUDIT_PATH=/var/log/yamata/campaign-scheduler-audit.log
LOG_SECURITY_PATH=/var/log/yamata/campaign-scheduler-security.log
SENTRY_SERVER_NAME=yamata-campaign-scheduler-beta
EOF
printf 'SENTRY_DSN=%s\n' "$SCHEDULER_SENTRY_DSN" >>"$RUNTIME_ENV"
printf 'BOT_API_DOMAIN=%s\n' "$INTERNAL_BOT_API_DOMAIN" >>"$RUNTIME_ENV"
cat >>"$RUNTIME_ENV" <<'EOF'
NO_PROXY=localhost,127.0.0.1,app-beta,yamata-app-beta,postgres-beta,yamata-postgres-beta,redis-beta,yamata-redis-beta,172.30.0.0/24
no_proxy=localhost,127.0.0.1,app-beta,yamata-app-beta,postgres-beta,yamata-postgres-beta,redis-beta,yamata-redis-beta,172.30.0.0/24
EOF

verify_runtime_environment() {
	local expected key actual
	while IFS= read -r expected; do
		[[ -n "$expected" ]] || continue
		[[ "$expected" == *=* ]] || {
			printf '[campaign-scheduler] ERROR: generated environment entry is invalid: %s\n' "${expected%%=*}" >&2
			return 1
		}
		key="${expected%%=*}"
		actual="$(
			"${DOCKER[@]}" inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER_NAME" |
				awk -F= -v key="$key" '$1 == key { entry=$0 } END { print entry }'
		)"
		if [[ "$actual" != "$expected" ]]; then
			printf '[campaign-scheduler] ERROR: scheduler environment did not retain %s\n' "$key" >&2
			return 1
		fi
	done <"$RUNTIME_ENV"
}

"${DOCKER[@]}" volume create "$LOG_VOLUME" >/dev/null

if "${DOCKER[@]}" container inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
	log "Stopping the previous scheduler container gracefully"
	"${DOCKER[@]}" stop --time 60 "$CONTAINER_NAME" >/dev/null || true
	"${DOCKER[@]}" rm "$CONTAINER_NAME" >/dev/null
fi

log "Starting $CONTAINER_NAME from $IMAGE on $NETWORK"
"${DOCKER[@]}" run -d \
	--name "$CONTAINER_NAME" \
	--hostname "$CONTAINER_NAME" \
	--shm-size 1g \
	--restart unless-stopped \
	--stop-timeout 60 \
	--network "$NETWORK" \
	--env-file "$RUNTIME_ENV" \
	--user appuser \
	--read-only \
	--tmpfs /tmp:rw,nosuid,noexec,size=256m \
	--tmpfs /var/cache:rw,nosuid,noexec,size=64m \
	--mount "type=volume,source=$LOG_VOLUME,target=/var/log/yamata" \
	--mount "type=volume,source=$LOG_VOLUME,target=/data" \
	--mount "type=bind,source=$PROMPT_PERSONA_SOURCE,target=/SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT,readonly" \
	--mount "type=bind,source=$PROMPT_SCORING_SOURCE,target=/SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT,readonly" \
	--security-opt no-new-privileges=true \
	--cap-drop ALL \
	--log-driver local \
	--log-opt max-size=20m \
	--log-opt max-file=5 \
	--label com.yamata.role=campaign-scheduler \
	"$IMAGE" >/dev/null

if ! verify_runtime_environment; then
	"${DOCKER[@]}" rm --force "$CONTAINER_NAME" >/dev/null 2>&1 || true
	die "Refusing a scheduler with an incomplete runtime environment"
fi

log "Waiting for the isolated instance to become healthy"
deadline=$((SECONDS + 90))
while ((SECONDS < deadline)); do
	state="$("${DOCKER[@]}" inspect -f '{{.State.Status}}' "$CONTAINER_NAME")"
	health="$("${DOCKER[@]}" inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$CONTAINER_NAME")"
	if [[ "$state" == "running" && "$health" == "healthy" ]]; then
		"${DOCKER[@]}" exec "$CONTAINER_NAME" \
			curl -fsS --noproxy app-beta "$INTERNAL_BOT_API_DOMAIN/api/v1/health" >/dev/null ||
			die "$CONTAINER_NAME cannot reach the API at $INTERNAL_BOT_API_DOMAIN"
		log "Ready. Campaign execution is enabled only in $CONTAINER_NAME"
		log "Internal bot API: $INTERNAL_BOT_API_DOMAIN"
		log "Logs: ${DOCKER[*]} logs -f $CONTAINER_NAME"
		log "Persistent log volume: $LOG_VOLUME"
		exit 0
	fi
	if [[ "$state" == "dead" || "$state" == "exited" ]]; then
		break
	fi
	sleep 2
done

"${DOCKER[@]}" logs --tail 100 "$CONTAINER_NAME" >&2 || true
"${DOCKER[@]}" stop --time 30 "$CONTAINER_NAME" >/dev/null 2>&1 || true
"${DOCKER[@]}" rm "$CONTAINER_NAME" >/dev/null 2>&1 || true
die "$CONTAINER_NAME did not become healthy and was removed"
