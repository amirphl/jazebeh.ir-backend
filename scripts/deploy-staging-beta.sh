#!/usr/bin/env bash
set -Eeuo pipefail
set +x
umask 077
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
domain=staging.jazebeh.ir
while (($#)); do
	case "$1" in
	--domain)
		[[ $# -ge 2 ]] || {
			echo 'Missing domain' >&2
			exit 2
		}
		domain="$2"
		shift 2
		;;
	--help | -h)
		echo 'Usage: deploy-staging-beta.sh --domain staging.jazebeh.ir'
		exit 0
		;;
	*)
		printf 'Unknown option: %s\n' "$1" >&2
		exit 2
		;;
	esac
done
[[ "$domain" == staging.jazebeh.ir ]] || {
	echo 'This staging configuration requires staging.jazebeh.ir' >&2
	exit 1
}
cd "$root"
[[ -f .env.staging && ! -L .env.staging ]] || {
	echo 'Create .env.staging from config/staging.env.example first' >&2
	exit 1
}
source "$script_dir/load-yamata-env.sh"
load_yamata_env_file "$root/.env.staging"
[[ "${APP_ENV:-}" == staging && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Staging requires mock mode' >&2
	exit 1
}
source "$script_dir/guard-environment.sh"
reject_production_like_target "${DB_NAME:-}" "postgres-beta"
export STAGING_ENV_FILE=.env.staging
for key in DB_PASSWORD JWT_SECRET_KEY SENTRY_POSTGRES_PASSWORD SENTRY_GLITCHTIP_SECRET_KEY SENTRY_SUPERUSER_PASSWORD GRAFANA_ADMIN_PASSWORD BOT_PASSWORD MOCK_SEED_ADMIN_PASSWORD; do
	[[ -n "${!key:-}" && "${!key}" != CHANGE_ME ]] || {
		printf 'Set %s in .env.staging\n' "$key" >&2
		exit 1
	}
done
if [[ -n "${SENTRY_DSN:-}" ]]; then
	python3 -c '
import sys, urllib.parse
p = urllib.parse.urlsplit(sys.argv[1])
valid = p.scheme == "http" and p.hostname == "sentry-beta" and p.port == 8000 and bool(p.username)
valid = valid and p.path.rstrip("/").rsplit("/", 1)[-1].isdigit()
sys.exit(0 if valid else 1)
' "$SENTRY_DSN" || {
		echo 'Staging SENTRY_DSN must use http://<key>@sentry-beta:8000/sentry/<project-id>' >&2
		exit 1
	}
fi
for key in PGADMIN_ENV_FILE PGADMIN_DEFAULT_PASSWORD_FILE PGADMIN_NGINX_HTPASSWD_FILE; do
	[[ -f "${!key:-}" && ! -L "${!key}" ]] || {
		printf 'Set %s to a protected regular file\n' "$key" >&2
		exit 1
	}
done
cert_name="${STAGING_CERT_NAME:-jazebeh.ir}"
[[ "$cert_name" =~ ^[A-Za-z0-9.-]+$ ]] || {
	echo 'Invalid STAGING_CERT_NAME' >&2
	exit 1
}
cert="/etc/letsencrypt/live/$cert_name/fullchain.pem"
key="/etc/letsencrypt/live/$cert_name/privkey.pem"
chain="/etc/letsencrypt/live/$cert_name/chain.pem"
[[ -r "$cert" && -r "$key" && -r "$chain" ]] || {
	echo 'Certificate, chain and key must be readable by the deploy user' >&2
	exit 1
}
# openssl x509 -checkhost reports mismatches in its output and can still exit 0.
openssl x509 -in "$cert" -checkhost "$domain" -noout | grep -Fq 'does match certificate' || {
	echo 'Certificate does not cover staging hostname' >&2
	exit 1
}
openssl x509 -in "$cert" -checkend 0 -noout >/dev/null || {
	echo 'Certificate has expired' >&2
	exit 1
}
cert_pub="$(openssl x509 -in "$cert" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)"
key_pub="$(openssl pkey -in "$key" -pubout -outform DER | openssl dgst -sha256)"
[[ "$cert_pub" == "$key_pub" ]] || {
	echo 'Certificate and private key do not match' >&2
	exit 1
}
for cmd in docker envsubst curl; do command -v "$cmd" >/dev/null || {
	printf 'Missing %s\n' "$cmd" >&2
	exit 1
}; done
getent ahostsv4 "$domain" >/dev/null || {
	echo 'Staging domain does not resolve' >&2
	exit 1
}
docker compose version >/dev/null
docker info >/dev/null
for container in yamata-app-beta yamata-campaign-scheduler-beta yamata-postgres-beta yamata-redis-beta; do
	if docker inspect "$container" >/dev/null 2>&1; then
		project="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$container")"
		[[ "$project" == yamata-staging ]] || {
			printf 'Refusing to touch non-staging container %s\n' "$container" >&2
			exit 1
		}
	fi
