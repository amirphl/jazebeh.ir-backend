#!/bin/bash

# Beta Deployment Script for Yamata no Orochi
# This script automates the beta deployment process and validates pre-provisioned certificates

set -Eeuo pipefail
set +x # Deployment environment values may contain credentials.
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ENV_FILE="$PROJECT_ROOT/.env.beta"
NGINX_CONF_DIR="$PROJECT_ROOT/docker/nginx/sites-available"
NGINX_TEMPLATE="$NGINX_CONF_DIR/yamata-beta.conf"
PGADMIN_NGINX_TEMPLATE="$NGINX_CONF_DIR/pgadmin-beta.conf"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
	echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
	echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
	echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
	echo -e "${RED}[ERROR]${NC} $1" >&2
}

# Function to check if command exists
command_exists() {
	command -v "$1" >/dev/null 2>&1
}

# Helper to resolve docker command (uses sudo if required)
get_docker_cmd() {
	if docker info >/dev/null 2>&1; then
		echo "docker"
	elif command_exists sudo && sudo -n docker info >/dev/null 2>&1; then
		echo "sudo docker"
	else
		echo "docker"
	fi
}

# Export the variables required to render the beta nginx template safely.
export_beta_nginx_template_vars() {
	local domain=$1

	export DOMAIN="$domain"
	export API_DOMAIN="api.$domain"
	export MONITORING_DOMAIN="monitoring.$domain"
	export SENTRY_UI_DOMAIN="sentry.$domain"
	export PGADMIN_DOMAIN="pg.$domain"
	export HSTS_MAX_AGE="31536000"
	export GLOBAL_RATE_LIMIT="1000"
	export AUTH_RATE_LIMIT="10"
}

# Validate that every certificate path referenced in yamata-beta.conf exists and is valid
validate_nginx_certificates() {
	local domain=$1

	print_status "Validating certificates referenced in yamata-beta.conf"

	# Render the template into a temporary file with the provided domain values
	local tmp_conf
	tmp_conf=$(mktemp /tmp/yamata-nginx-certcheck.XXXXXX)

	export_beta_nginx_template_vars "$domain"

	if ! envsubst '$DOMAIN $API_DOMAIN $MONITORING_DOMAIN $SENTRY_UI_DOMAIN $PGADMIN_DOMAIN $HSTS_MAX_AGE $GLOBAL_RATE_LIMIT $AUTH_RATE_LIMIT' < "$NGINX_TEMPLATE" > "$tmp_conf"; then
		rm -f "$tmp_conf"
		print_error "Failed to render nginx template for certificate validation"
		exit 1
	fi

	if ! "$SCRIPT_DIR/check-yamata-certificates.sh" "$tmp_conf"; then
		rm -f -- "$tmp_conf"
		return 1
	fi
	rm -f -- "$tmp_conf"

	print_success "All referenced certificates exist and are valid"
}

# The pgAdmin virtual host intentionally shares DOMAIN's certificate paths.
# Verify that the existing certificate also covers pg.DOMAIN before Nginx is
# started, rather than discovering the missing SAN/wildcard in a browser.
validate_pgadmin_certificate_hostname() {
	local domain=$1
	local certificate="/etc/letsencrypt/live/$domain/fullchain.pem"
	local pgadmin_domain="pg.$domain"
	local privileged=()

	if [[ $EUID -ne 0 ]]; then
		command_exists sudo || {
			print_error "sudo is required to verify the pgAdmin certificate hostname"
			return 1
		}
		sudo -v
		privileged=(sudo)
	fi

	"${privileged[@]}" openssl x509 -in "$certificate" \
		-checkhost "$pgadmin_domain" -noout >/dev/null 2>&1 || {
		print_error "The existing certificate at $certificate does not cover $pgadmin_domain"
		print_error "Issue a certificate with pg.$domain as a SAN or use a wildcard certificate before deploying"
		return 1
	}
	print_success "Existing certificate covers $pgadmin_domain"
}

