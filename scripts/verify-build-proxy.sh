#!/usr/bin/env bash
set -Eeuo pipefail
image="${1:-yamata-no-orochi}"
docker image inspect "$image" --format '{{json .Config.Env}}' | python3 -c '
import json,sys
env=json.load(sys.stdin) or []
names={item.split("=",1)[0].upper() for item in env}
if names & {"HTTP_PROXY","HTTPS_PROXY"}: raise SystemExit("Image config persists HTTP_PROXY or HTTPS_PROXY")
print("No HTTP_PROXY or HTTPS_PROXY in image config")
'
docker image history --no-trunc --format '{{.CreatedBy}}' "$image" | python3 -c '
import re,sys
content=sys.stdin.read()
if re.search(r"https?://[^\s/:@]+:[^\s/@]+@[^\s/]+",content,re.I): raise SystemExit("Possible proxy credentials in image history")
print("No proxy credential URLs detected in image history")
'