done
docker image inspect yamata-frontend-beta >/dev/null 2>&1 || {
	echo 'Provision the yamata-frontend-beta image first' >&2
	exit 1
}
mkdir -p docker/nginx/sites-available/generated/staging docker/nginx/sites-available/generated/staging-pgadmin
export STAGING_CERT_NAME="$cert_name"
envsubst '$STAGING_CERT_NAME' <docker/nginx/sites-available/staging.conf.template >docker/nginx/sites-available/generated/staging/yamata.conf
envsubst '$STAGING_CERT_NAME' <docker/nginx/sites-available/staging-pgadmin.conf.template >docker/nginx/sites-available/generated/staging-pgadmin/pgadmin.conf
compose=(docker compose -p yamata-staging --env-file .env.staging -f docker-compose.beta.yml -f docker-compose.staging.yml)
"${compose[@]}" config -q
"$script_dir/ensure-openresty-base-image.sh"
docker build -f docker/Dockerfile.production -t yamata-no-orochi-staging .
"${compose[@]}" stop -t 60 app-beta campaign-scheduler-beta
"${compose[@]}" up -d postgres-beta redis-beta
for attempt in {1..60}; do
	[[ "$(docker inspect -f '{{.State.Health.Status}}' yamata-postgres-beta 2>/dev/null || true)" == healthy ]] && break
	((attempt < 60)) || {
		echo 'PostgreSQL did not become healthy' >&2
		exit 1
	}
	sleep 2
done
"$script_dir/migrate-staging.sh"
"$script_dir/seed-mock-reference-data.sh" --environment staging
"${compose[@]}" up -d --build
for attempt in {1..90}; do
	if curl --noproxy '*' -fsS --resolve "staging.jazebeh.ir:443:127.0.0.1" https://staging.jazebeh.ir/health >/dev/null; then
		break
	fi
	((attempt < 90)) || {
		echo 'Staging did not pass the HTTPS health check; inspect docker compose logs' >&2
		exit 1
	}
	sleep 2
done
for attempt in {1..120}; do
	unhealthy=0
	for service in postgres-beta redis-beta postgres-backup-beta sentry-postgres-beta sentry-redis-beta sentry-beta app-beta campaign-scheduler-beta pgadmin-beta pgadmin-nginx-beta nginx-beta nginx-sentry-forwarder-beta cert-monitor-beta prometheus-beta grafana-beta postgres-exporter-beta node-exporter-beta cadvisor-beta frontend-beta; do
		container="$("${compose[@]}" ps -q "$service")"
		[[ -n "$container" ]] || {
			unhealthy=1
			continue
		}
		state="$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container")"
		[[ "$state" == 'running healthy' || "$state" == 'running none' ]] || unhealthy=1
	done
	if ((unhealthy == 0)); then
		"$script_dir/verify-staging-no-external-egress.sh"
		if [[ -n "${SENTRY_DSN:-}" ]]; then
			# Use the same client and internal network path as the Nginx forwarder.
			# A successful request proves the transformed DSN reaches GlitchTip; it
			# does not expose the DSN in logs or process arguments.
			docker exec yamata-nginx-sentry-forwarder-beta python -c '
import runpy
module = runpy.run_path("/app/nginx_sentry_forwarder.py")
client = module["SentryStoreClient"]()
client.send(client.event(
    message="staging Sentry delivery probe",
    level="info",
    status=None,
    request_data={"url": "https://staging.jazebeh.ir/health", "method": "DEPLOY"},
    extra={"source": "deploy-staging-beta"},
))
'
		fi
		echo 'Staging services are healthy at https://staging.jazebeh.ir'
		exit 0
	fi
	((attempt < 120)) || {
		echo 'Some staging services did not become healthy; inspect docker compose ps and logs' >&2
		exit 1
	}
	sleep 2
done
