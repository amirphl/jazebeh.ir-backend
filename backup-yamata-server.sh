#!/usr/bin/env bash

# Create a backup consumed by restore-yamata-backup.sh.
# Run from the repository root, or pass --project-dir explicitly.

set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"
OUTPUT_PARENT="$HOME"
LEAVE_STOPPED=false
MAIN_DB_BACKUP="latest"

log() {
	printf '[backup] %s\n' "$*"
}

warn() {
	printf '[backup] WARNING: %s\n' "$*" >&2
}

die() {
	printf '[backup] ERROR: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat >&2 <<EOF
Usage: $(basename "$0") [options]

Options:
  --project-dir DIR   Project containing .env.beta and docker-compose.beta.yml
                      (default: directory containing this script)
  --output-dir DIR    Parent directory for the backup (default: \$HOME)
  --main-db-backup X  Use X for the main database. X is an absolute .dump path
                      or "latest" for the newest completed scheduled dump
                      (default: latest); the live main database is never dumped
  --leave-stopped     Deprecated compatibility option; this script is online
  --help              Show this help
EOF
	exit 2
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--project-dir)
			[[ $# -ge 2 ]] || usage
			PROJECT_DIR="$2"
			shift 2
			;;
		--output-dir)
			[[ $# -ge 2 ]] || usage
			OUTPUT_PARENT="$2"
			shift 2
			;;
		--leave-stopped)
			LEAVE_STOPPED=true
			shift
			;;
		--main-db-backup)
			[[ $# -ge 2 ]] || usage
			MAIN_DB_BACKUP="$2"
			shift 2
			;;
		--help|-h)
			usage
			;;
		*)
			die "Unknown option: $1"
			;;
	esac
done

PROJECT_DIR="$(readlink -f "$PROJECT_DIR")"
mkdir -p "$OUTPUT_PARENT"
OUTPUT_PARENT="$(readlink -f "$OUTPUT_PARENT")"
readonly PROJECT_DIR OUTPUT_PARENT LEAVE_STOPPED MAIN_DB_BACKUP

[[ -f "$PROJECT_DIR/.env.beta" ]] || die "Missing $PROJECT_DIR/.env.beta"
[[ -f "$PROJECT_DIR/docker-compose.beta.yml" ]] || \
	die "Missing $PROJECT_DIR/docker-compose.beta.yml"
for prompt_file in \
	SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT \
	SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT; do
	[[ -f "$PROJECT_DIR/$prompt_file" ]] || die "Missing runtime prompt: $PROJECT_DIR/$prompt_file"
done

case "$OUTPUT_PARENT/" in
	"$PROJECT_DIR/"*) die "Output directory must not be inside the project" ;;
esac

for command_name in docker tar gzip sha256sum find sort awk grep readlink openssl stat; do
	command -v "$command_name" >/dev/null 2>&1 || die "Required command is missing: $command_name"
done

if [[ $EUID -ne 0 ]]; then
	command -v sudo >/dev/null 2>&1 || die "sudo is required to archive /etc/letsencrypt"
	sudo -v
fi

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif [[ $EUID -ne 0 ]] && sudo docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	die "Docker is unavailable"
fi
readonly DOCKER

"${DOCKER[@]}" compose version >/dev/null 2>&1 || die "Docker Compose v2 is unavailable"

cd "$PROJECT_DIR"
# shellcheck disable=SC1091
source "$PROJECT_DIR/scripts/load-yamata-env.sh"
load_yamata_env_file "$PROJECT_DIR/.env.beta" || \
	die "Could not safely parse $PROJECT_DIR/.env.beta"

: "${DB_NAME:?DB_NAME is missing from .env.beta}"
: "${DB_USER:?DB_USER is missing from .env.beta}"
: "${SENTRY_POSTGRES_DB:?SENTRY_POSTGRES_DB is missing from .env.beta}"
: "${SENTRY_POSTGRES_USER:?SENTRY_POSTGRES_USER is missing from .env.beta}"

COMPOSE=("${DOCKER[@]}" compose --env-file "$PROJECT_DIR/.env.beta" -f "$PROJECT_DIR/docker-compose.beta.yml")
readonly COMPOSE

