#!/usr/bin/env bash

# Restore a Yamata production backup created by backup-yamata-server.sh.
# Usage: ./restore-yamata-backup.sh BACKUP.tar.gz [PROJECT_DIR]
# Example: ./restore-yamata-backup.sh ~/yamata-backup-20260801T041255Z.tar.gz /srv/yamata

set -Eeuo pipefail
umask 077

readonly SCRIPT_NAME="$(basename "$0")"

log() {
	printf '[restore] %s\n' "$*"
}

warn() {
	printf '[restore] WARNING: %s\n' "$*" >&2
}

die() {
	printf '[restore] ERROR: %s\n' "$*" >&2
	exit 1
}

usage() {
	printf 'Usage: %s BACKUP.tar.gz [PROJECT_DIR]\n' "$SCRIPT_NAME" >&2
	exit 2
}

[[ $# -ge 1 && $# -le 2 ]] || usage

BACKUP_ARCHIVE="$(readlink -f "$1")"
PROJECT_DIR="${2:-/srv/yamata}"
readonly BACKUP_ARCHIVE PROJECT_DIR

[[ -f "$BACKUP_ARCHIVE" ]] || die "Backup archive not found: $BACKUP_ARCHIVE"
[[ "$PROJECT_DIR" == /* ]] || die "PROJECT_DIR must be an absolute path"
case "$PROJECT_DIR" in
	/|/srv|/home|/root|/usr|/var) die "Unsafe PROJECT_DIR: $PROJECT_DIR" ;;
esac
[[ ! -e "$PROJECT_DIR" ]] || die "PROJECT_DIR already exists: $PROJECT_DIR"

for command_name in tar gzip sha256sum find awk grep sed openssl envsubst; do
	command -v "$command_name" >/dev/null 2>&1 || die "Required command is missing: $command_name"
done

if [[ $EUID -ne 0 ]]; then
	command -v sudo >/dev/null 2>&1 || die "sudo is required when not running as root"
	sudo -v
fi

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	die "Docker is unavailable; install/start Docker Engine and Compose v2 first"
fi
readonly DOCKER

"${DOCKER[@]}" compose version >/dev/null 2>&1 || die "Docker Compose v2 is unavailable"

for container_name in \
	yamata-postgres-beta yamata-redis-beta yamata-postgres-backup-beta \
	yamata-sentry-postgres-beta yamata-sentry-redis-beta yamata-sentry-beta \
	yamata-app-beta yamata-nginx-beta yamata-nginx-sentry-forwarder-beta \
	yamata-cert-monitor-beta yamata-prometheus-beta yamata-grafana-beta \
	yamata-postgres-exporter-beta yamata-node-exporter-beta \
	yamata-cadvisor-beta yamata-campaign-scheduler-beta frontend-beta; do
	if "${DOCKER[@]}" container inspect "$container_name" >/dev/null 2>&1; then
		die "Container already exists; target must be fresh: $container_name"
	fi
done

for container_name in yamata-pgadmin-beta yamata-pgadmin-nginx-beta; do
	if "${DOCKER[@]}" container inspect "$container_name" >/dev/null 2>&1; then
		die "Container already exists; target must be fresh: $container_name"
	fi
done

if ip route show 2>/dev/null | grep -Eq '(^|[[:space:]])172\.30\.0\.0/24([[:space:]]|$)'; then
	die "172.30.0.0/24 conflicts with an existing host route"
fi

WORK_ROOT="${RESTORE_WORK_ROOT:-$(dirname "$BACKUP_ARCHIVE")}" 
mkdir -p "$WORK_ROOT"
WORK_DIR="$(mktemp -d "$WORK_ROOT/.yamata-restore.XXXXXX")"
readonly WORK_DIR

cleanup() {
	if [[ -n "${WORK_DIR:-}" && -d "$WORK_DIR" ]]; then
		rm -rf -- "$WORK_DIR"
	fi
}
trap cleanup EXIT

log "Extracting outer backup archive"
tar -xzf "$BACKUP_ARCHIVE" -C "$WORK_DIR"

BACKUP_DIR="$(find "$WORK_DIR" -mindepth 1 -maxdepth 1 -type d -name 'yamata-backup-*' -print -quit)"
[[ -n "$BACKUP_DIR" ]] || die "The archive does not contain a yamata-backup-* directory"
readonly BACKUP_DIR

SOURCE_ARCHITECTURE="amd64"
if [[ -f "$BACKUP_DIR/manifests/image-architecture.txt" ]]; then
	SOURCE_ARCHITECTURE="$(tr -d '[:space:]' < "$BACKUP_DIR/manifests/image-architecture.txt")"
elif [[ -f "$BACKUP_DIR/manifests/host-architecture.txt" ]]; then
	SOURCE_ARCHITECTURE="$(tr -d '[:space:]' < "$BACKUP_DIR/manifests/host-architecture.txt")"
fi
case "$SOURCE_ARCHITECTURE" in
	x86_64) SOURCE_ARCHITECTURE=amd64 ;;
	aarch64) SOURCE_ARCHITECTURE=arm64 ;;
esac
TARGET_ARCHITECTURE="$(uname -m)"
case "$TARGET_ARCHITECTURE" in
	x86_64) TARGET_ARCHITECTURE=amd64 ;;
	aarch64) TARGET_ARCHITECTURE=arm64 ;;
esac
[[ "$SOURCE_ARCHITECTURE" == "$TARGET_ARCHITECTURE" ]] ||
	die "Backup architecture $SOURCE_ARCHITECTURE does not match target $TARGET_ARCHITECTURE"
readonly SOURCE_ARCHITECTURE TARGET_ARCHITECTURE

for required_file in \
	SHA256SUMS \
	db/yamata-main.dump \
	db/yamata-sentry.dump \
	repo/yamata-project.tar.gz \
	repo/docker-compose.beta.yml \
	certs/letsencrypt.tar.gz \
	redis/redi_data_beta.tar.gz \
	redis/sentry_redis_data_beta.tar.gz; do
	[[ -f "$BACKUP_DIR/$required_file" ]] || die "Backup member is missing: $required_file"
done

SOURCE_POSTGRES_VOLUME=""
if [[ -f "$BACKUP_DIR/manifests/docker-volumes.txt" ]]; then
	SOURCE_POSTGRES_VOLUME="$(awk '
		NR > 1 &&
		($2 == "postgres_data_beta" || $2 ~ /_postgres_data_beta$/) &&
		$2 !~ /_sentry_postgres_data_beta$/ {
			print $2
			exit
		}
	' "$BACKUP_DIR/manifests/docker-volumes.txt")"
fi

if [[ "$SOURCE_POSTGRES_VOLUME" == *_postgres_data_beta ]]; then
	COMPOSE_PROJECT_NAME="${SOURCE_POSTGRES_VOLUME%_postgres_data_beta}"
else
	# This is the normalized default for the backed-up Yamata-no-Orochi directory.
	COMPOSE_PROJECT_NAME="yamata-no-orochi"
fi
readonly COMPOSE_PROJECT_NAME
log "Using Compose project/volume prefix: $COMPOSE_PROJECT_NAME"

while IFS= read -r existing_volume; do
	case "$existing_volume" in
		"${COMPOSE_PROJECT_NAME}"_*)
			die "Target volume already exists; target must be fresh: $existing_volume"
			;;
	esac
done < <("${DOCKER[@]}" volume ls --format '{{.Name}}')

log "Verifying backup checksums"
# Exclude only the self-referential checksum entry; verify every payload file.
awk '
	$2 != "./SHA256SUMS" &&
	$2 != "SHA256SUMS"
' \
	"$BACKUP_DIR/SHA256SUMS" > "$WORK_DIR/SHA256SUMS.payload"
(
	cd "$BACKUP_DIR"
	sha256sum -c "$WORK_DIR/SHA256SUMS.payload"
)

for archive in "$BACKUP_DIR"/{certs,redis,repo,volumes,secrets}/*.tar.gz; do
	[[ -e "$archive" ]] || continue
	tar -tzf "$archive" >/dev/null || die "Invalid nested archive: $archive"
	if ! tar -tzf "$archive" | awk '
		$0 !~ /^\.?\/?$/ { found = 1 }
		END { exit(found ? 0 : 1) }
	'; then
		warn "Nested archive contains no files: $archive"
	fi
done

log "Loading Docker images"
for image_archive in "$BACKUP_DIR"/images/*.tar.gz; do
	[[ -e "$image_archive" ]] || die "No Docker images found in the backup"
	gzip -dc "$image_archive" | "${DOCKER[@]}" load
done

for image_name in \
	postgres:15-alpine redis:8.0.3-alpine glitchtip/glitchtip:latest \
	python:3.12-alpine prom/prometheus:v3.5.0 \
	grafana/grafana:12.1.0-ubuntu \
	quay.io/prometheuscommunity/postgres-exporter:v0.15.0 \
	prom/node-exporter:v1.10.2 gcr.io/cadvisor/cadvisor:v0.49.2 \
	yamata-no-orochi yamata-nginx-beta yamata-cert-monitor-beta \
	yamata-frontend-beta; do
	"${DOCKER[@]}" image inspect "$image_name" >/dev/null 2>&1 || \
		die "Required image was not loaded: $image_name"
done

log "Validating PostgreSQL dumps"
"${DOCKER[@]}" run --rm --entrypoint pg_restore \
	-v "$BACKUP_DIR/db/yamata-main.dump:/backup.dump:ro" \
	postgres:15-alpine -l /backup.dump >/dev/null
"${DOCKER[@]}" run --rm --entrypoint pg_restore \
	-v "$BACKUP_DIR/db/yamata-sentry.dump:/backup.dump:ro" \
	postgres:15-alpine -l /backup.dump >/dev/null

if ! tar -tzf "$BACKUP_DIR/redis/redi_data_beta.tar.gz" | awk '
	/(^|\/)dump\.rdb$/ { found = 1 }
	END { exit(found ? 0 : 1) }
'; then
	die "Main Redis archive has no dump.rdb; it was not backed up from the active Compose volume"
fi
if ! tar -tzf "$BACKUP_DIR/redis/sentry_redis_data_beta.tar.gz" | awk '
	/(^|\/)dump\.rdb$/ { found = 1 }
	END { exit(found ? 0 : 1) }
'; then
	die "Sentry Redis archive has no dump.rdb; it was not backed up from the active Compose volume"
fi

log "Restoring project into $PROJECT_DIR"
PROJECT_STAGE="$WORK_DIR/project"
mkdir -p "$PROJECT_STAGE"
tar -xzf "$BACKUP_DIR/repo/yamata-project.tar.gz" -C "$PROJECT_STAGE"

COMPOSE_FILE_FOUND="$(find "$PROJECT_STAGE" -type f -name docker-compose.beta.yml -print -quit)"
[[ -n "$COMPOSE_FILE_FOUND" ]] || die "docker-compose.beta.yml is absent from yamata-project.tar.gz"
SOURCE_PROJECT_DIR="$(dirname "$COMPOSE_FILE_FOUND")"

PROJECT_PARENT="$(dirname "$PROJECT_DIR")"
if [[ $EUID -eq 0 ]]; then
	mkdir -p "$PROJECT_PARENT"
	mv "$SOURCE_PROJECT_DIR" "$PROJECT_DIR"
else
	sudo mkdir -p "$PROJECT_PARENT"
	sudo mv "$SOURCE_PROJECT_DIR" "$PROJECT_DIR"
	sudo chown -R "$(id -u):$(id -g)" "$PROJECT_DIR"
fi

if [[ -f "$BACKUP_DIR/repo/.env.beta" ]]; then
	install -m 600 "$BACKUP_DIR/repo/.env.beta" "$PROJECT_DIR/.env.beta"
fi
[[ -f "$PROJECT_DIR/.env.beta" ]] || die ".env.beta is missing from both repo backups"
chmod 600 "$PROJECT_DIR/.env.beta"

cd "$PROJECT_DIR"

set -a
# shellcheck disable=SC1091
source .env.beta
set +a

# Campaign execution belongs exclusively to the isolated scheduler container.
if grep -q '^CAMPAIGN_EXECUTION_ENABLED=' .env.beta; then
	sed -i 's/^CAMPAIGN_EXECUTION_ENABLED=.*/CAMPAIGN_EXECUTION_ENABLED=false/' .env.beta
else
	printf '\nCAMPAIGN_EXECUTION_ENABLED=false\n' >>.env.beta
fi
export CAMPAIGN_EXECUTION_ENABLED=false

# The steady-state API owns exact-capacity and smart-tag jobs. Keep those
# settings durable in .env.beta; they are overridden only while imports run.
if grep -q '^SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=' .env.beta; then
	sed -i 's/^SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=.*/SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true/' .env.beta
else
	printf '\nSMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true\n' >>.env.beta
fi
if grep -q '^SMART_TAG_EVALUATION_ENABLED=' .env.beta; then
	sed -i 's/^SMART_TAG_EVALUATION_ENABLED=.*/SMART_TAG_EVALUATION_ENABLED=true/' .env.beta
else
	printf '\nSMART_TAG_EVALUATION_ENABLED=true\n' >>.env.beta
fi
if grep -q '^SMART_TAG_EVALUATION_SCHEDULER_ENABLED=' .env.beta; then
	sed -i 's/^SMART_TAG_EVALUATION_SCHEDULER_ENABLED=.*/SMART_TAG_EVALUATION_SCHEDULER_ENABLED=true/' .env.beta
else
	printf '\nSMART_TAG_EVALUATION_SCHEDULER_ENABLED=true\n' >>.env.beta
fi

: "${DB_NAME:?DB_NAME is missing from .env.beta}"
: "${DB_USER:?DB_USER is missing from .env.beta}"
: "${SENTRY_POSTGRES_DB:?SENTRY_POSTGRES_DB is missing from .env.beta}"
: "${SENTRY_POSTGRES_USER:?SENTRY_POSTGRES_USER is missing from .env.beta}"
: "${DOMAIN:?DOMAIN is missing from .env.beta}"
: "${MONITORING_DOMAIN:?MONITORING_DOMAIN is missing from .env.beta}"
: "${SENTRY_UI_DOMAIN:?SENTRY_UI_DOMAIN is missing from .env.beta}"

if grep -q '^COMPOSE_PROJECT_NAME=' .env.beta; then
	sed -i "s/^COMPOSE_PROJECT_NAME=.*/COMPOSE_PROJECT_NAME=$COMPOSE_PROJECT_NAME/" .env.beta
else
	printf '\nCOMPOSE_PROJECT_NAME=%s\n' "$COMPOSE_PROJECT_NAME" >> .env.beta
fi
export COMPOSE_PROJECT_NAME

for prompt_file in \
	SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT \
	SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT; do
	[[ -f "$prompt_file" ]] || die "Required runtime prompt is missing: $prompt_file"
	chmod 644 "$prompt_file"
done

log "Generating runtime PostgreSQL and Nginx files"
mkdir -p docker/nginx/sites-available/generated/beta docker/prometheus/rules
bash docker/postgres/process-init-beta.sh
envsubst '$DOMAIN $API_DOMAIN $MONITORING_DOMAIN $SENTRY_UI_DOMAIN $HSTS_MAX_AGE $GLOBAL_RATE_LIMIT $AUTH_RATE_LIMIT' \
	< docker/nginx/sites-available/yamata-beta.conf \
	> docker/nginx/sites-available/generated/beta/yamata.conf

find docker -type d -exec chmod 755 {} +
find docker -type f -exec chmod 644 {} +
chmod 755 docker/postgres/process-init-beta.sh docker/redis/start-redis.sh

log "Restoring Let's Encrypt certificates"
if [[ $EUID -eq 0 ]]; then
	tar -xzf "$BACKUP_DIR/certs/letsencrypt.tar.gz" -C /
else
	sudo tar -xzf "$BACKUP_DIR/certs/letsencrypt.tar.gz" -C /
fi

if [[ -f "$BACKUP_DIR/secrets/runtime-files.tar.gz" ]]; then
	log "Restoring configured file-backed secrets and certificates to their original absolute paths"
	if [[ $EUID -eq 0 ]]; then
		tar -xzf "$BACKUP_DIR/secrets/runtime-files.tar.gz" -C /
	else
		sudo tar -xzf "$BACKUP_DIR/secrets/runtime-files.tar.gz" -C /
	fi
fi

if [[ -f "$BACKUP_DIR/host/etc-hosts" ]]; then
	log "Restoring /etc/hosts from the source server"
	if [[ $EUID -eq 0 ]]; then
		install -m 644 "$BACKUP_DIR/host/etc-hosts" /etc/hosts
	else
		sudo install -m 644 "$BACKUP_DIR/host/etc-hosts" /etc/hosts
	fi
fi
if [[ -f "$BACKUP_DIR/host/docker-daemon.json" ]]; then
	warn "Source Docker daemon configuration is available at $BACKUP_DIR/host/docker-daemon.json; review and apply it manually before restarting Docker"
fi

while IFS= read -r certificate_path; do
	[[ -n "$certificate_path" ]] || continue
	if [[ $EUID -eq 0 ]]; then
		test -f "$certificate_path" || die "Certificate file is missing: $certificate_path"
		if [[ "$certificate_path" == *privkey* ]]; then
			openssl pkey -in "$certificate_path" -check -noout >/dev/null 2>&1 || \
				die "Private key is invalid: $certificate_path"
		else
			openssl x509 -in "$certificate_path" -checkend 0 -noout >/dev/null 2>&1 || \
				die "Certificate is invalid or expired: $certificate_path"
		fi
	else
		sudo test -f "$certificate_path" || die "Certificate file is missing: $certificate_path"
		if [[ "$certificate_path" == *privkey* ]]; then
			sudo openssl pkey -in "$certificate_path" -check -noout >/dev/null 2>&1 || \
				die "Private key is invalid: $certificate_path"
		else
			sudo openssl x509 -in "$certificate_path" -checkend 0 -noout >/dev/null 2>&1 || \
				die "Certificate is invalid or expired: $certificate_path"
		fi
	fi
	log "Validated certificate material: $certificate_path"
done < <(
	awk '/^[[:space:]]*ssl_(certificate|certificate_key|trusted_certificate)[[:space:]]/ {
		gsub(/;/, "", $2); print $2
	}' docker/nginx/sites-available/generated/beta/yamata.conf | sort -u
)

COMPOSE=("${DOCKER[@]}" compose --env-file "$PROJECT_DIR/.env.beta" -f "$PROJECT_DIR/docker-compose.beta.yml")
readonly COMPOSE

"${COMPOSE[@]}" config -q

log "Creating empty Compose containers and named volumes"
"${COMPOSE[@]}" create

restore_volume() {
	local archive="$1"
	local container="$2"
	local destination="$3"
	local volume

	[[ -f "$archive" ]] || return 0
	volume="$("${DOCKER[@]}" inspect --format \
		'{{range .Mounts}}{{if eq .Destination "'"$destination"'"}}{{.Name}}{{end}}{{end}}' \
		"$container")"
	[[ -n "$volume" ]] || die "Cannot resolve $container volume mounted at $destination"

	log "Restoring $(basename "$archive") into $volume"
	"${DOCKER[@]}" run --rm --entrypoint sh \
		-v "$volume:/to" \
		-v "$archive:/backup.tar.gz:ro" \
		postgres:15-alpine -c 'tar -C /to -xzf /backup.tar.gz'
}

log "Restoring Docker volumes"
restore_volume "$BACKUP_DIR/redis/redi_data_beta.tar.gz" \
	yamata-redis-beta /data
restore_volume "$BACKUP_DIR/redis/sentry_redis_data_beta.tar.gz" \
	yamata-sentry-redis-beta /data
restore_volume "$BACKUP_DIR/volumes/uploads_beta.tar.gz" \
	yamata-app-beta /data
restore_volume "$BACKUP_DIR/volumes/app_logs_beta.tar.gz" \
	yamata-app-beta /var/log/yamata
restore_volume "$BACKUP_DIR/volumes/nginx_logs_beta.tar.gz" \
	yamata-nginx-beta /var/log/nginx
restore_volume "$BACKUP_DIR/volumes/frontend_logs_beta.tar.gz" \
	frontend-beta /var/log/nginx
restore_volume "$BACKUP_DIR/volumes/grafana_data_beta.tar.gz" \
	yamata-grafana-beta /var/lib/grafana
restore_volume "$BACKUP_DIR/volumes/prometheus_data_beta.tar.gz" \
	yamata-prometheus-beta /prometheus
restore_volume "$BACKUP_DIR/volumes/postgres_backups_beta.tar.gz" \
	yamata-postgres-backup-beta /backups
restore_volume "$BACKUP_DIR/volumes/pgadmin_data_beta.tar.gz" \
	yamata-pgadmin-beta /var/lib/pgadmin
restore_volume "$BACKUP_DIR/volumes/pgadmin_nginx_logs_beta.tar.gz" \
	yamata-pgadmin-nginx-beta /var/log/nginx
if [[ -f "$BACKUP_DIR/volumes/campaign_scheduler_logs_beta.tar.gz" ]]; then
	log "Restoring isolated campaign scheduler logs"
	"${DOCKER[@]}" volume create yamata-campaign-scheduler-logs-beta >/dev/null
	"${DOCKER[@]}" run --rm --entrypoint sh \
		-v yamata-campaign-scheduler-logs-beta:/to \
		-v "$BACKUP_DIR/volumes/campaign_scheduler_logs_beta.tar.gz:/backup.tar.gz:ro" \
		postgres:15-alpine -c 'tar -C /to -xzf /backup.tar.gz'
fi

wait_for_postgres() {
	local container="$1"
	local user="$2"
	local attempt

	for attempt in $(seq 1 90); do
		if "${DOCKER[@]}" exec "$container" pg_isready -U "$user" -d postgres >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	die "PostgreSQL did not become ready: $container"
}

log "Starting PostgreSQL containers"
"${COMPOSE[@]}" start postgres-beta sentry-postgres-beta
wait_for_postgres yamata-postgres-beta "$DB_USER"
wait_for_postgres yamata-sentry-postgres-beta "$SENTRY_POSTGRES_USER"
sleep 5

log "Restoring main PostgreSQL database"
"${DOCKER[@]}" exec yamata-postgres-beta \
	dropdb --if-exists --force -U "$DB_USER" "$DB_NAME"
"${DOCKER[@]}" exec yamata-postgres-beta \
	createdb -T template0 -U "$DB_USER" -O "$DB_USER" "$DB_NAME"
"${DOCKER[@]}" exec -i yamata-postgres-beta pg_restore \
	--exit-on-error --no-owner --no-privileges \
	-U "$DB_USER" -d "$DB_NAME" < "$BACKUP_DIR/db/yamata-main.dump"

log "Applying schema changes required by selective data imports and current app"
bash "$PROJECT_DIR/scripts/apply-yamata-required-migrations.sh" --repair "$PROJECT_DIR"

log "Restoring Sentry/GlitchTip PostgreSQL database"
"${DOCKER[@]}" exec yamata-sentry-postgres-beta \
	dropdb --if-exists --force -U "$SENTRY_POSTGRES_USER" "$SENTRY_POSTGRES_DB"
"${DOCKER[@]}" exec yamata-sentry-postgres-beta \
	createdb -T template0 -U "$SENTRY_POSTGRES_USER" \
	-O "$SENTRY_POSTGRES_USER" "$SENTRY_POSTGRES_DB"
"${DOCKER[@]}" exec -i yamata-sentry-postgres-beta pg_restore \
	--exit-on-error --no-owner --no-privileges \
	-U "$SENTRY_POSTGRES_USER" -d "$SENTRY_POSTGRES_DB" \
	< "$BACKUP_DIR/db/yamata-sentry.dump"

"${DOCKER[@]}" exec yamata-postgres-beta \
	vacuumdb -U "$DB_USER" -d "$DB_NAME" --analyze-in-stages

log "Starting infrastructure"
export SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=false
export SMART_TAG_EVALUATION_SCHEDULER_ENABLED=false

"${COMPOSE[@]}" up -d \
	redis-beta sentry-redis-beta sentry-beta \
	prometheus-beta grafana-beta postgres-exporter-beta \
	node-exporter-beta cadvisor-beta frontend-beta \
	pgadmin-beta pgadmin-nginx-beta

log "Starting application with campaign, exact-capacity, and smart-tag schedulers disabled"
"${COMPOSE[@]}" up -d --force-recreate app-beta

for attempt in $(seq 1 90); do
	app_health="$("${DOCKER[@]}" inspect \
		--format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' \
		yamata-app-beta 2>/dev/null || true)"
	[[ "$app_health" == healthy ]] && break
	[[ "$attempt" -eq 90 ]] && die "Application did not become healthy (status: $app_health)"
	sleep 2
done

log "Starting edge and monitoring sidecars; PostgreSQL backup remains stopped during selective imports"
"${COMPOSE[@]}" up -d \
	nginx-beta cert-monitor-beta nginx-sentry-forwarder-beta

"${DOCKER[@]}" exec yamata-app-beta \
	curl -fsS http://localhost:8080/api/v1/health >/dev/null
"${DOCKER[@]}" exec yamata-nginx-beta nginx -t
"${DOCKER[@]}" exec yamata-redis-beta redis-cli PING
"${DOCKER[@]}" exec yamata-postgres-beta \
	psql -U "$DB_USER" -d "$DB_NAME" -Atc \
	'SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = '\''public'\'';'

"${COMPOSE[@]}" ps

cat <<EOF

Restore completed successfully.

The restored API is running with campaign execution disabled. Before DNS cutover:
  curl --resolve '${DOMAIN}:443:NEW_SERVER_IP' 'https://${DOMAIN}/api/v1/health'

After selective data imports finish, recreate the API with its database-job
schedulers enabled and start the isolated campaign scheduler with:
  cd '$PROJECT_DIR'
  ./scripts/deploy-production-beta.sh --domain '$DOMAIN'

Then start periodic PostgreSQL backups with:
  docker compose --env-file .env.beta -f docker-compose.beta.yml up -d postgres-backup-beta

Keep CAMPAIGN_EXECUTION_ENABLED=false, SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true,
and SMART_TAG_EVALUATION_SCHEDULER_ENABLED=true in .env.beta.
EOF
