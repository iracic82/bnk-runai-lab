#!/usr/bin/env bash
# Copy every Run:ai image local-dev.sh built (local Kind registry, localhost:5001/runai-local/*)
# to Docker Hub as ${DOCKERHUB_USER}/${PREFIX}<service>:<tag>, and list them in
# /root/runai-images.txt. Needs DOCKERHUB_TOKEN in the environment (never echoed).
set -euo pipefail
DOCKERHUB_USER="${DOCKERHUB_USER:-iracic82}"
PREFIX="${IMAGE_PREFIX:-runai-oss-}"
REG=localhost:5001
OUT=/root/runai-images.txt
[ -n "${DOCKERHUB_TOKEN:-}" ] || { echo "DOCKERHUB_TOKEN is not set (it is an Instruqt secret; check: env | grep -c DOCKERHUB_TOKEN)" >&2; exit 1; }
command -v jq >/dev/null || apt-get install -y -qq jq >/dev/null

printf '%s' "$DOCKERHUB_TOKEN" | docker login -u "$DOCKERHUB_USER" --password-stdin >/dev/null
trap 'docker logout >/dev/null 2>&1 || true' EXIT

repos=$(curl -s "http://${REG}/v2/_catalog?n=1000" | jq -r '.repositories[]' | grep '^runai-local/' || true)
[ -n "$repos" ] || { echo "no runai-local/* images in ${REG}; did local-dev.sh finish?" >&2; exit 1; }
: > "$OUT"
n=0; failed=0
for repo in $repos; do
  svc="${repo#runai-local/}"
  for tag in $(curl -s "http://${REG}/v2/${repo}/tags/list" | jq -r '.tags[]'); do
    src="${REG}/${repo}:${tag}"
    dst="docker.io/${DOCKERHUB_USER}/${PREFIX}${svc}:${tag}"
    if docker pull -q "$src" >/dev/null && docker tag "$src" "$dst" && docker push -q "$dst" >/dev/null; then
      echo "$svc $tag $dst" | tee -a "$OUT"; n=$((n+1))
    else
      echo "FAILED $src" >&2; failed=$((failed+1))
    fi
  done
done
echo "published ${n} images, ${failed} failed (list: ${OUT})"
[ "$failed" -eq 0 ]
