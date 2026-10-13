#!/usr/bin/env bash
# Deploy and operate the standalone external short-link service on Debian 12.
#
# The filename is retained for callers that already use it. This script only
# supports the dedicated Debian host and checkout specified below.
set -Eeuo pipefail
IFS=$'\n\t'
umask 027

readonly DOMAIN='jzbe.ir'
readonly DEPLOYMENT_USER='debian'
readonly DEPLOYMENT_HOME='/home/debian'
readonly PROJECT_DIR='/home/debian/Yamata-no-Orochi'
readonly DEFAULT_SOURCE_DIR="$PROJECT_DIR/external-shortlink"
readonly INSTALL_DIR='/opt/external-shortlink'
readonly SERVICE_NAME='external-shortlink'
readonly PARTITION_TIMER_NAME='external-shortlink-partitions.timer'
readonly DISK_GUARD_TIMER_NAME='external-shortlink-disk-guard.timer'
readonly SERVICE_USER='external-shortlink'
readonly SERVICE_GROUP='external-shortlink'
readonly SERVICE_ENV='/etc/external-shortlink.env'
readonly POSTGRES_ENV='/etc/external-shortlink-postgres.env'
readonly COMPOSE_FILE="$INSTALL_DIR/app/deploy/postgres.compose.yml"
readonly NGINX_SITE='/etc/nginx/sites-available/external-shortlink'
readonly NGINX_ENABLED='/etc/nginx/sites-enabled/external-shortlink'
readonly NGINX_BOOTSTRAP_SITE='/etc/nginx/sites-available/external-shortlink-bootstrap'
readonly NGINX_BOOTSTRAP_ENABLED='/etc/nginx/sites-enabled/external-shortlink-bootstrap'
readonly ACME_WEBROOT='/var/www/external-shortlink-acme'
readonly CERTBOT_RENEWAL_HOOK='/etc/letsencrypt/renewal-hooks/deploy/external-shortlink-reload-nginx'
readonly DOCKER_APT_KEYRING='/etc/apt/keyrings/external-shortlink-docker.asc'
readonly DOCKER_APT_SOURCE='/etc/apt/sources.list.d/external-shortlink-docker.sources'
readonly LOG_DIR='/var/log/external-shortlink'
readonly DEPLOY_LOG="$LOG_DIR/deploy.log"
readonly RUST_TOOLCHAIN='1.85.0'
readonly MINIMUM_MEMORY_KIB=3800000
readonly MINIMUM_ROOT_FREE_KIB=$((20 * 1024 * 1024))

ACTION='deploy'
SOURCE_DIR="$DEFAULT_SOURCE_DIR"
API_TOKEN_FILE=''
ACME_EMAIL=''
CONFIGURE_UFW=false
LOG_FOLLOW=false
LOG_LINES=200
LOG_SINCE=''
RUSTUP_INIT=''
API_TOKEN=''
POSTGRES_ADMIN_PASSWORD=''
POSTGRES_RUNTIME_PASSWORD=''

log() {
    printf '%s external-shortlink[%s]: %s\n' \
        "$(date --utc '+%Y-%m-%dT%H:%M:%SZ')" "$ACTION" "$*"
}

die() {
    log "ERROR: $*"
    exit 1
}

cleanup() {
    if [[ -n "$RUSTUP_INIT" && -e "$RUSTUP_INIT" ]]; then
        rm -f -- "$RUSTUP_INIT"
    fi
}

compose() {
    docker compose -f "$COMPOSE_FILE" "$@"
}

service_diagnostics() {
    set +e
    if command -v systemctl >/dev/null 2>&1 && systemctl cat "$SERVICE_NAME" >/dev/null 2>&1; then
        log "recent $SERVICE_NAME status follows"
        systemctl status "$SERVICE_NAME" --no-pager --full || true
        journalctl -u "$SERVICE_NAME" -n 100 --no-pager || true
    fi
    if command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]]; then
        log 'recent PostgreSQL container status follows'
        compose ps || true
        compose logs --tail 100 postgres || true
    fi
}

on_error() {
    local line="$1"
    local status="$2"
    trap - ERR
    set +e
    log "ERROR: action failed (exit_code=$status line=$line)"
    service_diagnostics
    log "ERROR: deployment log retained at $DEPLOY_LOG"
    exit "$status"
}

setup_logging() {
    install -d -o root -g root -m 0700 "$LOG_DIR"
    touch "$DEPLOY_LOG"
    chown root:root "$DEPLOY_LOG"
    chmod 0600 "$DEPLOY_LOG"
    exec > >(tee -a "$DEPLOY_LOG") 2>&1
}