# Function to generate nginx configuration from template
generate_nginx_config() {
	local domain=$1
	local generated_dir="$NGINX_CONF_DIR/generated/beta"
	local pgadmin_generated_dir="$NGINX_CONF_DIR/generated/pgadmin"
	local temporary_config temporary_pgadmin_config
	
	print_status "Generating nginx configuration for domain: $domain"
	
	# Create generated directories if they don't exist.
	mkdir -p "$generated_dir"
	mkdir -p "$pgadmin_generated_dir"
	temporary_config=$(mktemp "$generated_dir/.yamata.conf.XXXXXX")
	temporary_pgadmin_config=$(mktemp "$pgadmin_generated_dir/.pgadmin.conf.XXXXXX")

	# Set environment variables for template processing
	export_beta_nginx_template_vars "$domain"
	
	# Read the template and replace environment variables
	if [ -f "$NGINX_TEMPLATE" ] && [ -f "$PGADMIN_NGINX_TEMPLATE" ]; then
		# Process the template with only specific environment variable substitution
		# Use envsubst with specific variables to avoid interfering with Nginx variables
		if ! envsubst '$DOMAIN $API_DOMAIN $MONITORING_DOMAIN $SENTRY_UI_DOMAIN $PGADMIN_DOMAIN $HSTS_MAX_AGE $GLOBAL_RATE_LIMIT $AUTH_RATE_LIMIT' < "$NGINX_TEMPLATE" > "$temporary_config"; then
			rm -f -- "$temporary_config"
			rm -f -- "$temporary_pgadmin_config"
			print_error "Failed to render nginx configuration"
			return 1
		fi
		if ! envsubst '$DOMAIN $API_DOMAIN $MONITORING_DOMAIN $SENTRY_UI_DOMAIN $PGADMIN_DOMAIN $HSTS_MAX_AGE $GLOBAL_RATE_LIMIT $AUTH_RATE_LIMIT' < "$PGADMIN_NGINX_TEMPLATE" > "$temporary_pgadmin_config"; then
			rm -f -- "$temporary_config"
			rm -f -- "$temporary_pgadmin_config"
			print_error "Failed to render pgAdmin Nginx configuration"
			return 1
		fi
		
		# SSL certificate paths are expected to be present in the rendered template.
		
		# Replace upstream server addresses for beta development
		sed -i "s|server app:8080 max_fails=3 fail_timeout=30s;|server app-beta:8080 max_fails=3 fail_timeout=30s;|g" "$temporary_config"
		sed -i "s|server app:9090 max_fails=3 fail_timeout=30s;|server app-beta:9090 max_fails=3 fail_timeout=30s;|g" "$temporary_config"
		chmod 644 "$temporary_config"
		chmod 644 "$temporary_pgadmin_config"
		mv -f -- "$temporary_config" "$generated_dir/yamata.conf"
		mv -f -- "$temporary_pgadmin_config" "$pgadmin_generated_dir/pgadmin.conf"

		print_success "Nginx and isolated pgAdmin-proxy configurations generated from templates"
	else
		rm -f -- "$temporary_config"
		rm -f -- "$temporary_pgadmin_config"
		print_error "Nginx template not found: $NGINX_TEMPLATE or $PGADMIN_NGINX_TEMPLATE"
		exit 1
	fi
}

# Function to make Docker bind-mounted config files readable by container users
normalize_docker_bind_mount_permissions() {
	print_status "Normalizing Docker bind-mounted config permissions..."

	if [ ! -d "$PROJECT_ROOT/docker" ]; then
		print_error "Docker config directory not found: $PROJECT_ROOT/docker"
		return 1
	fi

	find "$PROJECT_ROOT/docker" -type d -exec chmod 755 {} +
	find "$PROJECT_ROOT/docker" -type f \( \
		-name '*.conf' -o -name '*.yml' -o -name '*.yaml' -o -name '*.json' -o \
		-name '*.html' -o -name '*.sql' -o -name '*.py' -o -name 'Dockerfile*' \
	\) -exec chmod 644 {} +

	if [ -f "$PROJECT_ROOT/docker/postgres/process-init-beta.sh" ]; then
		chmod 755 "$PROJECT_ROOT/docker/postgres/process-init-beta.sh"
	fi

	if [ -f "$PROJECT_ROOT/docker/redis/start-redis.sh" ]; then
		chmod 755 "$PROJECT_ROOT/docker/redis/start-redis.sh"
	fi

	for prompt_file in \
		SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT \
		SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT; do
		if [ ! -f "$PROJECT_ROOT/$prompt_file" ]; then
			print_error "Required runtime prompt is missing: $PROJECT_ROOT/$prompt_file"
			return 1
		fi
		chmod 644 "$PROJECT_ROOT/$prompt_file"
	done

	print_success "Docker bind-mounted config permissions normalized"
}

