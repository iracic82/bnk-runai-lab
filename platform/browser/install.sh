#!/usr/bin/env bash
# A browser inside the lab for the Run:ai UI. The UI lives at https://runai.lab behind ingress-nginx on the
# runai node, and its Keycloak login redirects to that name, which Instruqt's own browser cannot resolve.
# This container runs on the control node (which resolves runai.lab and trusts the lab CA) and serves
# Firefox over HTTP on :3001 for the Instruqt "Run:ai UI" tab.
set -euo pipefail
IMAGE="${BROWSER_IMAGE:-lscr.io/linuxserver/firefox:1157.0build1-1xtradeb1.2404.1-ls128}"
DOMAIN="${RUNAI_DOMAIN:-runai.lab}"
RUNAI_IP="$(kubectl get node runai -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"

command -v docker >/dev/null || { apt-get update -qq && apt-get install -y -qq docker.io >/dev/null; }
systemctl enable --now docker >/dev/null 2>&1 || true

install -d -m 755 /opt/lab-browser/policies
# Firefox opens the Run:ai UI and trusts the lab CA (copied by platform/runai/install.sh).
cat > /opt/lab-browser/policies/policies.json <<POL
{"policies":{
  "Homepage":{"URL":"https://${DOMAIN}","StartPage":"homepage"},
  "Certificates":{"Install":["/lab-ca/ca.crt"],"ImportEnterpriseRoots":true},
  "DisableFirstRunPage":true,"OverrideFirstRunPage":"","OverridePostUpdatePage":"","DontCheckDefaultBrowser":true,
  "DisableTelemetry":true,"NoDefaultBookmarks":true,"DisplayBookmarksToolbar":"never",
  "UserMessaging":{"WhatsNew":false,"ExtensionRecommendations":false,"FeatureRecommendations":false,"SkipOnboarding":true}
}}
POL

docker rm -f lab-browser >/dev/null 2>&1 || true
docker run -d --name lab-browser --restart unless-stopped \
  --shm-size 1g \
  --add-host "${DOMAIN}:${RUNAI_IP}" \
  -e PUID=0 -e PGID=0 -e TZ=Etc/UTC -e TITLE="Run:ai UI" \
  -e FIREFOX_CLI="https://${DOMAIN}" \
  -v /etc/runai/ca.crt:/lab-ca/ca.crt:ro \
  -v /opt/lab-browser/policies/policies.json:/usr/lib/firefox/distribution/policies.json:ro \
  -p 3001:3000 \
  "$IMAGE" >/dev/null

for i in $(seq 1 60); do
  curl -fsS -o /dev/null http://127.0.0.1:3001/ && { echo "  lab browser ready on :3001 -> https://${DOMAIN}"; exit 0; }
  sleep 5
done
echo "  lab browser did not answer on :3001" >&2; exit 1
