#!/usr/bin/env bash
# One-off: compile open-source NVIDIA Run:ai from source with NVIDIA's own local-dev.sh, prove it
# deploys (Kind + fake GPUs), then copy every image from its local registry to Docker Hub so lab
# sandboxes pull ready-made images instead of compiling.
#
# Runs on a throwaway amd64 Linux VM (16 vCPU, 64 GB, 150 GB disk). Needs:
#   DOCKERHUB_USER, DOCKERHUB_TOKEN   push credentials (token never echoed)
#   RUNAI_REF                         git ref of dsx-ai-factory/run-ai to build
# Output: docker.io/${DOCKERHUB_USER}/runai-oss-<service>:<tag>, plus /root/runai-images.txt
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
RUNAI_REF="${RUNAI_REF:-59e288d7}"
DOCKERHUB_USER="${DOCKERHUB_USER:-iracic82}"
PREFIX="${IMAGE_PREFIX:-runai-oss-}"
WORK=/root/runai-build
LOG=/root/runai-build.log
step() { echo; echo "==== $(date -u +%H:%M:%S) $*"; }

[ -n "${DOCKERHUB_TOKEN:-}" ] || { echo "DOCKERHUB_TOKEN is not set" >&2; exit 1; }

step "packages and docker (containerd image store, as local-dev.md asks on Linux)"
apt-get update -qq
apt-get install -y -qq ca-certificates curl git make python3 openssl jq >/dev/null
if ! command -v docker >/dev/null; then
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{ "features": { "containerd-snapshotter": true } }
EOF
systemctl restart docker
sysctl -w fs.inotify.max_user_watches=524288 fs.inotify.max_user_instances=8192 >/dev/null

step "clone dsx-ai-factory/run-ai @ ${RUNAI_REF}"
if [ ! -d "$WORK/.git" ]; then
  git clone -q https://github.com/dsx-ai-factory/run-ai.git "$WORK"
fi
git -C "$WORK" checkout -q "$RUNAI_REF"

step "build + deploy with NVIDIA local-dev.sh (this is the long part)"
cd "$WORK"
echo "cwd=$(pwd)"; ls -la "$WORK/local-dev.sh"
PARALLEL_JOBS="$(nproc)" RUNAI_ADMIN_PASSWORD='Lab-Admin-1!' bash "$WORK/local-dev.sh" 2>&1 | tee "$LOG"

step "smoke check: Run:ai pods"
kubectl get pods -A --no-headers | awk '{print $4}' | sort | uniq -c

step "copy images from the local registry to docker.io/${DOCKERHUB_USER}"
printf '%s' "$DOCKERHUB_TOKEN" | docker login -u "$DOCKERHUB_USER" --password-stdin >/dev/null
: > /root/runai-images.txt
for repo in $(curl -s http://localhost:5001/v2/_catalog?n=1000 | jq -r '.repositories[]' | grep '^runai-local/'); do
  svc="${repo#runai-local/}"
  for tag in $(curl -s "http://localhost:5001/v2/${repo}/tags/list" | jq -r '.tags[]'); do
    src="localhost:5001/${repo}:${tag}"
    dst="docker.io/${DOCKERHUB_USER}/${PREFIX}${svc}:${tag}"
    docker pull -q "$src" >/dev/null
    docker tag "$src" "$dst"
    docker push -q "$dst" >/dev/null
    echo "$svc $tag $dst" | tee -a /root/runai-images.txt
  done
done
docker logout >/dev/null

step "done: $(wc -l < /root/runai-images.txt) images published"
