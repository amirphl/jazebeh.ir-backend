#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${APP_ENV:-}" == staging && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Set APP_ENV=staging and EXTERNAL_SERVICES_MODE=mock' >&2
	exit 1
}
echo 'Operational environment: staging (mock)'
for container in yamata-prometheus-beta yamata-grafana-beta yamata-sentry-beta yamata-nginx-sentry-forwarder-beta yamata-cert-monitor-beta; do
	project="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$container")"
	[[ "$project" == yamata-staging ]] || {
		printf '%s is not part of yamata-staging\n' "$container" >&2
		exit 1
	}
	state="$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' "$container")"
	[[ "$state" == 'running healthy' || "$state" == 'running no-healthcheck' ]] || {
		printf '%s is %s\n' "$container" "$state" >&2
		exit 1
	}
	printf '%s: %s\n' "$container" "$state"
done
docker exec yamata-prometheus-beta wget -qO- http://127.0.0.1:9090/api/v1/targets | python3 -c '
import json,sys,urllib.parse
data=json.load(sys.stdin)
targets=data["data"]["activeTargets"]
allowed={"app-beta", "campaign-scheduler-beta", "postgres-exporter-beta", "node-exporter-beta", "cadvisor-beta"}
if not targets: raise SystemExit("Prometheus has no active targets")
for target in targets:
    host=urllib.parse.urlsplit(target["scrapeUrl"]).hostname
    if host not in allowed or target.get("health") != "up": raise SystemExit("unsafe or unhealthy Prometheus target: " + str(host))
    labels=target.get("labels", {})
    if labels.get("environment") == "production": raise SystemExit("production-labelled target in staging")
print("Prometheus targets are staging-only and healthy")
'
curl --noproxy '*' -fsS --resolve staging.jazebeh.ir:443:127.0.0.1 https://staging.jazebeh.ir/grafana/ >/dev/null
curl --noproxy '*' -fsS --resolve staging.jazebeh.ir:443:127.0.0.1 https://staging.jazebeh.ir/sentry/ >/dev/null
