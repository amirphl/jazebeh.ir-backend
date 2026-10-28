#!/usr/bin/env bash

# Read-only validation of the restored/deployed production topology.

set -Eeuo pipefail
set +x # Container environment checks must never emit credential values.

readonly PROJECT_DIR="${1:-/srv/yamata}"

log() {
	printf '[production-check] %s\n' "$*"
}

die() {
	printf '[production-check] ERROR: %s\n' "$*" >&2
	exit 1
}

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	die "Docker is unavailable or requires an interactive sudo login"
fi
readonly DOCKER

env_value() {
	local container="$1"
	local key="$2"
	"${DOCKER[@]}" inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$container" |
		awk -F= -v key="$key" '$1 == key { value=substr($0, index($0, "=")+1) } END { print value }'
}

for container in \
	yamata-postgres-beta yamata-redis-beta yamata-app-beta yamata-nginx-beta \
	yamata-pgadmin-beta yamata-pgadmin-nginx-beta yamata-campaign-scheduler-beta; do
	"${DOCKER[@]}" container inspect "$container" >/dev/null 2>&1 || die "Missing container: $container"
	state="$("${DOCKER[@]}" inspect -f '{{.State.Status}}' "$container")"
	[[ "$state" == running ]] || die "$container is $state"
	log "$container: running"
done

# A running process is not sufficient for the stateful and monitoring services:
# each has a Compose healthcheck that verifies its local listener or dependency.
for container in \
	yamata-sentry-postgres-beta yamata-sentry-redis-beta yamata-sentry-beta \
	yamata-prometheus-beta yamata-grafana-beta yamata-postgres-exporter-beta \
	yamata-node-exporter-beta; do
	"${DOCKER[@]}" container inspect "$container" >/dev/null 2>&1 || die "Missing container: $container"
	state="$("${DOCKER[@]}" inspect -f '{{.State.Status}}' "$container")"
	[[ "$state" == running ]] || die "$container is $state"
	health="$("${DOCKER[@]}" inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container")"
	[[ "$health" == healthy ]] || die "$container health is $health"
	log "$container: healthy"
done

# Nginx-to-Sentry forwarding is observability-only. Keep reporting its state,
# but do not reject an otherwise healthy application deployment when it is
# temporarily unavailable.
readonly NGINX_SENTRY_FORWARDER=yamata-nginx-sentry-forwarder-beta
if ! "${DOCKER[@]}" container inspect "$NGINX_SENTRY_FORWARDER" >/dev/null 2>&1; then
	log "WARNING: $NGINX_SENTRY_FORWARDER is missing; Nginx errors are not being forwarded to Sentry"
else
	state="$("${DOCKER[@]}" inspect -f '{{.State.Status}}' "$NGINX_SENTRY_FORWARDER")"
	health="$("${DOCKER[@]}" inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$NGINX_SENTRY_FORWARDER")"
	if [[ "$state" == running && "$health" == healthy ]]; then
		log "$NGINX_SENTRY_FORWARDER: healthy"
	else
		log "WARNING: $NGINX_SENTRY_FORWARDER state is $state, health is $health; Nginx errors may not reach Sentry"
	fi
fi

# PostgreSQL parallel queries allocate POSIX dynamic shared-memory segments.
# Reject Docker's 64 MiB default (and other undersized deployments) before a
# large audience query discovers the problem in production.
readonly MIN_POSTGRES_SHM_BYTES=$((2 * 1024 * 1024 * 1024))
postgres_shm_bytes="$("${DOCKER[@]}" inspect -f '{{.HostConfig.ShmSize}}' yamata-postgres-beta)"
[[ "$postgres_shm_bytes" =~ ^[0-9]+$ ]] ||
	die "Could not determine yamata-postgres-beta shared-memory size"
((postgres_shm_bytes >= MIN_POSTGRES_SHM_BYTES)) ||
	die "yamata-postgres-beta /dev/shm is undersized (${postgres_shm_bytes} bytes; require at least ${MIN_POSTGRES_SHM_BYTES})"
log "yamata-postgres-beta /dev/shm: $((postgres_shm_bytes / 1024 / 1024 / 1024)) GiB"

[[ "$(env_value yamata-app-beta CAMPAIGN_EXECUTION_ENABLED)" == false ]] ||
	die "yamata-app-beta must have CAMPAIGN_EXECUTION_ENABLED=false"
[[ "$(env_value yamata-campaign-scheduler-beta CAMPAIGN_EXECUTION_ENABLED)" == true ]] ||
	die "yamata-campaign-scheduler-beta must have CAMPAIGN_EXECUTION_ENABLED=true"
