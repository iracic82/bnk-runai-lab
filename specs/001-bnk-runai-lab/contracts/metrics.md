# Contract: Metrics

## Primary (if BNK 2.4 can count tokens at the gateway; decided in spike S0)
```
bnk_tokens_total{project, department, model, direction="prompt|completion", source="gateway"}  counter
bnk_requests_total{project, department, model, code}                                            counter
```

## Fallback (always available)
```
bnk_requests_total{project, department, model, code}                 # from BNK stats/logs
bnk_tokens_total{project, department, model, direction, source="model-server"}
   # derived: vllm:prompt_tokens_total / vllm:generation_tokens_total
   # summed by pod namespace → project
```
The Grafana panel shows the `source` label, and the lab text explains it (Principle VI).

## Controller
```
bnk_tenant_state{project, state}                 gauge (1 for current state)
bnk_tenant_reconcile_seconds{project}            histogram
```

## Dashboards (one board: "One Tenant, Two Planes")
Each row is per tenant, with columns: GPU deserved vs allocated (KAI) | req/s and
throttled (BNK) | TTFT p50/p95 | tokens/s | KV-cache hit rate.