for required_container in \
	yamata-postgres-beta yamata-redis-beta yamata-postgres-backup-beta \
	yamata-sentry-postgres-beta yamata-sentry-redis-beta \
	yamata-app-beta yamata-nginx-beta frontend-beta; do
	"${DOCKER[@]}" container inspect "$required_container" >/dev/null 2>&1 || \
		die "Required container does not exist: $required_container"
done

for required_running_container in \
	yamata-postgres-beta yamata-redis-beta \
	yamata-sentry-postgres-beta yamata-sentry-redis-beta; do
	container_running="$("${DOCKER[@]}" inspect --format '{{.State.Running}}' "$required_running_container")"
	[[ "$container_running" == true ]] || die "Required container is not running: $required_running_container"
done

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_NAME="yamata-backup-$TIMESTAMP"
BACKUP_ROOT="$OUTPUT_PARENT/$BACKUP_NAME"
FINAL_ARCHIVE="$BACKUP_ROOT.tar.gz"
readonly TIMESTAMP BACKUP_NAME BACKUP_ROOT FINAL_ARCHIVE

[[ ! -e "$BACKUP_ROOT" ]] || die "Backup directory already exists: $BACKUP_ROOT"
[[ ! -e "$FINAL_ARCHIVE" ]] || die "Backup archive already exists: $FINAL_ARCHIVE"

mkdir -p "$BACKUP_ROOT"/{db,redis,volumes,certs,secrets,repo,images,manifests}
chmod 700 "$BACKUP_ROOT"

BACKUP_SUCCEEDED=false

log "Recording source manifests"
uname -m > "$BACKUP_ROOT/manifests/host-architecture.txt"
"${DOCKER[@]}" image inspect --format '{{.Architecture}}' yamata-no-orochi \
	> "$BACKUP_ROOT/manifests/image-architecture.txt"
"${COMPOSE[@]}" ps > "$BACKUP_ROOT/manifests/compose-ps.txt"
"${DOCKER[@]}" volume ls > "$BACKUP_ROOT/manifests/docker-volumes.txt"
"${DOCKER[@]}" image ls > "$BACKUP_ROOT/manifests/docker-images.txt"
"${DOCKER[@]}" network ls > "$BACKUP_ROOT/manifests/docker-networks.txt"
"${DOCKER[@]}" inspect yamata-postgres-beta > \
	"$BACKUP_ROOT/manifests/yamata-postgres-beta.inspect.json"
"${DOCKER[@]}" inspect yamata-redis-beta > \
	"$BACKUP_ROOT/manifests/yamata-redis-beta.inspect.json"
if "${DOCKER[@]}" container inspect yamata-campaign-scheduler-beta >/dev/null 2>&1; then
	"${DOCKER[@]}" inspect yamata-campaign-scheduler-beta > \
		"$BACKUP_ROOT/manifests/yamata-campaign-scheduler-beta.inspect.json"
fi

install -m 600 "$PROJECT_DIR/.env.beta" "$BACKUP_ROOT/repo/.env.beta"
install -m 600 "$PROJECT_DIR/docker-compose.beta.yml" \
	"$BACKUP_ROOT/repo/docker-compose.beta.yml"

log "Validating configured certificates without issuing or renewing"
NGINX_GENERATED_CONFIG="$PROJECT_DIR/docker/nginx/sites-available/generated/beta/yamata.conf"
[[ -f "$NGINX_GENERATED_CONFIG" ]] || die "Missing generated Nginx config: $NGINX_GENERATED_CONFIG"

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
done < <(
	awk '/^[[:space:]]*ssl_(certificate|certificate_key|trusted_certificate)[[:space:]]/ {
		gsub(/;/, "", $2); print $2
	}' "$NGINX_GENERATED_CONFIG" | sort -u
)

if [[ "$("${DOCKER[@]}" inspect --format '{{.State.Running}}' yamata-nginx-beta)" == true ]]; then
	"${DOCKER[@]}" exec yamata-nginx-beta nginx -t
fi