[[ "$(env_value yamata-campaign-scheduler-beta EXTERNAL_SHORTLINK_ENABLED)" == false ]] ||
	die "External short-link sync must be disabled in the campaign scheduler; app-beta is the sole sync owner"
[[ "$(env_value yamata-app-beta SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED)" == true ]] ||
	die "Exact Smart Targeting capacity scheduling must be enabled in yamata-app-beta"
[[ "$(env_value yamata-campaign-scheduler-beta SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED)" == false ]] ||
	die "Exact Smart Targeting capacity scheduling must be disabled in the campaign scheduler"
[[ "$(env_value yamata-app-beta SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED)" == true ]] ||
	die "Smart Targeting Test sampling must be enabled in yamata-app-beta"
[[ "$(env_value yamata-campaign-scheduler-beta SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED)" == false ]] ||
	die "Smart Targeting Test sampling must be disabled in the campaign scheduler"
[[ "$(env_value yamata-app-beta TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED)" == true ]] ||
	die "Tag Test performance scheduling must be enabled in yamata-app-beta"
[[ "$(env_value yamata-campaign-scheduler-beta TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED)" == false ]] ||
	die "Tag Test performance scheduling must be disabled in the campaign scheduler"
[[ "$(env_value yamata-campaign-scheduler-beta BOT_API_DOMAIN)" == http://app-beta:8080 ]] ||
	die "Scheduler BOT_API_DOMAIN must be http://app-beta:8080"
[[ "$(env_value yamata-campaign-scheduler-beta SERVER_HOST)" == 127.0.0.1 ]] ||
	die "Scheduler HTTP listener must bind to 127.0.0.1"
[[ "$(env_value yamata-campaign-scheduler-beta SMART_TAG_EVALUATION_ENABLED)" == false ]] ||
	die "Smart-tag evaluation must be disabled in the campaign scheduler"
[[ "$(env_value yamata-campaign-scheduler-beta SMART_TAG_EVALUATION_SCHEDULER_ENABLED)" == false ]] ||
	die "Smart-tag scheduling must be disabled in the campaign scheduler"
[[ "$(env_value yamata-app-beta SMART_TAG_EVALUATION_ENABLED)" == true ]] ||
	die "Smart-tag evaluation must be enabled in yamata-app-beta"
[[ "$(env_value yamata-app-beta SMART_TAG_EVALUATION_SCHEDULER_ENABLED)" == true ]] ||
	die "Smart-tag scheduling must be enabled in yamata-app-beta"
[[ -n "$(env_value yamata-app-beta BOT_USERNAME)" ]] || die "BOT_USERNAME is empty"
[[ -n "$(env_value yamata-app-beta BOT_PASSWORD)" ]] || die "BOT_PASSWORD is empty"

app_image_id="$("${DOCKER[@]}" inspect -f '{{.Image}}' yamata-app-beta)"
scheduler_image_id="$("${DOCKER[@]}" inspect -f '{{.Image}}' yamata-campaign-scheduler-beta)"
[[ "$app_image_id" == "$scheduler_image_id" ]] ||
	die "API and scheduler use different image IDs; run deploy-production-beta.sh"
[[ "$("${DOCKER[@]}" inspect -f '{{.HostConfig.RestartPolicy.Name}}' yamata-campaign-scheduler-beta)" == unless-stopped ]] ||
	die "Scheduler restart policy must be unless-stopped"
[[ "$("${DOCKER[@]}" inspect -f '{{len .HostConfig.PortBindings}}' yamata-campaign-scheduler-beta)" == 0 ]] ||
	die "Scheduler must not publish ports"
scheduler_log_volume="$(
	"${DOCKER[@]}" inspect -f \
		'{{range .Mounts}}{{if and (eq .Type "volume") (eq .Destination "/var/log/yamata")}}{{.Name}}{{end}}{{end}}' \
		yamata-campaign-scheduler-beta
)"
[[ -n "$scheduler_log_volume" ]] || die "Scheduler has no persistent log volume"
log "scheduler log volume: $scheduler_log_volume"

app_network="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		yamata-app-beta | awk 'NF { print; exit }'
)"
scheduler_network="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		yamata-campaign-scheduler-beta | awk 'NF { print; exit }'
)"
[[ -n "$app_network" && "$app_network" == "$scheduler_network" ]] ||
	die "API and scheduler are not attached to the same Docker network"
log "shared network: $app_network"

