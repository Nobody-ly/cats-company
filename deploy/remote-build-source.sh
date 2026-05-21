#!/usr/bin/env bash
set -euo pipefail

root="${1:?stack root is required}"
revision="${2:?revision is required}"
owner="${3:?GHCR owner is required}"

source_bundle="$root/releases/cats-company-source-${revision}.tar.gz"
source_root="$root/source/$revision"

if [ ! -f "$source_bundle" ]; then
  echo "missing source bundle: $source_bundle" >&2
  exit 1
fi

rm -rf "$source_root"
mkdir -p "$source_root"
tar -xzf "$source_bundle" -C "$source_root"

cd "$source_root"
docker build \
  --build-arg GOPROXY="${REMOTE_GOPROXY:-https://goproxy.cn,direct}" \
  -f deploy/Dockerfile.server \
  -t "ghcr.io/${owner}/cats-company-server:${revision}" \
  .

if [ -n "${GHCR_USERNAME:-}" ] && [ -n "${GHCR_TOKEN:-}" ]; then
  printf '%s\n' "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USERNAME" --password-stdin >/dev/null
fi

timeout "${REMOTE_WEB_PULL_TIMEOUT_SECONDS:-420}" docker pull "ghcr.io/${owner}/cats-company-web:${revision}"

find "$root/source" -mindepth 1 -maxdepth 1 -type d -mtime +7 -exec rm -rf {} +
find "$root/releases" -mindepth 1 -maxdepth 1 -name 'cats-company-source-*.tar.gz' -type f -mtime +7 -delete
find "$root/releases" -mindepth 1 -maxdepth 1 -name 'cats-company-images-*.tar.gz' -type f -delete