if [[ "$LEAVE_STOPPED" == true ]]; then
	warn "--leave-stopped is ignored: this backup deliberately never stops production"
fi

request_redis_snapshot() {
	local container="$1"
	local previous_snapshot current_snapshot attempt

	previous_snapshot="$("${DOCKER[@]}" exec "$container" redis-cli --raw LASTSAVE)"
	# SAVE blocks Redis while it writes. BGSAVE SCHEDULE performs the same RDB
	# snapshot in a child process and waits for any existing background save.
	"${DOCKER[@]}" exec "$container" redis-cli BGSAVE SCHEDULE >/dev/null
	for attempt in $(seq 1 900); do
		current_snapshot="$("${DOCKER[@]}" exec "$container" redis-cli --raw LASTSAVE)"
		if [[ "$current_snapshot" -gt "$previous_snapshot" ]]; then
			return 0
		fi
		sleep 1
	done
	die "Timed out waiting for Redis background snapshot: $container"
}

log "Requesting Redis background point-in-time snapshots without stopping services"
request_redis_snapshot yamata-redis-beta
request_redis_snapshot yamata-sentry-redis-beta

postgres_backup_volume="$("${DOCKER[@]}" inspect --format '{{range .Mounts}}{{if and (eq .Type "volume") (eq .Destination "/backups")}}{{.Name}}{{end}}{{end}}' yamata-postgres-backup-beta)"
[[ -n "$postgres_backup_volume" ]] || die "Cannot resolve postgres backup volume"
log "Copying the scheduled main PostgreSQL dump; live yamata-postgres-beta is not dumped"
if [[ "$MAIN_DB_BACKUP" == latest ]]; then
	latest_main_dump="$("${DOCKER[@]}" run --rm --entrypoint sh -v "$postgres_backup_volume:/from:ro" postgres:15-alpine -c 'find /from -maxdepth 1 -type f -name "*.dump" -exec basename {} \; | sort | tail -n 1')"
	[[ -n "$latest_main_dump" ]] || die "No completed *.dump exists in postgres_backups_beta"
	"${DOCKER[@]}" run --rm --entrypoint sh -v "$postgres_backup_volume:/from:ro" -v "$BACKUP_ROOT/db:/to" postgres:15-alpine -c "test -f /from/$latest_main_dump && cp /from/$latest_main_dump /to/yamata-main.dump"
	printf '%s\n' "$latest_main_dump" > "$BACKUP_ROOT/manifests/main-db-backup-source.txt"