# pgAdmin is an Nginx-only service: it must not publish a host port. Its
# dedicated Nginx proxy is the sole listener, bound to the selected host
# interface on 14433, while pgAdmin stays on two internal-only networks.
[[ "$("${DOCKER[@]}" inspect -f '{{len .HostConfig.PortBindings}}' yamata-pgadmin-beta)" == 0 ]] ||
	die "pgAdmin must not publish ports"
[[ "$("${DOCKER[@]}" inspect -f '{{len .HostConfig.PortBindings}}' yamata-postgres-beta)" == 0 ]] ||
	die "PostgreSQL must not publish ports"
pgadmin_networks="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		yamata-pgadmin-beta | awk 'NF { print }' | sort
)"
pgadmin_proxy_networks="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		yamata-pgadmin-nginx-beta | awk 'NF { print }' | sort
)"
main_nginx_networks="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		yamata-nginx-beta | awk 'NF { print }' | sort
)"
postgres_networks="$(
	"${DOCKER[@]}" inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' \
		yamata-postgres-beta | awk 'NF { print }' | sort
)"
expected_pgadmin_networks=$'yamata-no-orochi_pgadmin-network-beta\nyamata-no-orochi_pgadmin-postgres-network-beta'
[[ "$pgadmin_networks" == "$expected_pgadmin_networks" ]] ||
	die "pgAdmin must join only the Nginx and PostgreSQL internal networks"
expected_pgadmin_proxy_networks=$'yamata-no-orochi_pgadmin-edge-network-beta\nyamata-no-orochi_pgadmin-network-beta'
[[ "$pgadmin_proxy_networks" == "$expected_pgadmin_proxy_networks" ]] ||
	die "The dedicated pgAdmin Nginx proxy must join only its edge and pgAdmin networks"
if grep -qx 'yamata-no-orochi_pgadmin-network-beta' <<<"$main_nginx_networks"; then
	die "The application Nginx must not join the pgAdmin network"
fi
if grep -qx 'yamata-no-orochi_yamata-network-beta' <<<"$pgadmin_proxy_networks"; then
	die "The dedicated pgAdmin Nginx proxy must not join the application network"
fi
if grep -qx 'yamata-no-orochi_pgadmin-postgres-network-beta' <<<"$pgadmin_proxy_networks"; then
	die "The dedicated pgAdmin Nginx proxy must not join the PostgreSQL network"
fi
grep -qx 'yamata-no-orochi_pgadmin-postgres-network-beta' <<<"$postgres_networks" ||
	die "PostgreSQL is not attached to the dedicated pgAdmin network"
if grep -qx 'yamata-no-orochi_yamata-network-beta' <<<"$pgadmin_networks"; then
	die "pgAdmin must not join the application network"
fi
pgadmin_proxy_ip="$(
	"${DOCKER[@]}" inspect -f '{{with index .NetworkSettings.Networks "yamata-no-orochi_pgadmin-network-beta"}}{{.IPAddress}}{{end}}' \
		yamata-pgadmin-beta
)"
[[ "$pgadmin_proxy_ip" == "172.31.0.10" ]] ||
	die "pgAdmin must listen behind Nginx at 172.31.0.10"
pgadmin_database_ip="$(
	"${DOCKER[@]}" inspect -f '{{with index .NetworkSettings.Networks "yamata-no-orochi_pgadmin-postgres-network-beta"}}{{.IPAddress}}{{end}}' \
		yamata-pgadmin-beta
)"
[[ "$pgadmin_database_ip" == "172.29.0.3" ]] ||
	die "pgAdmin must use its dedicated PostgreSQL network address"
postgres_database_ip="$(
	"${DOCKER[@]}" inspect -f '{{with index .NetworkSettings.Networks "yamata-no-orochi_pgadmin-postgres-network-beta"}}{{.IPAddress}}{{end}}' \
		yamata-postgres-beta
)"
[[ "$postgres_database_ip" == "172.29.0.2" ]] ||
	die "PostgreSQL must use its dedicated pgAdmin network address"
pgadmin_listener_binding="$(
	"${DOCKER[@]}" inspect -f '{{range $port, $bindings := .HostConfig.PortBindings}}{{if eq $port "14433/tcp"}}{{range $bindings}}{{println .HostIp .HostPort}}{{end}}{{end}}{{end}}' \
		yamata-pgadmin-nginx-beta
)"
pgadmin_listener_count="$(printf '%s\n' "$pgadmin_listener_binding" | awk 'NF { count++ } END { print count + 0 }')"
[[ "$pgadmin_listener_count" -eq 1 ]] ||
	die "Nginx must publish exactly one pgAdmin listener on 14433/tcp"
