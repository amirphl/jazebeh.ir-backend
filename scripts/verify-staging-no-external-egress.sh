#!/usr/bin/env bash
set -Eeuo pipefail

project="${1:-yamata-staging}"
network="${project}_yamata-network-beta"

[[ "$(docker network inspect -f '{{.Internal}}' "$network")" == "true" ]] || {
	printf '%s must be an internal Docker network\n' "$network" >&2
	exit 1
}

for container in yamata-app-beta yamata-campaign-scheduler-beta; do
	owner="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$container")"
	[[ "$owner" == "$project" ]] || { printf '%s is not owned by %s\n' "$container" "$project" >&2; exit 1; }
	[[ "$(docker inspect -f '{{.State.Running}}' "$container")" == true ]] || { printf '%s is not running\n' "$container" >&2; exit 1; }
	networks="$(docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' "$container")"
	[[ -n "$networks" ]] || { printf '%s has no Docker network\n' "$container" >&2; exit 1; }
	[[ $'\n'"$networks"$'\n' == *$'\n'"$network"$'\n'* ]] || { printf '%s is not attached to %s\n' "$container" "$network" >&2; exit 1; }
	while IFS= read -r attached; do
		[[ -z "$attached" ]] && continue
		[[ "$(docker network inspect -f '{{.Internal}}' "$attached")" == true ]] || { printf '%s is attached to non-internal network %s\n' "$container" "$attached" >&2; exit 1; }
	done <<< "$networks"
	# A local HTTP request verifies docker exec, curl and the HTTP stack before
	# interpreting a failed external request as an isolation signal.
	docker exec "$container" curl --noproxy '*' -fsS --connect-timeout 3 --max-time 5 http://127.0.0.1:8080/api/v1/health >/dev/null || {
		printf '%s cannot run the local HTTP control probe\n' "$container" >&2; exit 1;
	}
	# -k avoids making certificate validation the reason this probe passes. A
	# successful TCP/TLS exchange with this public IP is always a failure.
	status=0
	docker exec "$container" curl --noproxy '*' -k --connect-timeout 3 --max-time 5 --silent --show-error --output /dev/null https://1.1.1.1/ || status=$?
	if [[ "$status" == 0 ]]; then
		printf '%s reached a public address; staging egress isolation is broken\n' "$container" >&2
		exit 1
	fi
	[[ "$status" == 7 || "$status" == 28 ]] || { printf '%s external probe failed unexpectedly (exit %s)\n' "$container" "$status" >&2; exit 1; }
done

printf 'Staging app and campaign scheduler use only internal Docker networks; the public HTTP probe did not connect.\n'