usage() {
    cat <<'USAGE'
Usage:
  sudo ./scripts/deploy-debian-12.sh deploy \
    --api-token-file <root-only-token-file> \
    [--acme-email <email>] \
    [--configure-ufw] [--source-dir <directory>]
  sudo ./scripts/deploy-debian-12.sh start
  sudo ./scripts/deploy-debian-12.sh stop
  sudo ./scripts/deploy-debian-12.sh restart
  sudo ./scripts/deploy-debian-12.sh status
  sudo ./scripts/deploy-debian-12.sh logs [--lines <count>] [--since <time>] [--follow]

The default action is deploy. This script supports only Debian 12, the
deployment account "debian", and a source checkout below
/home/debian/Yamata-no-Orochi/. Release files are installed root-owned under
/opt/external-shortlink and the service itself runs as external-shortlink.

Deploy requirements:
  --api-token-file  One-line root-only file containing the shared 32+ character URL-safe API token.

Deploy options:
  --acme-email      Required only when /etc/letsencrypt/live/jzbe.ir is absent.
  --configure-ufw   Allow the current SSH port plus HTTP/HTTPS and enable UFW.
  --source-dir      Source directory under the required project checkout.

Logs:
  logs              Show the latest 200 module journal entries, including maintenance guards.
  --follow          Continue streaming module logs after the history.
  --lines <count>   Number of historical entries to show (default: 200).
  --since <time>    journalctl-compatible lower time bound, such as "2 hours ago".

The deploy action never prints secrets, refuses to silently rotate an existing
API token, and writes its own failure diagnostics to
/var/log/external-shortlink/deploy.log.
USAGE
}

require_argument() {
    local option="$1"
    local value="${2:-}"
    [[ -n "$value" ]] || die "$option requires a value"
}

if (($# > 0)); then
    case "$1" in
        deploy|start|stop|restart|status|logs)
            ACTION="$1"
            shift
            ;;
    esac
fi

while (($# > 0)); do
    case "$1" in
        --api-token-file)
            require_argument "$1" "${2:-}"
            API_TOKEN_FILE="$2"
            shift 2
            ;;
        --acme-email)
            require_argument "$1" "${2:-}"
            ACME_EMAIL="$2"
            shift 2
            ;;
        --source-dir)
            require_argument "$1" "${2:-}"
            SOURCE_DIR="$2"
            shift 2
            ;;
        --configure-ufw)
            CONFIGURE_UFW=true
            shift
            ;;
        --follow|-f)
            LOG_FOLLOW=true
            shift
            ;;
        --lines|-n)
            require_argument "$1" "${2:-}"
            LOG_LINES="$2"
            shift 2
            ;;
        --since)
            require_argument "$1" "${2:-}"
            LOG_SINCE="$2"
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            die "unknown option or action: $1"
            ;;
    esac
done