read -r pgadmin_listener_ip pgadmin_listener_port <<<"$pgadmin_listener_binding"
[[ "$pgadmin_listener_port" == "14433" && -n "$pgadmin_listener_ip" && \
	"$pgadmin_listener_ip" != "0.0.0.0" && "$pgadmin_listener_ip" != "::" && \
	"$pgadmin_listener_ip" != 127.* ]] ||
	die "Nginx pgAdmin listener must be bound to a specific host interface"
[[ "$(env_value yamata-pgadmin-beta PGADMIN_LISTEN_ADDRESS)" == "172.31.0.10" ]] ||
	die "pgAdmin listener is not restricted to its private Nginx network address"
[[ "$(env_value yamata-pgadmin-beta PGADMIN_LISTEN_PORT)" == "5050" ]] ||
	die "pgAdmin must listen on its private port 5050"
pgadmin_allowed_host="$(env_value yamata-pgadmin-beta PGADMIN_ALLOWED_HOST)"
[[ "$pgadmin_allowed_host" =~ ^pg\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$ ]] ||
	die "pgAdmin must restrict Host headers to a pg.<domain> name"
"${DOCKER[@]}" exec yamata-pgadmin-nginx-beta sh -ec \
	'nginx -T 2>&1 | grep -Fq "auth_basic_user_file /run/secrets/pgadmin_nginx_htpasswd;"' ||
	die "Dedicated Nginx pgAdmin virtual host is missing its Basic Auth secret"
"${DOCKER[@]}" exec yamata-pgadmin-nginx-beta sh -ec \
	'nginx -T 2>&1 | grep -Fq "server 172.31.0.10:5050"' ||
	die "Dedicated Nginx is not proxying pgAdmin over the dedicated internal address"
"${DOCKER[@]}" exec yamata-pgadmin-nginx-beta sh -ec \
	'nginx -T 2>&1 | grep -Fq "listen 14433 ssl;" && nginx -T 2>&1 | grep -Fq "proxy_send_timeout 300s;" && nginx -T 2>&1 | grep -Fq "proxy_read_timeout 300s;"' ||
	die "Dedicated Nginx pgAdmin virtual host must listen on 14433 with five-minute upstream timeouts"
"${DOCKER[@]}" exec --user 5050:0 yamata-pgadmin-beta sh -ec \
	'test -r /run/secrets/pgadmin_default_password && test ! -w /run/secrets/pgadmin_default_password' ||
	die "pgAdmin cannot safely read its password secret"
"${DOCKER[@]}" exec --user 65534:65534 yamata-pgadmin-nginx-beta sh -ec \
	'test -r /run/secrets/pgadmin_nginx_htpasswd && test ! -w /run/secrets/pgadmin_nginx_htpasswd' ||
	die "Dedicated Nginx workers cannot safely read the pgAdmin Basic Auth secret"
"${DOCKER[@]}" exec yamata-postgres-beta sh -ec \
	'grep -Fq "172.29.0.0/28  scram-sha-256" /etc/postgresql/pg_hba.conf' ||
	die "PostgreSQL pg_hba.conf must allow SCRAM only from the dedicated pgAdmin network"
log "pgAdmin is isolated, private, and protected by Nginx Basic Auth"

"${DOCKER[@]}" exec yamata-campaign-scheduler-beta \
	curl -fsS --noproxy app-beta http://app-beta:8080/api/v1/health >/dev/null ||
	die "Scheduler cannot reach the API over the private network"
"${DOCKER[@]}" exec yamata-nginx-beta nginx -t >/dev/null

app_password_hash="$(
	"${DOCKER[@]}" exec yamata-app-beta sh -c 'printf %s "$BOT_PASSWORD" | sha256sum' | awk '{print $1}'
)"
scheduler_password_hash="$(
	"${DOCKER[@]}" exec yamata-campaign-scheduler-beta sh -c 'printf %s "$BOT_PASSWORD" | sha256sum' | awk '{print $1}'
)"
[[ "$app_password_hash" == "$scheduler_password_hash" ]] ||
	die "BOT_PASSWORD differs between API and scheduler; run deploy-production-beta.sh"
[[ "$(env_value yamata-app-beta BOT_USERNAME)" == \
	"$(env_value yamata-campaign-scheduler-beta BOT_USERNAME)" ]] ||
	die "BOT_USERNAME differs between API and scheduler; run deploy-production-beta.sh"
log "BOT_PASSWORD hashes match (secret not displayed)"

