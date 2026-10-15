---
slug: tokens-per-project
id: b4dv4wqs5rjq
type: challenge
title: Tokens per project
teaser: BNK counts tokens per Run:ai project at the edge, shows them next to GPU share,
  and writes the totals back onto the Run:ai project and its department.
notes:
- type: image
  url: ../assets/notes-04.png
tabs:
- id: yxo0y5hhhrnp
  title: Terminal
  type: terminal
  hostname: control
- id: 3fharece6pau
  title: Topology
  type: service
  hostname: control
  path: /topology.html
  port: 3002
- id: rcoyhpiujcvt
  title: Grafana
  type: service
  hostname: control
  path: /d/one-tenant-two-planes?orgId=1&kiosk&hideLogo&refresh=5s
  port: 3000
- id: xi47eeaxmw4y
  title: Run:ai UI
  type: service
  hostname: control
  port: 3001
- id: ejaxdecsy0g4
  title: Lab code
  type: code
  hostname: control
  path: /root/lab
difficulty: intermediate
timelimit: 1500
enhanced_loading: null
---
# Tokens per project

Run:ai knows how much GPU each project was given. It can't know how many tokens each project actually
used, because tokens live in requests and responses, and the scheduler never sees those.

BNK sees every request. So BNK can count them.

![Token metering from the BNK edge back to Run:ai](../assets/04-metering.png)

---

## Step 1 · Generate some tokens

```bash,run
labctl load steady --tenant tenant-a --seconds 45
labctl load steady --tenant tenant-b --seconds 30
labctl chat tenant-a "Explain GPU fractions in two sentences."
```

**In Grafana**, the **Tokens/s by department** panel at the top starts to climb while this runs.

---

## Step 2 · Where the count comes from

Every OpenAI-compatible model response carries a `usage` block. Look at one:

```bash,run
VIP=$(kubectl get gateway -n f5-bnk tenant-a -o jsonpath='{.status.addresses[0].value}')
MODEL=$(curl -s http://$VIP:8000/v1/models | jq -r '.data[0].id')
curl -s http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":16}" | jq .usage
```

As the response passes back through BNK, the tenant's iRule reads that block and books the tokens to
the Run:ai project and department that own the Gateway. The token meter then turns them into two
Prometheus counters:

| Metric | Labels |
|---|---|
| `bnk_tokens_total` | `project`, `department`, `model`, `direction` (prompt or completion) |
| `bnk_requests_total` | `project`, `department`, `model`, `code` |

```bash,run
kubectl get pods -n bnk-runai-tenancy
labctl dash
```

> A fair caveat: BNK does the counting, but the numbers are whatever the model reported. The models here
> are simulators, so the counts are synthetic. The path from response to Run:ai object is the real one.

---

## Step 3 · One tenant, two planes

Open the **Grafana** tab. The dashboard is called **One tenant, two planes**, and each project gets its own
row. Read a row from left to right:

| Panel | Plane | Source |
|---|---|---|
| **GPU: deserved vs allocated** | GPU | Run:ai |
| **Requests/s and 429s** | request | BNK |
| **Tokens/s** | tokens | BNK meter |
| **Tokens per GPU of quota** | the join of both | tokens ÷ the project's GPU share |
| **Tokens/s by department** (top row) | roll-up | BNK meter, summed by Run:ai department |

The last one, **Tokens per GPU of quota**, is the panel a platform team will care about. It answers a
question they get asked all the time: is `tenant-a`'s 2-GPU quota earning its keep compared with
`tenant-b`'s half GPU? Until now, answering that meant joining scheduler data with gateway logs by hand.

---

## Step 4 · The totals are reported against Run:ai

Only Run:ai's own services may write to a project, so the meter doesn't try. It keeps the running totals
in a usage ledger, and posts them as events on each Run:ai project and department, where any Run:ai
admin can see them:

```bash,run
kubectl -n bnk-runai-tenancy get cm bnk-runai-usage -o jsonpath='{.data}' | jq .
kubectl get events -A --field-selector reason=TokensMetered \
  -o custom-columns='KIND:.involvedObject.kind,NAME:.involvedObject.name,MESSAGE:.message' | tail -6
```

`project.tenant-a` and `project.tenant-b` are each project's running total, and `department.default`
is the two added together. Now check the ledger against the meter's own counters:

```bash,run
M=$(kubectl get node control -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
curl -s http://$M:9100/metrics | grep -E '^bnk_tokens_total' | grep -v 'model="none"'
```

**You should see:** a project's prompt and completion tokens add up to its ledger total, give or take
one 30-second write.

That ledger is what a chargeback script would read. It doesn't need to know anything about BNK or
Prometheus.

Click **Check** once tenant-a has a token total.