else
	[[ "$MAIN_DB_BACKUP" == /* && -f "$MAIN_DB_BACKUP" ]] || die "--main-db-backup must be latest or an existing absolute path"
	install -m 600 "$MAIN_DB_BACKUP" "$BACKUP_ROOT/db/yamata-main.dump"
	printf '%s\n' "$MAIN_DB_BACKUP" > "$BACKUP_ROOT/manifests/main-db-backup-source.txt"
fi
"${DOCKER[@]}" run --rm --entrypoint pg_restore -v "$BACKUP_ROOT/db/yamata-main.dump:/backup.dump:ro" postgres:15-alpine -l /backup.dump >/dev/null || die "Selected main PostgreSQL dump is invalid"

log "Dumping Sentry PostgreSQL online"
"${DOCKER[@]}" exec yamata-sentry-postgres-beta pg_dump \
	-U "$SENTRY_POSTGRES_USER" -d "$SENTRY_POSTGRES_DB" -Fc \
	> "$BACKUP_ROOT/db/yamata-sentry.dump"

"${DOCKER[@]}" exec -i yamata-sentry-postgres-beta pg_restore -l \
	< "$BACKUP_ROOT/db/yamata-sentry.dump" >/dev/null

backup_volume() {
	local container="$1"
	local destination="$2"
	local output_dir="$3"
	local output_file="$4"
	local volume

	volume="$("${DOCKER[@]}" inspect --format \
		'{{range .Mounts}}{{if and (eq .Type "volume") (eq .Destination "'"$destination"'")}}{{.Name}}{{end}}{{end}}' \
		"$container")"
	[[ -n "$volume" ]] || die "Cannot resolve $container volume mounted at $destination"

	log "Archiving $volume as $output_file"
	"${DOCKER[@]}" run --rm --entrypoint sh \
		-v "$volume:/from:ro" \
		-v "$output_dir:/to" \
		postgres:15-alpine \
		-c "tar -C /from -czf /to/$output_file ."
}

log "Archiving Redis and application volumes"
backup_volume yamata-redis-beta /data \
	"$BACKUP_ROOT/redis" redi_data_beta.tar.gz
backup_volume yamata-sentry-redis-beta /data \
	"$BACKUP_ROOT/redis" sentry_redis_data_beta.tar.gz
backup_volume yamata-app-beta /data \
	"$BACKUP_ROOT/volumes" uploads_beta.tar.gz
backup_volume yamata-app-beta /var/log/yamata \
	"$BACKUP_ROOT/volumes" app_logs_beta.tar.gz
backup_volume yamata-nginx-beta /var/log/nginx \
	"$BACKUP_ROOT/volumes" nginx_logs_beta.tar.gz
backup_volume frontend-beta /var/log/nginx \
	"$BACKUP_ROOT/volumes" frontend_logs_beta.tar.gz
backup_volume yamata-grafana-beta /var/lib/grafana \
	"$BACKUP_ROOT/volumes" grafana_data_beta.tar.gz
backup_volume yamata-prometheus-beta /prometheus \
	"$BACKUP_ROOT/volumes" prometheus_data_beta.tar.gz
backup_volume yamata-postgres-backup-beta /backups \
	"$BACKUP_ROOT/volumes" postgres_backups_beta.tar.gz
if "${DOCKER[@]}" container inspect yamata-pgadmin-beta >/dev/null 2>&1; then
	backup_volume yamata-pgadmin-beta /var/lib/pgadmin \
		"$BACKUP_ROOT/volumes" pgadmin_data_beta.tar.gz
fi
if "${DOCKER[@]}" container inspect yamata-pgadmin-nginx-beta >/dev/null 2>&1; then
	backup_volume yamata-pgadmin-nginx-beta /var/log/nginx \
		"$BACKUP_ROOT/volumes" pgadmin_nginx_logs_beta.tar.gz
fi
if "${DOCKER[@]}" container inspect yamata-campaign-scheduler-beta >/dev/null 2>&1; then
	backup_volume yamata-campaign-scheduler-beta /var/log/yamata \
		"$BACKUP_ROOT/volumes" campaign_scheduler_logs_beta.tar.gz
fi

log "Archiving certificates, host configuration, and project"
if [[ $EUID -eq 0 ]]; then
	tar -C / -czf "$BACKUP_ROOT/certs/letsencrypt.tar.gz" etc/letsencrypt
else
	sudo tar -C / -czf - etc/letsencrypt > \
		"$BACKUP_ROOT/certs/letsencrypt.tar.gz"
fi

# .env.beta itself is copied below and holds scalar secrets. Some Compose
# secrets/certificates are file-backed and may live outside the repository, so
# archive every configured *_FILE regular file plus the known TLS client files.
archive_runtime_files() {
	local variable value resolved_path
	local -a variables paths

	mapfile -t variables < <(compgen -v | awk '/_FILE$/ { print }')
	variables+=(TLS_CERT_FILE TLS_KEY_FILE EXTERNAL_SHORTLINK_CLIENT_CERT_FILE EXTERNAL_SHORTLINK_CLIENT_KEY_FILE EXTERNAL_SHORTLINK_CA_FILE)
	: > "$BACKUP_ROOT/manifests/runtime-file-secrets.tsv"
	for variable in "${variables[@]}"; do
		value="${!variable:-}"
		[[ -n "$value" && -f "$value" ]] || continue
		resolved_path="$(readlink -f "$value")"
		paths+=("$resolved_path")
		printf '%s\t%s\n' "$variable" "$resolved_path" >> "$BACKUP_ROOT/manifests/runtime-file-secrets.tsv"
	done
	[[ ${#paths[@]} -gt 0 ]] || return 0
	log "Archiving configured file-backed secrets and certificates"
	tar --absolute-names -czf "$BACKUP_ROOT/secrets/runtime-files.tar.gz" "${paths[@]}"
}

archive_runtime_files

mkdir -p "$BACKUP_ROOT/host"
if [[ $EUID -eq 0 ]]; then
	install -m 644 /etc/hosts "$BACKUP_ROOT/host/etc-hosts"
	[[ ! -f /etc/docker/daemon.json ]] || install -m 600 /etc/docker/daemon.json "$BACKUP_ROOT/host/docker-daemon.json"
else
	sudo install -m 644 /etc/hosts "$BACKUP_ROOT/host/etc-hosts"
	if sudo test -f /etc/docker/daemon.json; then
		sudo sh -c 'cat /etc/docker/daemon.json' > "$BACKUP_ROOT/host/docker-daemon.json"
		chmod 600 "$BACKUP_ROOT/host/docker-daemon.json"
	fi
fi

PROJECT_PARENT="$(dirname "$PROJECT_DIR")"
PROJECT_BASENAME="$(basename "$PROJECT_DIR")"
tar -C "$PROJECT_PARENT" \
	--exclude="$PROJECT_BASENAME/.gocache" \
	--exclude="$PROJECT_BASENAME/.git" \
	-czf "$BACKUP_ROOT/repo/yamata-project.tar.gz" \
	"$PROJECT_BASENAME"

log "Saving every image referenced by the Compose configuration"
mapfile -t IMAGES < <("${COMPOSE[@]}" config --images | sort -u)
if "${DOCKER[@]}" container inspect yamata-campaign-scheduler-beta >/dev/null 2>&1; then
	IMAGES+=("$("${DOCKER[@]}" inspect --format '{{.Config.Image}}' yamata-campaign-scheduler-beta)")
fi

for image_name in "${IMAGES[@]}"; do
	"${DOCKER[@]}" image inspect "$image_name" >/dev/null 2>&1 || \
		die "Required image is missing: $image_name"
	safe_name="$(printf '%s' "$image_name" | tr '/:' '__')"
	log "Saving image $image_name"
	"${DOCKER[@]}" save "$image_name" | gzip > \
		"$BACKUP_ROOT/images/$safe_name.tar.gz"
done

archive_has_file() {
	local archive="$1"
	local expected_name="$2"
	tar -tzf "$archive" | awk -F/ -v expected_name="$expected_name" '
		$NF == expected_name { found = 1 }
		END { exit(found ? 0 : 1) }
	'
}

log "Validating nested archives"
for nested_archive in "$BACKUP_ROOT"/{certs,redis,repo,volumes}/*.tar.gz; do
	tar -tzf "$nested_archive" >/dev/null || die "Invalid archive: $nested_archive"
done
archive_has_file "$BACKUP_ROOT/redis/redi_data_beta.tar.gz" \
	dump.rdb || die "Main Redis backup lacks dump.rdb"
archive_has_file "$BACKUP_ROOT/redis/sentry_redis_data_beta.tar.gz" \
	dump.rdb || die "Sentry Redis backup lacks dump.rdb"
archive_has_file "$BACKUP_ROOT/repo/yamata-project.tar.gz" \
	docker-compose.beta.yml || die "Project archive lacks docker-compose.beta.yml"

log "Writing payload checksums"
(
	cd "$BACKUP_ROOT"
	find . -type f ! -name SHA256SUMS -print0 | sort -z | \
		xargs -0 sha256sum > SHA256SUMS
	sha256sum -c SHA256SUMS >/dev/null
)

log "Creating final transport archive"
tar -C "$OUTPUT_PARENT" -czf "$FINAL_ARCHIVE.partial" "$BACKUP_NAME"
mv "$FINAL_ARCHIVE.partial" "$FINAL_ARCHIVE"
(
	cd "$OUTPUT_PARENT"
	sha256sum "$(basename "$FINAL_ARCHIVE")" > "$(basename "$FINAL_ARCHIVE").sha256"
)

BACKUP_SUCCEEDED=true

log "Backup completed"
printf '%s\n' "$BACKUP_ROOT" "$FINAL_ARCHIVE" "$FINAL_ARCHIVE.sha256"