schema_checks="$("${DOCKER[@]}" exec yamata-postgres-beta sh -lc \
	'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "
	 SELECT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''processed_campaigns'\''
	     AND column_name='\''bundle_audience_selection_id'\''
	 );
	 SELECT EXISTS (
	   SELECT 1 FROM pg_constraint
	   WHERE conrelid='\''public.processed_campaigns'\''::regclass
	     AND conname='\''processed_campaigns_single_audience_selection'\''
	 );
	 SELECT to_regclass('\''public.campaign_selected_tags'\'') IS NOT NULL;
	 SELECT to_regclass('\''public.campaign_targeting_capacity_calculations'\'') IS NOT NULL;
	 SELECT to_regclass('\''public.campaign_targeting_test_sampling_calculations'\'') IS NOT NULL;
		 SELECT to_regclass('\''public.src_reference'\'') IS NOT NULL;
		 SELECT to_regclass('\''public.idx_audience_profiles_campaign_id_phone'\'') IS NOT NULL;
	 SELECT to_regclass('\''public.bundle_audience_selection_members'\'') IS NOT NULL;
	 SELECT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''processed_campaigns'\''
	     AND column_name='\''is_current'\''
	 );
	 SELECT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''sent_bale_messages'\''
	     AND column_name='\''is_current'\''
	 );
	 SELECT NOT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''bundle_audience_selections'\''
	     AND column_name='\''audience_ids'\''
	 );
	 SELECT COALESCE((
	   SELECT indisunique AND pg_get_expr(indpred, indrelid) = '\''is_current'\''
	   FROM pg_index
	   WHERE indexrelid=to_regclass('\''public.uk_processed_campaigns_campaign_id'\'')
	 ), FALSE);
	 SELECT COALESCE((
	   SELECT indisunique AND pg_get_expr(indpred, indrelid) = '\''is_current'\''
	   FROM pg_index
	   WHERE indexrelid=to_regclass('\''public.uk_sent_bale_messages_processed_tracking'\'')
	 ), FALSE);
	 -- Legacy campaigns predate the current-checkpoint lifecycle. Enforce this
	 -- invariant only for campaigns created after the compatibility cutoff.
	 SELECT NOT EXISTS (
	   SELECT campaign_id FROM processed_campaigns
	   WHERE campaign_id >= 1001
	   GROUP BY campaign_id
	   HAVING COUNT(*) FILTER (WHERE is_current) <> 1
	 );
	 SELECT NOT EXISTS (
	   SELECT processed_campaign_id, tracking_id FROM sent_bale_messages
	   GROUP BY processed_campaign_id, tracking_id
	   HAVING COUNT(*) FILTER (WHERE is_current) <> 1
	 );
	 SELECT NOT EXISTS (
	   SELECT 1 FROM bundle_audience_selections AS selection
	   WHERE selection.audience_count <> (
	     SELECT COUNT(*) FROM bundle_audience_selection_members AS member
	     WHERE member.selection_id=selection.id
	   )
	 );
	 SELECT EXISTS (
	   SELECT 1 FROM pg_constraint
	   WHERE conrelid='\''public.bundle_audience_selection_members'\''::regclass
	     AND conname='\''fk_bundle_aud_sel_member_selection_bundle'\''
	 );
	 SELECT to_regclass('\''public.idx_processed_campaigns_capacity_reservation_materialized'\'') IS NULL;
	 SELECT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''campaigns'\''
	     AND column_name='\''sample_size_per_tag'\''
	 );
	 SELECT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''campaign_selected_tags'\''
	     AND column_name='\''selection_order'\''
	 );
	 SELECT EXISTS (
	   SELECT 1 FROM information_schema.columns
	   WHERE table_schema='\''public'\''
	     AND table_name='\''bundle_audience_selection_members'\''
	     AND column_name='\''selection_order'\''
	 );
	 SELECT to_regclass('\''public.campaign_audience_tag_attributions'\'') IS NOT NULL;
	 SELECT to_regclass('\''public.payam_sms_send_responses'\'') IS NOT NULL;
	 SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='\''pg_stat_statements'\'');
	 SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='\''pgstattuple'\'');
	 SELECT to_regclass('\''public.idx_audience_profiles_uid'\'') IS NULL;
	 SELECT to_regclass('\''public.idx_audience_profiles_phone_number'\'') IS NULL;"')"
[[ "$schema_checks" == $'t\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt\nt' ]] || die "Required migrations through 0131 are incomplete or inconsistent"

[[ -x "$PROJECT_DIR/scripts/check-yamata-certificates.sh" ]] ||
	die "Missing certificate checker in $PROJECT_DIR/scripts"
"$PROJECT_DIR/scripts/check-yamata-certificates.sh" \
	"$PROJECT_DIR/docker/nginx/sites-available/generated/beta/yamata.conf"

log "Production topology is valid"
