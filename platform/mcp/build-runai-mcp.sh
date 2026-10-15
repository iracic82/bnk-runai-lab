#!/usr/bin/env bash
# Fallback image for NVIDIA's runai-mcp-server, built from source at the same ref as the lab's Run:ai
# control plane, with NVIDIA's own Dockerfile and nothing changed. Only needed if the published
# nvcr.io/nvidia/runai/runai-mcp-server image (platform/versions.env RUNAI_MCP_IMAGE) does not work
# against our control plane. Run by a person with Docker Hub push rights; this script never runs in a lab.
#
# NVIDIA's Dockerfile copies a prebuilt bin/runai-mcp-server-<arch>; their Makefile builds it inside the
# monorepo (go.mod replaces point at ../control-plane and ../runai-clients). We build the same binary
# with the same flags in a golang container, then run their Dockerfile.
#
# Env: RUNAI_REF (default from versions.env), IMAGE (default docker.io/iracic82/runai-mcp-server:0.0.0),
#      WORK (clone dir), PUSH=1 to push after building, GO_IMAGE (default golang:1.27)
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "${HERE}/../versions.env"
RUNAI_REF="${RUNAI_REF:-59e288d7}"
IMAGE="${IMAGE:-docker.io/iracic82/runai-mcp-server:0.0.0}"
WORK="${WORK:-${TMPDIR:-/tmp}/run-ai-mcp-src}"
GO_IMAGE="${GO_IMAGE:-golang:1.27}"   # runai-mcp-server/go.mod at 59e288d7 says go 1.27.2
ARCH="${ARCH:-amd64}"
PKG=github.com/dsx-ai-factory/run-ai/runai-mcp-server

if [ ! -d "$WORK/.git" ]; then
  git clone --filter=blob:none --no-checkout https://github.com/dsx-ai-factory/run-ai.git "$WORK"
fi
git -C "$WORK" fetch -q origin "$RUNAI_REF" 2>/dev/null || true
git -C "$WORK" checkout -q "$RUNAI_REF"
SHA="$(git -C "$WORK" rev-parse HEAD)"
echo "building runai-mcp-server from dsx-ai-factory/run-ai@${SHA}"

# Same as NVIDIA's golang.mk build-go-amd: GOFIPS140=v1.0.0, version stamped via -ldflags.
docker run --rm -v "$WORK":/src -w /src/runai-mcp-server \
  -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH="$ARCH" -e GOFIPS140=v1.0.0 -e GOFLAGS=-mod=mod \
  "$GO_IMAGE" go build -trimpath \
    -ldflags "-X ${PKG}/internal/server.serverVersion=${IMAGE##*:}" \
    -o "bin/runai-mcp-server-${ARCH}" ./cmd/runai-mcp

docker build --platform "linux/${ARCH}" --build-arg TARGETARCH="$ARCH" \
  --label "org.opencontainers.image.source=https://github.com/dsx-ai-factory/run-ai" \
  --label "org.opencontainers.image.revision=${SHA}" \
  -t "$IMAGE" "$WORK/runai-mcp-server"
echo "built $IMAGE"

if [ "${PUSH:-0}" = 1 ]; then
  docker push "$IMAGE"
  echo "pushed $IMAGE; deploy it with RUNAI_MCP_IMAGE=$IMAGE bash platform/mcp/install.sh"
else
  echo "not pushed (PUSH=1 to push)"
fi
