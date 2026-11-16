#!/usr/bin/env bash

# Ensure the local image alias used by docker/nginx/Dockerfile.edge exists.
# Docker BuildKit resolves Docker Hub metadata even when the upstream base
# image is already cached. Using a local alias keeps ordinary Compose builds
# offline while retaining a deterministic, pinned fetch when the image is new.

set -Eeuo pipefail
set +x

readonly UPSTREAM_IMAGE='openresty/openresty:1.31.1.1-2-alpine-fat@sha256:427d94fea0c24b099e7891e8d1b7976f6d008e2d427e56bab725c8b8b293795b'
readonly LOCAL_IMAGE='yamata-openresty-base:1.31.1.1-2-alpine-fat'

if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
else
	printf '[openresty-base] ERROR: Docker daemon is unavailable or access is denied\n' >&2
	exit 1
fi

if "${DOCKER[@]}" image inspect "$LOCAL_IMAGE" >/dev/null 2>&1; then
	printf '[openresty-base] Using local image %s\n' "$LOCAL_IMAGE"
	exit 0
fi

printf '[openresty-base] Local image is absent; pulling pinned upstream image\n'
"${DOCKER[@]}" pull "$UPSTREAM_IMAGE"

image_id=$("${DOCKER[@]}" image inspect --format '{{.Id}}' "$UPSTREAM_IMAGE")
"${DOCKER[@]}" image tag "$image_id" "$LOCAL_IMAGE"
printf '[openresty-base] Tagged %s as %s\n' "$UPSTREAM_IMAGE" "$LOCAL_IMAGE"