resolve_source_dir() {
    [[ -d "$SOURCE_DIR" ]] || die "source directory does not exist: $SOURCE_DIR"
    SOURCE_DIR="$(cd -- "$SOURCE_DIR" && pwd -P)"
    case "$SOURCE_DIR" in
        "$PROJECT_DIR"/*) ;;
        *) die "source directory must be below $PROJECT_DIR" ;;
    esac
}

require_root() {
    [[ "${EUID}" -eq 0 ]] || die 'run this script with sudo or as root'
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "required command is missing: $1"
}

verify_debian_host() {
    require_command systemctl
    require_command getconf
    require_command df
    require_command awk
    require_command getent
    require_command git
    require_command grep
    require_command stat
    [[ -d /run/systemd/system ]] || die 'systemd is not the active init system'
    [[ -r /etc/os-release ]] || die 'cannot identify the operating system'

    # /etc/os-release is system-owned and is the authoritative source.
    # shellcheck disable=SC1091
    . /etc/os-release
    [[ "${ID:-}" == 'debian' && "${VERSION_ID:-}" == '12' ]] || die 'this deployment requires Debian 12'

    case "$(uname -m)" in
        x86_64|aarch64) ;;
        *) die "unsupported CPU architecture: $(uname -m)" ;;
    esac
}

verify_deployment_account_and_capacity() {
    local account_home project_owner source_owner cores memory_kib root_free_kib
    require_command runuser
    id -u "$DEPLOYMENT_USER" >/dev/null 2>&1 || die "required deployment user is missing: $DEPLOYMENT_USER"
    account_home="$(getent passwd "$DEPLOYMENT_USER" | awk -F: '{ print $6 }')"
    [[ "$account_home" == "$DEPLOYMENT_HOME" ]] || die "deployment user $DEPLOYMENT_USER must have home $DEPLOYMENT_HOME"
    project_owner="$(stat -c '%U' "$PROJECT_DIR")"
    [[ "$project_owner" == "$DEPLOYMENT_USER" ]] || die "$PROJECT_DIR must be owned by $DEPLOYMENT_USER"
    source_owner="$(stat -c '%U' "$SOURCE_DIR")"
    [[ "$source_owner" == "$DEPLOYMENT_USER" ]] || die "$SOURCE_DIR must be owned by $DEPLOYMENT_USER"
    runuser -u "$DEPLOYMENT_USER" -- test -r "$SOURCE_DIR/Cargo.toml" ||
        die "$DEPLOYMENT_USER cannot read the source tree"

    cores="$(getconf _NPROCESSORS_ONLN)"
    memory_kib="$(awk '/MemTotal:/ { print $2; exit }' /proc/meminfo)"
    root_free_kib="$(df -Pk / | awk 'NR == 2 { print $4 }')"
    [[ "$cores" =~ ^[0-9]+$ && "$cores" -ge 4 ]] || die 'at least 4 vCPUs are required'
    [[ "$memory_kib" =~ ^[0-9]+$ && "$memory_kib" -ge "$MINIMUM_MEMORY_KIB" ]] || die 'at least 4 GB RAM is required'
    [[ "$root_free_kib" =~ ^[0-9]+$ && "$root_free_kib" -ge "$MINIMUM_ROOT_FREE_KIB" ]] || die 'at least 20 GiB free space is required on /'
    log "host verified: debian=12 user=$DEPLOYMENT_USER vcpus=$cores memory_kib=$memory_kib root_free_kib=$root_free_kib"
}

verify_source_tree() {
    local required_files=(
        Cargo.toml
        Cargo.lock
        schema.sql
        maintain-click-partitions.sql
        deploy/external-shortlink.service
        deploy/external-shortlink.env.example
        deploy/postgres.compose.yml
        deploy/postgres-init/20-runtime-role.sh
        deploy/nginx.conf
        deploy/nginx-bootstrap.conf
        deploy/external-shortlink-partitions.service
        deploy/external-shortlink-partitions.timer
        deploy/external-shortlink-disk-guard.service
        deploy/external-shortlink-disk-guard.timer
        scripts/maintain-click-partitions.sh
        scripts/check-disk-headroom.sh
    )
    local file
    for file in "${required_files[@]}"; do
        [[ -f "$SOURCE_DIR/$file" ]] || die "source tree is missing $file"
    done
    log "source tree verified: $SOURCE_DIR"
}

verify_source_integrity() {
    local git_root relative_source cargo_path
    git_root="$(as_deployment_user git -C "$SOURCE_DIR" rev-parse --show-toplevel 2>/dev/null)" ||
        die "source directory is not a valid Git work tree for $DEPLOYMENT_USER: $SOURCE_DIR"
    git_root="$(cd -- "$git_root" && pwd -P)"
    case "$SOURCE_DIR" in
        "$git_root")
            relative_source='.'
            cargo_path='Cargo.toml'
            ;;
        "$git_root"/*)
            relative_source="${SOURCE_DIR#"$git_root"/}"
            cargo_path="$relative_source/Cargo.toml"
            ;;
        *)
            die 'source directory is outside its Git work tree'
            ;;
    esac

    as_deployment_user git -C "$git_root" ls-files --error-unmatch -- "$cargo_path" >/dev/null ||
        die 'source Cargo.toml is not tracked by Git'
    if ! as_deployment_user git -C "$git_root" diff --quiet -- "$relative_source" ||
        ! as_deployment_user git -C "$git_root" diff --cached --quiet -- "$relative_source" ||
        [[ -n "$(as_deployment_user git -C "$git_root" status --porcelain=v1 --untracked-files=all -- "$relative_source")" ]]; then
        die 'source directory has tracked, staged, or untracked changes; deploy a clean reviewed commit'
    fi
    log "source integrity verified: $(as_deployment_user git -C "$git_root" rev-parse --verify HEAD)"
}

verify_api_token() {
    [[ -n "$API_TOKEN_FILE" ]] || die '--api-token-file is required for deploy'
    [[ -f "$API_TOKEN_FILE" ]] || die "API token file does not exist: $API_TOKEN_FILE"
    [[ ! -L "$API_TOKEN_FILE" ]] || die 'API token file must not be a symlink'

    local mode
    mode="$(stat -c '%a' "$API_TOKEN_FILE")"
    [[ "$mode" =~ ^[0-7]{3,4}$ ]] || die 'cannot read API token file permissions'
    (( (8#$mode & 077) == 0 )) || die 'API token file must not be group- or world-readable'
    [[ "$(stat -c '%U:%G' "$API_TOKEN_FILE")" == 'root:root' ]] || die 'API token file must be owned by root:root'

    local -a token_lines
    mapfile -t token_lines < "$API_TOKEN_FILE"
    [[ "${#token_lines[@]}" -eq 1 ]] || die 'API token file must contain exactly one line'
    API_TOKEN="${token_lines[0]%$'\r'}"
    valid_url_safe_secret "$API_TOKEN" || die 'API token must be a 32+ character URL-safe secret'
    log 'shared API token file verified'
}

verify_tls_input() {
    local certificate="/etc/letsencrypt/live/$DOMAIN/fullchain.pem"
    local key="/etc/letsencrypt/live/$DOMAIN/privkey.pem"
    if [[ ! -f "$certificate" || ! -f "$key" ]]; then
        [[ -n "$ACME_EMAIL" ]] || die "--acme-email is required to issue the first certificate for $DOMAIN"
        [[ "$ACME_EMAIL" =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || die '--acme-email must be a valid email address'
    fi
}

read_env_value() {
    local file="$1"
    local key="$2"
    awk -v prefix="$key=" '
        index($0, prefix) == 1 {
            value = substr($0, length(prefix) + 1)
            sub(/\r$/, "", value)
            print value
            exit
        }
    ' "$file"
}

validate_env_file() {
    local file="$1"
    shift
    [[ -f "$file" && ! -L "$file" ]] || die "environment file is missing or a symlink: $file"
    awk '
        /^[[:space:]]*($|#|;)/ { next }
        !/^[A-Za-z_][A-Za-z0-9_]*=/ {
            printf "invalid environment file line %d\n", NR > "/dev/stderr"
            exit 1
        }
        {
            key = substr($0, 1, index($0, "=") - 1)
            if (seen[key]++) {
                printf "duplicate environment key %s\n", key > "/dev/stderr"
                exit 1
            }
        }
    ' "$file" || die "environment file has invalid syntax: $file"

    local key value
    for key in "$@"; do
        value="$(read_env_value "$file" "$key")"
        [[ -n "$value" ]] || die "required environment variable $key is missing or empty in $file"
    done
}

valid_url_safe_secret() {
    [[ "$1" =~ ^[A-Za-z0-9._~-]{32,}$ ]]
}

verify_file_owner_and_mode() {
    local file="$1"
    local expected_owner="$2"
    local expected_mode="$3"
    local description="$4"
    [[ "$(stat -c '%U:%G' "$file")" == "$expected_owner" ]] ||
        die "$description must be owned by $expected_owner"
    [[ "$(stat -c '%a' "$file")" == "$expected_mode" ]] ||
        die "$description must have mode $expected_mode"
}

validate_deployed_environment() {
    validate_env_file "$POSTGRES_ENV" \
        POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD EXTERNAL_SHORTLINK_RUNTIME_PASSWORD
    verify_file_owner_and_mode "$POSTGRES_ENV" 'root:root' '600' 'PostgreSQL environment file'
    [[ "$(read_env_value "$POSTGRES_ENV" 'POSTGRES_DB')" == 'external_shortlink' ]] || die 'PostgreSQL database name is unsupported'
    [[ "$(read_env_value "$POSTGRES_ENV" 'POSTGRES_USER')" == 'external_shortlink_admin' ]] || die 'PostgreSQL administrator role is unsupported'
    valid_url_safe_secret "$(read_env_value "$POSTGRES_ENV" 'POSTGRES_PASSWORD')" || die 'PostgreSQL administrator password must be a 32+ character URL-safe secret'
    valid_url_safe_secret "$(read_env_value "$POSTGRES_ENV" 'EXTERNAL_SHORTLINK_RUNTIME_PASSWORD')" || die 'PostgreSQL runtime password must be a 32+ character URL-safe secret'

    validate_env_file "$SERVICE_ENV" EXTERNAL_SHORTLINK_DATABASE_URL EXTERNAL_SHORTLINK_API_TOKEN
    verify_file_owner_and_mode "$SERVICE_ENV" "root:$SERVICE_GROUP" '640' 'service environment file'
    valid_url_safe_secret "$(read_env_value "$SERVICE_ENV" 'EXTERNAL_SHORTLINK_API_TOKEN')" || die 'deployed API token is invalid'
    local expected_database_url
    expected_database_url="postgresql://external_shortlink_runtime:$(read_env_value "$POSTGRES_ENV" 'EXTERNAL_SHORTLINK_RUNTIME_PASSWORD')@127.0.0.1:5433/external_shortlink"
    [[ "$(read_env_value "$SERVICE_ENV" 'EXTERNAL_SHORTLINK_DATABASE_URL')" == "$expected_database_url" ]] || die 'service database URL does not match the private PostgreSQL configuration'
    # One retention setting governs the whole click store: the service purges
    # legacy rows with it, and the maintenance timer uses the same value for day
    # partitions and deduplication rows.
    local retention_days
    retention_days="$(read_env_value "$SERVICE_ENV" 'EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS')"
    [[ "$retention_days" =~ ^[1-9][0-9]*$ ]] ||
        die 'EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS must be a positive integer: it sets the retention of the click store'
    log 'required service and PostgreSQL environment variables verified'
}

verify_existing_deployment_inputs() {
    if [[ -f "$SERVICE_ENV" ]]; then
        validate_env_file "$SERVICE_ENV" EXTERNAL_SHORTLINK_DATABASE_URL EXTERNAL_SHORTLINK_API_TOKEN
        local existing_token
        existing_token="$(read_env_value "$SERVICE_ENV" 'EXTERNAL_SHORTLINK_API_TOKEN')"
        if [[ -n "$existing_token" && "$existing_token" != "$API_TOKEN" ]]; then
            die 'API token differs from the deployed token; rotate production and this host explicitly before retrying'
        fi
    fi

    if [[ -f "$POSTGRES_ENV" ]]; then
        validate_env_file "$POSTGRES_ENV" \
            POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD EXTERNAL_SHORTLINK_RUNTIME_PASSWORD
        [[ "$(read_env_value "$POSTGRES_ENV" 'POSTGRES_DB')" == 'external_shortlink' ]] || die 'existing PostgreSQL database name is unsupported'
        [[ "$(read_env_value "$POSTGRES_ENV" 'POSTGRES_USER')" == 'external_shortlink_admin' ]] || die 'existing PostgreSQL administrator role is unsupported'
        valid_url_safe_secret "$(read_env_value "$POSTGRES_ENV" 'POSTGRES_PASSWORD')" || die 'existing PostgreSQL administrator password is not a supported 32+ character URL-safe secret'
        valid_url_safe_secret "$(read_env_value "$POSTGRES_ENV" 'EXTERNAL_SHORTLINK_RUNTIME_PASSWORD')" || die 'existing PostgreSQL runtime password is not a supported 32+ character URL-safe secret'
    fi
    log 'existing deployment inputs verified'
}

verify_no_conflicting_docker_packages() {
    require_command dpkg-query

    local package installed_packages=''
    local packages=(
        docker.io docker-compose docker-doc docker-buildx podman-docker containerd runc
    )
    for package in "${packages[@]}"; do
        if dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null | grep -Fxq 'installed'; then
            installed_packages+="$package, "
        fi
    done
    [[ -z "$installed_packages" ]] ||
        die "Docker's official packages conflict with installed packages: ${installed_packages%, }. Remove them before deploying."
}

configure_docker_apt_repository() {
    local temporary architecture
    require_command dpkg
    architecture="$(dpkg --print-architecture)"
    case "$architecture" in
        amd64|arm64) ;;
        *) die "Docker's official Debian repository does not support architecture: $architecture" ;;
    esac

    install -d -o root -g root -m 0755 /etc/apt/keyrings /etc/apt/sources.list.d

    temporary="$(mktemp /etc/apt/keyrings/.external-shortlink-docker.XXXXXX)"
    curl --proto '=https' --tlsv1.2 --fail --silent --show-error \
        https://download.docker.com/linux/debian/gpg -o "$temporary"
    install -o root -g root -m 0644 "$temporary" "$DOCKER_APT_KEYRING"
    rm -f -- "$temporary"

    temporary="$(mktemp /etc/apt/sources.list.d/.external-shortlink-docker.XXXXXX)"
    {
        printf '%s\n' \
            'Types: deb' \
            'URIs: https://download.docker.com/linux/debian' \
            'Suites: bookworm' \
            'Components: stable' \
            "Architectures: $architecture" \
            "Signed-By: $DOCKER_APT_KEYRING"
    } > "$temporary"
    install -o root -g root -m 0644 "$temporary" "$DOCKER_APT_SOURCE"
    rm -f -- "$temporary"

    apt-get update
    log "Docker's official APT repository configured for Debian 12 ($architecture)"
}

install_dependencies() {
    log 'checking and installing Debian 12 runtime and build dependencies'
    require_command apt-get
    require_command apt-cache
    export DEBIAN_FRONTEND=noninteractive
    apt-get update

    local base_packages=(
        ca-certificates curl git python3 build-essential pkg-config libssl-dev
        nginx certbot ufw
    )
    local package
    for package in "${base_packages[@]}"; do
        apt-cache show "$package" >/dev/null 2>&1 || die "required Debian package is unavailable: $package"
    done
    apt-get install -y --no-install-recommends "${base_packages[@]}"

    verify_no_conflicting_docker_packages
    configure_docker_apt_repository

    local docker_packages=(
        docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    )
    for package in "${docker_packages[@]}"; do
        apt-cache show "$package" >/dev/null 2>&1 || die "required Docker package is unavailable: $package"
    done
    apt-get install -y --no-install-recommends "${docker_packages[@]}"
    systemctl enable --now docker

    require_command docker
    docker compose version >/dev/null
    require_command nginx
    require_command certbot
    require_command openssl
    require_command curl
    log 'runtime and build dependencies verified'
}

as_deployment_user() {
    runuser -u "$DEPLOYMENT_USER" -- env \
        "HOME=$DEPLOYMENT_HOME" \
        "CARGO_HOME=$DEPLOYMENT_HOME/.cargo" \
        "RUSTUP_HOME=$DEPLOYMENT_HOME/.rustup" \
        "PATH=$DEPLOYMENT_HOME/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
        "$@"
}

install_rust_toolchain() {
    if ! as_deployment_user rustup --version >/dev/null 2>&1; then
        RUSTUP_INIT="$(mktemp /tmp/external-shortlink-rustup-init.XXXXXX)"
        log "installing Rust toolchain $RUST_TOOLCHAIN for $DEPLOYMENT_USER"
        curl --proto '=https' --tlsv1.2 --fail --silent --show-error https://sh.rustup.rs -o "$RUSTUP_INIT"
        chown "$DEPLOYMENT_USER":"$DEPLOYMENT_USER" "$RUSTUP_INIT"
        chmod 0700 "$RUSTUP_INIT"
        as_deployment_user sh "$RUSTUP_INIT" -y --profile minimal --default-toolchain "$RUST_TOOLCHAIN"
    fi
    as_deployment_user rustup toolchain install "$RUST_TOOLCHAIN" --profile minimal
    as_deployment_user cargo "+$RUST_TOOLCHAIN" --version >/dev/null
    log "Rust toolchain $RUST_TOOLCHAIN verified for $DEPLOYMENT_USER"
}

prepare_secrets() {
    if [[ -f "$POSTGRES_ENV" ]]; then
        POSTGRES_ADMIN_PASSWORD="$(read_env_value "$POSTGRES_ENV" 'POSTGRES_PASSWORD')"
        POSTGRES_RUNTIME_PASSWORD="$(read_env_value "$POSTGRES_ENV" 'EXTERNAL_SHORTLINK_RUNTIME_PASSWORD')"
    else
        POSTGRES_ADMIN_PASSWORD="$(openssl rand -hex 32)"
        POSTGRES_RUNTIME_PASSWORD="$(openssl rand -hex 32)"
    fi
    valid_url_safe_secret "$POSTGRES_ADMIN_PASSWORD" || die 'could not prepare a valid PostgreSQL administrator password'
    valid_url_safe_secret "$POSTGRES_RUNTIME_PASSWORD" || die 'could not prepare a valid PostgreSQL runtime password'
}

write_postgres_env() {
    local temporary
    temporary="$(mktemp /etc/.external-shortlink-postgres.env.XXXXXX)"
    {
        printf '%s\n' \
            'POSTGRES_DB=external_shortlink' \
            'POSTGRES_USER=external_shortlink_admin' \
            "POSTGRES_PASSWORD=$POSTGRES_ADMIN_PASSWORD" \
            "EXTERNAL_SHORTLINK_RUNTIME_PASSWORD=$POSTGRES_RUNTIME_PASSWORD"
    } > "$temporary"
    install -o root -g root -m 0600 "$temporary" "$POSTGRES_ENV"
    rm -f -- "$temporary"
}

write_service_env() {
    local source_env="$SOURCE_DIR/deploy/external-shortlink.env.example"
    local temporary
    if [[ -f "$SERVICE_ENV" ]]; then
        source_env="$SERVICE_ENV"
    fi
    temporary="$(mktemp /etc/.external-shortlink.env.XXXXXX)"
    {
        printf '%s\n' \
            "EXTERNAL_SHORTLINK_DATABASE_URL=postgresql://external_shortlink_runtime:$POSTGRES_RUNTIME_PASSWORD@127.0.0.1:5433/external_shortlink" \
            "EXTERNAL_SHORTLINK_API_TOKEN=$API_TOKEN" \
            'EXTERNAL_SHORTLINK_DB_COMMAND_TIMEOUT_SECONDS=15' \
            'EXTERNAL_SHORTLINK_DB_LOCK_TIMEOUT_SECONDS=10' \
            'EXTERNAL_SHORTLINK_CLICK_INSERT_TIMEOUT_SECONDS=2'
        awk '!/^(EXTERNAL_SHORTLINK_DATABASE_URL|EXTERNAL_SHORTLINK_API_TOKEN|EXTERNAL_SHORTLINK_DB_COMMAND_TIMEOUT_SECONDS|EXTERNAL_SHORTLINK_DB_LOCK_TIMEOUT_SECONDS|EXTERNAL_SHORTLINK_CLICK_INSERT_TIMEOUT_SECONDS|EXTERNAL_SHORTLINK_SPOOL_PATH|EXTERNAL_SHORTLINK_SPOOL_MAX_BYTES|EXTERNAL_SHORTLINK_SPOOL_MAX_EVENTS|EXTERNAL_SHORTLINK_SPOOL_REPLAY_BATCH_SIZE|EXTERNAL_SHORTLINK_SPOOL_REPLAY_INTERVAL_SECONDS|EXTERNAL_SHORTLINK_SPOOL_OPERATION_TIMEOUT_SECONDS)=/' "$source_env"
    } > "$temporary"
    install -o root -g "$SERVICE_GROUP" -m 0640 "$temporary" "$SERVICE_ENV"
    rm -f -- "$temporary"
}

install_release_files() {
    if ! getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
        groupadd --system "$SERVICE_GROUP"
    fi
    if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
        useradd --system --gid "$SERVICE_GROUP" --home /var/lib/external-shortlink --shell /usr/sbin/nologin "$SERVICE_USER"
    fi

    install -d -o root -g root -m 0755 "$INSTALL_DIR/bin" "$INSTALL_DIR/app/deploy/postgres-init" "$INSTALL_DIR/app/scripts"
    install -m 0755 "$SOURCE_DIR/target/release/external-shortlink" "$INSTALL_DIR/bin/external-shortlink"
    install -m 0644 "$SOURCE_DIR/schema.sql" "$INSTALL_DIR/app/schema.sql"
    install -m 0644 "$SOURCE_DIR/maintain-click-partitions.sql" "$INSTALL_DIR/app/maintain-click-partitions.sql"
    install -m 0644 "$SOURCE_DIR/deploy/external-shortlink.service" "$INSTALL_DIR/app/deploy/external-shortlink.service"
    install -m 0644 "$SOURCE_DIR/deploy/external-shortlink.env.example" "$INSTALL_DIR/app/deploy/external-shortlink.env.example"
    install -m 0644 "$SOURCE_DIR/deploy/postgres.compose.yml" "$INSTALL_DIR/app/deploy/postgres.compose.yml"
    install -m 0755 "$SOURCE_DIR/deploy/postgres-init/20-runtime-role.sh" "$INSTALL_DIR/app/deploy/postgres-init/20-runtime-role.sh"
    install -m 0644 "$SOURCE_DIR/deploy/nginx.conf" "$INSTALL_DIR/app/deploy/nginx.conf"
    install -m 0644 "$SOURCE_DIR/deploy/nginx-bootstrap.conf" "$INSTALL_DIR/app/deploy/nginx-bootstrap.conf"
    install -m 0755 "$SOURCE_DIR/scripts/maintain-click-partitions.sh" "$INSTALL_DIR/app/scripts/maintain-click-partitions.sh"
    install -m 0755 "$SOURCE_DIR/scripts/check-disk-headroom.sh" "$INSTALL_DIR/app/scripts/check-disk-headroom.sh"
    install -d -o "$SERVICE_USER" -g "$SERVICE_GROUP" -m 0750 /var/lib/external-shortlink
    install -d -o root -g root -m 0755 "$ACME_WEBROOT"

    write_postgres_env
    write_service_env
    validate_deployed_environment
    install -o root -g root -m 0644 "$INSTALL_DIR/app/deploy/external-shortlink.service" "/etc/systemd/system/$SERVICE_NAME.service"
    install -o root -g root -m 0644 "$SOURCE_DIR/deploy/external-shortlink-partitions.service" "/etc/systemd/system/external-shortlink-partitions.service"
    install -o root -g root -m 0644 "$SOURCE_DIR/deploy/external-shortlink-partitions.timer" "/etc/systemd/system/$PARTITION_TIMER_NAME"
    install -o root -g root -m 0644 "$SOURCE_DIR/deploy/external-shortlink-disk-guard.service" "/etc/systemd/system/external-shortlink-disk-guard.service"
    install -o root -g root -m 0644 "$SOURCE_DIR/deploy/external-shortlink-disk-guard.timer" "/etc/systemd/system/$DISK_GUARD_TIMER_NAME"
    systemd-analyze verify "/etc/systemd/system/$SERVICE_NAME.service"
}

build_release() {
    # Do this while the installed service is still running. A build failure must
    # never turn a healthy redirect service into a deployment outage.
    log "building locked Rust release binary as $DEPLOYMENT_USER before service replacement"
    as_deployment_user cargo "+$RUST_TOOLCHAIN" build --release --locked --manifest-path "$SOURCE_DIR/Cargo.toml"
    [[ -x "$SOURCE_DIR/target/release/external-shortlink" ]] || die 'release binary was not produced'
}

ensure_installed_deployment() {
    [[ -f "/etc/systemd/system/$SERVICE_NAME.service" ]] || die "service unit is missing; run '$0 deploy' first"
    [[ -x "$INSTALL_DIR/bin/external-shortlink" ]] || die "service binary is missing; run '$0 deploy' first"
    [[ -f "$COMPOSE_FILE" ]] || die "PostgreSQL configuration is missing; run '$0 deploy' first"
    validate_deployed_environment
    require_command docker
    docker compose version >/dev/null
    require_command curl
}

wait_for_postgres() {
    local attempt
    for attempt in $(seq 1 45); do
        if compose exec -T postgres pg_isready --host 127.0.0.1 --port 5432 -U external_shortlink_admin -d external_shortlink >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    die 'PostgreSQL did not become ready within 45 seconds'
}

deploy_postgres() {
    log 'starting private PostgreSQL container'
    compose config --quiet
    compose up -d
    wait_for_postgres
    compose exec -T postgres sh -ceu '
        PGPASSWORD="$POSTGRES_PASSWORD" psql --set=ON_ERROR_STOP=1 \
            --host 127.0.0.1 --port 5432 \
            --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
            -f /opt/external-shortlink-schema.sql
    ' >/dev/null
    compose exec -T postgres /opt/external-shortlink-runtime-role.sh >/dev/null
    log 'PostgreSQL schema and least-privilege runtime role verified'
}

activate_nginx_site() {
    local site="$1"
    local enabled="$2"
    ln -sfn "$site" "$enabled"
    nginx -t
    systemctl enable --now nginx
    systemctl reload nginx
}

ensure_certificate() {
    local certificate="/etc/letsencrypt/live/$DOMAIN/fullchain.pem"
    local key="/etc/letsencrypt/live/$DOMAIN/privkey.pem"
    if [[ -f "$certificate" && -f "$key" ]]; then
        openssl x509 -checkend 0 -noout -in "$certificate" >/dev/null || die "existing certificate for $DOMAIN is expired"
        install_certbot_renewal_hook
        systemctl enable --now certbot.timer
        log "existing TLS certificate verified for $DOMAIN"
        return
    fi

    [[ -n "$ACME_EMAIL" ]] || die "--acme-email is required to issue the first certificate for $DOMAIN"
    install -m 0644 "$INSTALL_DIR/app/deploy/nginx-bootstrap.conf" "$NGINX_BOOTSTRAP_SITE"
    activate_nginx_site "$NGINX_BOOTSTRAP_SITE" "$NGINX_BOOTSTRAP_ENABLED"
    log "requesting TLS certificate for $DOMAIN"
    certbot certonly --webroot --webroot-path "$ACME_WEBROOT" --non-interactive --agree-tos --email "$ACME_EMAIL" --keep-until-expiring -d "$DOMAIN"
    install_certbot_renewal_hook
    systemctl enable --now certbot.timer
    rm -f -- "$NGINX_BOOTSTRAP_ENABLED" "$NGINX_BOOTSTRAP_SITE"
}

configure_nginx() {
    local temporary
    temporary="$(mktemp /etc/nginx/sites-available/.external-shortlink.XXXXXX)"
    install -m 0644 "$INSTALL_DIR/app/deploy/nginx.conf" "$temporary"
    install -o root -g root -m 0644 "$temporary" "$NGINX_SITE"
    rm -f -- "$temporary"
    activate_nginx_site "$NGINX_SITE" "$NGINX_ENABLED"
}

install_certbot_renewal_hook() {
    local temporary hook_directory
    hook_directory="$(dirname "$CERTBOT_RENEWAL_HOOK")"
    install -d -o root -g root -m 0755 "$hook_directory"
    temporary="$(mktemp "$hook_directory/.external-shortlink-reload-nginx.XXXXXX")"
    {
        printf '%s\n' '#!/bin/sh' 'set -eu' 'systemctl reload nginx'
    } > "$temporary"
    install -o root -g root -m 0755 "$temporary" "$CERTBOT_RENEWAL_HOOK"
    rm -f -- "$temporary"
}

configure_firewall() {
    if [[ "$CONFIGURE_UFW" != true ]]; then
        log 'UFW was not changed; confirm the cloud firewall permits TCP 80/443 and blocks 5432, 5433, and 8081'
        return
    fi
    local ssh_port='22'
    if [[ -n "${SSH_CONNECTION:-}" ]]; then
        ssh_port="$(awk '{ print $4 }' <<<"$SSH_CONNECTION")"
    fi
    [[ "$ssh_port" =~ ^[0-9]+$ ]] || die 'could not determine the current SSH port for UFW'
    ufw allow "$ssh_port/tcp"
    ufw allow 'Nginx Full'
    ufw --force enable
    log "UFW enabled with SSH port $ssh_port and Nginx Full allowed"
}

wait_for_service_readiness() {
    local attempt
    for attempt in $(seq 1 30); do
        if curl --fail --silent --show-error http://127.0.0.1:8081/readyz >/dev/null; then
            return 0
        fi
        sleep 1
    done
    die "$SERVICE_NAME did not become ready"
}

start_service_and_verify() {
    local restart="$1"
    deploy_postgres
    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME"
    systemctl enable --now "$PARTITION_TIMER_NAME"
    systemctl enable --now "$DISK_GUARD_TIMER_NAME"
    systemctl start external-shortlink-partitions.service
    if [[ "$restart" == true ]]; then
        log "compiled release and PostgreSQL checks succeeded; performing the single service restart"
        systemctl restart "$SERVICE_NAME"
    else
        systemctl start "$SERVICE_NAME"
    fi
    # Do not leave a newly deployed service accepting traffic if the dedicated
    # PostgreSQL volume is already beyond its safe operating threshold.
    systemctl start external-shortlink-disk-guard.service
    wait_for_service_readiness
}

verify_public_health() {
    curl --fail --silent --show-error --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/healthz" >/dev/null
    log "health checks succeeded for https://$DOMAIN"
}

run_deploy() {
    resolve_source_dir
    verify_debian_host
    verify_deployment_account_and_capacity
    verify_source_tree
    verify_source_integrity
    verify_api_token
    verify_tls_input
    verify_existing_deployment_inputs
    install_dependencies
    install_rust_toolchain
    prepare_secrets
    build_release
    install_release_files
    start_service_and_verify true
    ensure_certificate
    configure_nginx
    configure_firewall
    verify_public_health
    log "deployment complete: https://$DOMAIN"
    log "service log history: sudo $0 logs"
    log "live service logs: sudo $0 logs --follow"
}

run_start() {
    verify_debian_host
    ensure_installed_deployment
    start_service_and_verify false
    log "$SERVICE_NAME is running"
}

run_stop() {
    verify_debian_host
    [[ -f "/etc/systemd/system/$SERVICE_NAME.service" ]] || die "service unit is missing; run '$0 deploy' first"
    systemctl stop "$SERVICE_NAME"
    log "$SERVICE_NAME stopped; its private PostgreSQL container remains running"
}

run_restart() {
    verify_debian_host
    ensure_installed_deployment
    start_service_and_verify true
    log "$SERVICE_NAME restarted"
}

run_status() {
    verify_debian_host
    [[ -f "/etc/systemd/system/$SERVICE_NAME.service" ]] || die "service unit is missing; run '$0 deploy' first"
    systemctl status "$SERVICE_NAME" --no-pager --full || true
    systemctl status "$PARTITION_TIMER_NAME" --no-pager --full || true
    systemctl status "$DISK_GUARD_TIMER_NAME" --no-pager --full || true
    if [[ -f "$COMPOSE_FILE" ]] && command -v docker >/dev/null 2>&1; then
        compose ps || true
        # Any row here means the partition window fell behind. The default
        # partition keeps serving those clicks meanwhile, so this is a
        # maintenance alarm rather than an outage.
        local default_rows
        default_rows="$(compose exec -T postgres sh -ceu '
            PGPASSWORD="$POSTGRES_PASSWORD" psql --host 127.0.0.1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
                --tuples-only --no-align --command "SELECT count(*) FROM clicks_v2_default"
        ' 2>/dev/null || true)"
        default_rows="$(printf '%s' "$default_rows" | tr -d '[:space:]')"
        [[ "$default_rows" =~ ^[0-9]+$ ]] || default_rows='unavailable'
        log "clicks in the clicks_v2 default partition: $default_rows"
    fi
}

run_logs() {
    verify_debian_host
    [[ "$LOG_LINES" =~ ^[1-9][0-9]*$ ]] || die '--lines must be a positive integer'
    local journal_args=(
        -u "$SERVICE_NAME"
        -u external-shortlink-partitions.service
        -u external-shortlink-disk-guard.service
        -n "$LOG_LINES"
        --no-pager
    )
    if [[ -n "$LOG_SINCE" ]]; then
        journal_args+=(--since "$LOG_SINCE")
    fi
    if [[ "$LOG_FOLLOW" == true ]]; then
        journal_args+=(--follow)
    fi
    journalctl "${journal_args[@]}"
}

main() {
    require_root
    if [[ "$ACTION" != logs ]]; then
        setup_logging
    fi
    trap cleanup EXIT
    trap 'on_error "$LINENO" "$?"' ERR

    case "$ACTION" in
        deploy) run_deploy ;;
        start) run_start ;;
        stop) run_stop ;;
        restart) run_restart ;;
        status) run_status ;;
        logs) run_logs ;;
        *) die "unsupported action: $ACTION" ;;
    esac
}

main