# Function to validate the pre-provisioned beta environment file
create_beta_env() {
	# Check if .env.beta file already exists
	if [ -f "$ENV_FILE" ]; then
		[ ! -L "$ENV_FILE" ] || { print_error "Refusing symlinked environment file: $ENV_FILE"; return 1; }
		chmod 600 "$ENV_FILE"
		print_status "Using existing .env.beta file: $ENV_FILE"
		return 0
	fi

	print_error "No $ENV_FILE file found"
	
	exit 1
}

validate_pgadmin_secret_files() {
	local path username password_hash deploy_uid deploy_gid
	local has_bcrypt_entry=false invalid_bcrypt_entry=false
	deploy_uid=$(id -u)
	deploy_gid=$(id -g)

	validate_pgadmin_secret_metadata() {
		local variable=$1 expected_uid=$2 expected_gid=$3 expected_mode=$4
		local secret_path="${!variable:-}" uid gid mode

		[[ -n "$secret_path" ]] || { print_error "$variable is required"; return 1; }
		[[ -f "$secret_path" && ! -L "$secret_path" ]] || {
			print_error "$variable must name a regular, non-symlink file: $secret_path"
			return 1
		}
		uid=$(stat -c '%u' "$secret_path")
		gid=$(stat -c '%g' "$secret_path")
		mode=$(stat -c '%a' "$secret_path")
		[[ "$uid" == "$expected_uid" && "$gid" == "$expected_gid" && "$mode" == "$expected_mode" ]] || {
			print_error "$secret_path must be owned by $expected_uid:$expected_gid with mode $expected_mode (found $uid:$gid mode $mode)"
			return 1
		}
	}

	# The deployment user owns these source files, so Compose can read them
	# without the deployment itself requiring root. Compose file-backed secrets
	# preserve the source-file ownership and mode: pgAdmin runs with primary GID
	# 0 and the Nginx workers use GID 65534.
	validate_pgadmin_secret_metadata PGADMIN_ENV_FILE "$deploy_uid" "$deploy_gid" 600
	validate_pgadmin_secret_metadata PGADMIN_DEFAULT_PASSWORD_FILE "$deploy_uid" 0 640
	validate_pgadmin_secret_metadata PGADMIN_NGINX_HTPASSWD_FILE "$deploy_uid" 65534 640

	path="$PGADMIN_DEFAULT_PASSWORD_FILE"
	[[ -s "$path" ]] || {
		print_error "$path must contain a non-empty initial pgAdmin password"
		return 1
	}

	while IFS=: read -r username password_hash || [[ -n "$username" || -n "$password_hash" ]]; do
		[[ -z "$username" || "$username" == \#* ]] && continue
		if [[ "$password_hash" =~ ^\$2[aby]\$([0-9]{2})\$ ]] &&
			(( 10#${BASH_REMATCH[1]} >= 12 )); then
			has_bcrypt_entry=true
		else
			invalid_bcrypt_entry=true
		fi
	done < "$PGADMIN_NGINX_HTPASSWD_FILE"
	[[ "$has_bcrypt_entry" == true && "$invalid_bcrypt_entry" == false ]] || {
		print_error "$PGADMIN_NGINX_HTPASSWD_FILE must contain only bcrypt htpasswd entries with cost 12 or higher"
		return 1
	}

	# The env file is intentionally restricted to the initial pgAdmin email.
	# Configuration settings belong in the reviewed, read-only distro config.
	awk '
		/^[[:space:]]*($|#)/ { next }
		/^[[:space:]]*PGADMIN_DEFAULT_EMAIL[[:space:]]*=[[:space:]]*[^[:space:]#]/ { emails++; next }
		{ invalid = 1 }
		END { exit invalid || emails != 1 }
	' "$PGADMIN_ENV_FILE" || {
		print_error "$PGADMIN_ENV_FILE must contain only one non-empty PGADMIN_DEFAULT_EMAIL assignment"
		return 1
	}

	print_success "pgAdmin secret files have the required ownership, modes, and bcrypt cost"
}

# The pgAdmin Nginx listener is deliberately bound to one explicit host
# interface. Compose would otherwise publish it on every host interface.
validate_pgadmin_listener_bind_ip() {
	local bind_ip="${PGADMIN_LISTEN_BIND_IP:-}" octet
	local -a octets=()

	[[ "$bind_ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || {
		print_error "PGADMIN_LISTEN_BIND_IP must be the IPv4 address of the selected host interface"
		return 1
	}
	IFS=. read -r -a octets <<< "$bind_ip"
	for octet in "${octets[@]}"; do
		(( 10#$octet <= 255 )) || {
			print_error "PGADMIN_LISTEN_BIND_IP is not a valid IPv4 address: $bind_ip"
			return 1
		}
	done
	[[ "$bind_ip" != "0.0.0.0" && "$bind_ip" != 127.* ]] || {
		print_error "PGADMIN_LISTEN_BIND_IP must not be a wildcard or loopback address"
		return 1
	}

	print_success "pgAdmin listener is restricted to $bind_ip:14433"
}

# Function to check prerequisites
check_prerequisites() {
	print_status "Checking prerequisites..."
	local required_command
	for required_command in python3 envsubst openssl sed find mktemp stat awk; do
		if ! command_exists "$required_command"; then
			print_error "Required command is not installed: $required_command"
			exit 1
		fi
	done
	
	# Check Docker
	if ! command_exists docker; then
		print_error "Docker is not installed. Please install Docker first."
		exit 1
	fi
	
	local docker_cmd
	docker_cmd=$(get_docker_cmd)
	# Check if docker compose is available (Docker Compose V2)
	if ! $docker_cmd compose version >/dev/null 2>&1; then
		print_error "Docker Compose is not available. Please ensure Docker Compose V2 is installed."
		exit 1
	fi
	
	# Check if Docker daemon is running
	if ! $docker_cmd info >/dev/null 2>&1; then
		print_error "Docker daemon is not running. Please start Docker first."
		exit 1
	fi
	
	print_success "All prerequisites are satisfied"
}

# Function to check for HTTP proxy environment variables
check_http_proxy() {
	local proxy_found=false
	
	# Check for HTTP proxy in various formats
	if [ -n "${HTTP_PROXY:-}" ]; then
		print_status "HTTP_PROXY is configured (value redacted)"
		proxy_found=true
	fi
	
	if [ -n "${http_proxy:-}" ]; then
		print_status "http_proxy is configured (value redacted)"
		proxy_found=true
	fi
	
	if [ -n "${HTTPS_PROXY:-}" ]; then
		print_status "HTTPS_PROXY is configured (value redacted)"
		proxy_found=true
	fi
	
	if [ -n "${https_proxy:-}" ]; then
		print_status "https_proxy is configured (value redacted)"
		proxy_found=true
	fi
	
	if [ "$proxy_found" = true ]; then
		print_success "HTTP proxy configuration detected"
		return 0
	else
		print_warning "No HTTP proxy configuration found"
		return 0
	fi
}

# Function to start services (all except app-beta)
stop_application_writers() {
	print_status "Stopping API and campaign scheduler before migrations..."
	local docker_cmd
	docker_cmd=$(get_docker_cmd)
	if $docker_cmd container inspect yamata-campaign-scheduler-beta >/dev/null 2>&1; then
		$docker_cmd stop --time 60 yamata-campaign-scheduler-beta >/dev/null
	fi
	$docker_cmd compose --env-file "$ENV_FILE" -f docker-compose.beta.yml stop -t 60 app-beta >/dev/null 2>&1 || true
	print_success "Database writers stopped"
}

# Function to start services (all except app-beta)
start_services() {
	print_status "Starting Docker Compose services (excluding app-beta)..."
	
	# Resolve docker command (fallback to sudo if needed)
	local docker_cmd
	docker_cmd=$(get_docker_cmd)

	# Dockerfile.edge deliberately uses a local OpenResty base-image alias. This
	# avoids a 60-second Docker Hub metadata timeout on every Compose build;
	# the helper pulls the pinned upstream image only if the alias is absent.
	"$SCRIPT_DIR/ensure-openresty-base-image.sh"
	
	# Process init.sql with environment variables for beta environment
	print_status "Processing PostgreSQL init.sql for beta environment..."
	if [ -f "docker/postgres/process-init-beta.sh" ]; then
		./docker/postgres/process-init-beta.sh
		if [ $? -ne 0 ]; then
			print_error "Failed to process init.sql for beta environment"
			return 1
		fi
	else
		print_error "process-init-beta.sh not found"
		return 1
	fi
	
	# Start all supporting services explicitly, excluding app-beta to allow safe DB migration
	# Note: nginx-beta depends on app-beta, so we start it after app-beta to avoid pulling app-beta up implicitly
	$docker_cmd compose --env-file "$ENV_FILE" -f docker-compose.beta.yml up -d --build \
		postgres-beta \
		redis-beta \
		sentry-postgres-beta \
		sentry-redis-beta \
		sentry-beta \
		prometheus-beta \
		grafana-beta \
		frontend-beta \
		postgres-backup-beta \
		postgres-exporter-beta \
		pgadmin-beta \
		pgadmin-nginx-beta \
		node-exporter-beta \
		cadvisor-beta

	print_success "Core services started successfully (app-beta not started)"
}

# Function to wait for services to be ready
wait_for_services() {
	print_status "Waiting for services to be ready..."
	
	# Resolve docker command (fallback to sudo if needed)
	local docker_cmd
	docker_cmd=$(get_docker_cmd)
	
	local max_attempts=30
	local attempt=1
	local last_blocker=""
	local containers=(
		yamata-postgres-beta yamata-redis-beta yamata-sentry-postgres-beta
		yamata-sentry-redis-beta yamata-sentry-beta yamata-prometheus-beta
		yamata-grafana-beta frontend-beta yamata-postgres-backup-beta yamata-pgadmin-beta yamata-pgadmin-nginx-beta
		yamata-postgres-exporter-beta yamata-node-exporter-beta yamata-cadvisor-beta
	)
	
	while [ $attempt -le $max_attempts ]; do
		local all_running=true
		local container state health
		for container in "${containers[@]}"; do
			state=$($docker_cmd inspect -f '{{.State.Status}}' "$container" 2>/dev/null || echo missing)
			health=$($docker_cmd inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container" 2>/dev/null || echo missing)
			# A large first backup can outlast this readiness loop. Docker keeps
			# checking its freshness independently after the deployment completes.
			if [ "$state" != running ] || { \
				[ "$health" != none ] && [ "$health" != healthy ] && \
				{ [ "$container" != yamata-postgres-backup-beta ] || [ "$health" != starting ]; }; \
			}; then
				all_running=false
				last_blocker="$container (state: $state, health: $health)"
				break
			fi
		done
		if [ "$all_running" = true ]; then
			print_success "Services are ready!"
			return 0
		fi
		
		echo "Attempt $attempt/$max_attempts - Waiting for $last_blocker..."
		sleep 10
		attempt=$((attempt + 1))
	done
	
	print_error "Services failed to start within expected time (last blocker: ${last_blocker:-unknown})"
	return 1
}

# pgAdmin stores editor settings per user in its persistent configuration
# database. The image loads preferences.json only when that database is first
# initialized, so explicitly apply the supported setting on every deployment
# for both new and existing installations.
configure_pgadmin_query_autocomplete() {
	print_status "Enabling pgAdmin Query Tool autocomplete..."
	local docker_cmd
	docker_cmd=$(get_docker_cmd)

	$docker_cmd exec --user 5050:0 yamata-pgadmin-beta sh -ec '
		cd /pgadmin4
		exec /venv/bin/python3 setup.py set-prefs "$PGADMIN_DEFAULT_EMAIL" \
			"sqleditor:auto_completion:autocomplete_on_key_press=true"
	'
	print_success "pgAdmin Query Tool autocomplete enabled"
}

# Wait for app-beta container to become healthy
wait_for_app_health() {
	print_status "Waiting for app-beta health..."
	local docker_cmd
	docker_cmd=$(get_docker_cmd)
	local max_attempts=40
	local attempt=1
	local status="unknown"
	while [ $attempt -le $max_attempts ]; do
		status=$($docker_cmd inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' yamata-app-beta 2>/dev/null || echo "unknown")
		if [ "$status" = "healthy" ]; then
			print_success "app-beta is healthy"
			return 0
		fi
		print_status "Attempt $attempt/$max_attempts - app-beta health status: $status"
		sleep 5
		attempt=$((attempt + 1))
	done
	print_warning "app-beta did not become healthy within expected time (last status: $status)"
	return 1
}

# Function to start app-beta (and nginx-beta after)
start_app_service() {
	print_status "Starting app-beta and nginx-beta services..."
	local docker_cmd
	docker_cmd=$(get_docker_cmd)
	$docker_cmd compose --env-file "$ENV_FILE" -f docker-compose.beta.yml up -d app-beta
	print_success "app-beta started"
	# Start nginx after app-beta to avoid implicit dependency startup. Recreate it
	# so Docker refreshes the individual nginx.conf bind mount after git replaces
	# that file during an update. The generated vhost is directory-mounted and
	# can otherwise get ahead of a stale nginx.conf in the existing container.
	$docker_cmd compose --env-file "$ENV_FILE" -f docker-compose.beta.yml up -d --force-recreate nginx-beta
	print_success "nginx-beta started"
	# Start services that depend on nginx after the proxy is up.
	$docker_cmd compose --env-file "$ENV_FILE" -f docker-compose.beta.yml up -d --build cert-monitor-beta
	print_success "cert-monitor-beta started"
	$docker_cmd compose --env-file "$ENV_FILE" -f docker-compose.beta.yml up -d --build nginx-sentry-forwarder-beta
	print_success "nginx-sentry-forwarder-beta started"
}

# Function to display deployment information
show_deployment_info() {
	local domain=$1
	
	print_success "🎉 Beta deployment completed successfully!"
	echo ""
	echo "📋 Deployment Information:"
	echo "  Domain: https://$domain"
	echo "  API: https://api.$domain"
	echo "  Monitoring: https://monitoring.$domain"
	echo "  Sentry: https://sentry.$domain"
	echo "  pgAdmin: https://pg.$domain:14433 (HTTP Basic Auth + pgAdmin login required)"
	echo ""
	echo "⚠️  Important Notes:"
	echo "  - SSL certificates must already exist and be valid"
	echo "  - All services are running in beta mode"
	echo ""
	echo "🚀 Your application is ready at: https://$domain"
}

# Function to show help message
show_help() {
	echo "Usage: $0 <domain> [OPTIONS]"
	echo ""
	echo "Arguments:"
	echo "  domain              Domain name (e.g., thewritingonthewall.com)"
	echo ""
	echo "Options:"
	echo "  --domain            Override the default domain (e.g., yourdomain.com)"
	echo "  --help, -h          Show this help message"
	echo ""
	echo "Environment Configuration:"
	echo "  - A pre-provisioned, regular .env.beta file is required"
	echo "  - The file is restricted to mode 0600 before it is loaded"
	echo ""
	echo "SSL Certificate Configuration:"
	echo "  - Certificates must be obtained before running this script"
	echo "  - The script only checks that certificate files referenced by nginx exist and are not expired"
	echo ""
	echo "Examples:"
	echo "  $0 yourdomain.com                    # Use the existing .env.beta"
	echo "  $0 yourdomain.com --domain=yourdomain.com"
	echo ""
}

# Main function
main() {
	cd "$PROJECT_ROOT"
	echo "🐍 Yamata no Orochi - Beta Deployment"
	echo "======================================"
	echo ""
	
	# Parse command line arguments
	local domain="" # Default domain, can be overridden by argument
	
	# Parse command line arguments
	while [[ $# -gt 0 ]]; do
		case $1 in
			--domain)
				[ $# -ge 2 ] || { print_error "--domain requires a value"; exit 2; }
				domain="$2"
				shift 2
				;;
			--domain=*)
				domain="${1#--domain=}"
				shift
				;;
			--help|-h)
				show_help
				exit 0
				;;
			*)
				if [ -z "$domain" ]; then
					domain=$1 # First argument is domain if not an option
				else
					print_error "Unknown option or multiple domains specified: $1"
					show_help
					exit 1
				fi
				shift
				;;
		esac
	done
	
	# Require an explicit domain to avoid deploying an unintended default host.
	if [ -z "$domain" ]; then
		print_error "Domain name is required."
		show_help
		exit 1
	fi
	
	# Validate domain format (supports subdomains)
	if [[ ! "$domain" =~ ^([a-zA-Z0-9]([-a-zA-Z0-9]{0,61}[a-zA-Z0-9])\.)+[a-zA-Z]{2,}$ ]]; then
		print_error "Invalid domain format: $domain"
		echo "Please provide a valid domain name (e.g., thewritingonthewall.com)"
		exit 1
	fi
	
	print_status "Starting beta deployment for domain: $domain"
	
	# Check and display proxy information
	echo ""
	print_status "Checking HTTP proxy configuration..."
	check_http_proxy
	echo ""
	
	# Check prerequisites
	check_prerequisites

	# Validate certificate files referenced by nginx config
	validate_nginx_certificates "$domain"
	validate_pgadmin_certificate_hostname "$domain"
	
	# Generate nginx configuration from template
	generate_nginx_config "$domain"

	# Ensure bind-mounted configs are readable by non-root container users
	normalize_docker_bind_mount_permissions
	
	# Create beta environment file
	create_beta_env
	
	# Source environment variables for database initialization
	if [ -f "$ENV_FILE" ]; then
		# Treat dotenv content as data; never execute it as shell code.
		# shellcheck disable=SC1091
		source "$SCRIPT_DIR/load-yamata-env.sh"
		load_yamata_env_file "$ENV_FILE"
	fi
	# Keep Compose and the generated Nginx configuration on the same explicit domain.
	export_beta_nginx_template_vars "$domain"
	validate_pgadmin_secret_files
	validate_pgadmin_listener_bind_ip

	if [ "${CAMPAIGN_EXECUTION_ENABLED:-}" != "false" ]; then
		print_error "CAMPAIGN_EXECUTION_ENABLED must remain false in .env.beta"
		print_error "Campaign execution is managed by the isolated scheduler container"
		exit 1
	fi
	if [ "${SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED:-}" != "true" ]; then
		print_error "SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED must remain true in .env.beta"
		print_error "Exact Smart Targeting capacity jobs are managed by the main API instance"
		exit 1
	fi
	if [ "${SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED:-${SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED:-false}}" != "true" ]; then
		print_error "SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED must remain true in .env.beta"
		print_error "Smart Targeting Test sampling jobs are managed by the main API instance"
		exit 1
	fi
	if [ "${SMART_TARGETING_EXECUTION_CALCULATION_SCHEDULER_ENABLED:-${SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED:-false}}" != "true" ]; then
		print_error "SMART_TARGETING_EXECUTION_CALCULATION_SCHEDULER_ENABLED must remain true in .env.beta"
		print_error "Execution audience calculation jobs are managed by the main API instance"
		exit 1
	fi
	if [ "${TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED:-}" != "true" ]; then
		print_error "TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED must remain true in .env.beta"
		print_error "Tag Test performance jobs are managed by the main API instance"
		exit 1
	fi
	if [ "${SMART_TAG_EVALUATION_ENABLED:-}" != "true" ] ||
		[ "${SMART_TAG_EVALUATION_SCHEDULER_ENABLED:-}" != "true" ]; then
		print_error "Smart-tag evaluation and its scheduler must remain enabled in .env.beta"
		print_error "Smart-tag scoring jobs are managed by the main API instance"
		exit 1
	fi

	# Start core services (excluding app-beta)
	stop_application_writers
	start_services
	wait_for_services
	configure_pgadmin_query_autocomplete
	
	# Initialize database and apply migrations
	print_status "Initializing database and applying migrations..."
	
	./scripts/init-beta-database.sh --yes
	# Routine deployments must not replay historical backfills or maintenance
	# operations. The pending-migration runner above performs schema changes once;
	# this second pass is deliberately read-only.
	./scripts/apply-yamata-required-migrations.sh --verify-only "$PROJECT_ROOT"
	print_success "Database initialization completed"
	
	# Start app service after successful migrations
	start_app_service
	
	# Wait for app-beta to report healthy status
	wait_for_app_health
	# The forwarder is auxiliary observability infrastructure. Its Docker health
	# check continues in the background and must not delay an application deploy.
	print_status "nginx-sentry-forwarder-beta health check continues in background"
	
	# Show deployment information
	show_deployment_info "$domain"
}

# Run main function with all arguments
main "$@"
