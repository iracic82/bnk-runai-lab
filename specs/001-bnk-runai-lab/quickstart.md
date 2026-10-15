# Quickstart: Validation Guide

## Prerequisites
- Linux VM, 16 vCPU / 64 GB / 200 GB (spike sizing)
- BNK 2.4 FAR key + JWT exported as `BNK_FAR_KEY`, `BNK_JWT` (never committed)
- `platform/versions.env` pinned

## Bring-up
```bash
platform/install.sh          # kind → BNK → KAI+RM → fake-gpu → observability → controller
labctl status                # all components Ready
```

## Validation scenarios (map to spec user stories)

| # | Run | Expected |
|---|---|---|
| V1 (US1) | `labctl tenant create prod-chat --gpu 2 --priority 150`; `labctl tenant create research --gpu 0.5 --priority 50`; `labctl tenant show prod-chat` | Bundle objects exist within 30 s, labelled with the project. `curl https://prod-chat.ai.acme.lab/v1/models` → 200 |
| V1b | `labctl tenant delete research` | Bundle gone within 30 s; endpoint stops answering |
| V2 (US2) | `labctl model deploy prod-chat chat --replicas 2 --fraction 0.5`; `labctl routing prod-chat rr; labctl load prefix-heavy`; then `epp` and repeat | Both pods on one fake GPU; EPP run TTFT p50 ≥30% lower or higher cache hit rate (SC-002) |
| V3 (US3) | `labctl load overload --tenant all` | KAI preempts research pods; research throttled or shed; prod p95 ≤1.5× baseline (SC-003) |
| V4 (US4) | open the dashboard after V2/V3 | `bnk_tokens_total` per project, `source` label visible |
| V5 (US5) | deploy `--version v2`; apply rewrite 90/10, then 0/100 | Observed split ±5%; zero client errors |
| V6 (US6) | `labctl verify c7` | Hub log shows distinct SNAT IP per tenant |

## Track validation
```bash
for c in c1 c2 c3 c4 c5 c6 c7; do instruqt/challenges/$c/solve && labctl verify $c; done   # ×3 (SC-006)
instruqt track test
```
