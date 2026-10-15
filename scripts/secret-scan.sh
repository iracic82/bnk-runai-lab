#!/usr/bin/env bash
# Fails if staged (default) or all tracked files contain BNK licence tokens, F5 registry keys,
# cloud keys or private keys. Usage: scripts/secret-scan.sh [--all]
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
if command -v gitleaks >/dev/null 2>&1; then
  if [ "${1:-}" = "--all" ]; then exec gitleaks detect --no-banner --redact; fi
  exec gitleaks protect --staged --no-banner --redact
fi
if [ "${1:-}" = "--all" ]; then files=$(git ls-files); else files=$(git diff --cached --name-only --diff-filter=ACM); fi
[ -z "$files" ] && exit 0
patterns=(
  'eyJ[A-Za-z0-9_-]{20,}\.eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}'   # JWT (BNK licence)
  '"private_key"\s*:\s*"-----BEGIN'                                  # GCP / FAR service-account key
  '-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----'
  'AKIA[0-9A-Z]{16}'                                                 # AWS access key id
  'gh[pousr]_[A-Za-z0-9]{36,}'                                       # GitHub token
)
hit=0
for p in "${patterns[@]}"; do
  if echo "$files" | xargs grep -nIE -- "$p" 2>/dev/null; then hit=1; fi
done
if [ "$hit" = 1 ]; then echo "secret-scan: possible secret above; commit blocked" >&2; exit 1; fi
