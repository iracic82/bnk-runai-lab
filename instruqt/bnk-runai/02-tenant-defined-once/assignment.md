---
slug: tenant-defined-once
id: aeo17zofe3hu
type: challenge
title: Define a tenant once
teaser: Create two projects in Run:ai and watch BNK build a front door, a rate limit
  and a route for each, with nothing configured in BNK by hand.
notes:
- type: image
  url: ../assets/notes-02.png
tabs:
- id: nedmowgcjf6q
  title: Terminal
  type: terminal
  hostname: control
- id: ir7hyleyosgs
  title: Topology
  type: service
  hostname: control
  path: /topology.html
  port: 3002
- id: azt7df3mf1qv
  title: Run:ai UI
  type: service
  hostname: control
  port: 3001
- id: leroz7dq1bci
  title: Grafana
  type: service
  hostname: control
  path: /d/one-tenant-two-planes?orgId=1&kiosk
  port: 3000
- id: tquof3svfz5g
  title: Lab code
  type: code
  hostname: control
  path: /root/lab
difficulty: basic
timelimit: 1800
enhanced_loading: null
---
In Run:ai, a **project** is the tenant: it owns a GPU quota, a priority and a namespace. In this
challenge you create two projects and never touch BNK. The tenancy controller reads each project and
writes the BNK objects that serve it.

![From Run:ai project to BNK tenant](../assets/02-tenant-flow.png)

This is the point of the integration for a Run:ai team: the platform owner keeps defining tenants in
the tool they already use, and the network follows.

# Step 1: Create a tenant in the Run:ai UI

Start the way a Run:ai admin would. In the **Run:ai UI** tab (sign in as `admin@acme.lab` /
`Lab-Admin-1!`):

1. **Organization → Projects → NEW PROJECT**.
2. Department `default`, name `tenant-a`. Leave the namespace empty; Run:ai creates `runai-tenant-a`.
3. **GPU quota**: `2` GPUs on the `default` node pool.
4. **SAVE & CONTINUE** through the remaining steps, then **CREATE PROJECT**.

Now watch BNK follow. You have not touched BNK, and you will not:

```bash,run
until kubectl get projects.kai.resources tenant-a -o jsonpath='{.status.phase}' 2>/dev/null | grep -q Ready; do sleep 3; done
kubectl get projects.kai.resources tenant-a -o jsonpath='{.status.phase} {.status.namespace}{"\n"}'
sleep 20; kubectl get gateways -n f5-bnk -o wide
```

`tenant-a` has its own Gateway, programmed on its own address, about half a minute after you clicked
create.

Give it a high priority and create a second tenant from the terminal. `labctl` uses the same Run:ai
API the UI does:

```bash,run
labctl tenant create tenant-a --gpu 2 --priority 150
labctl tenant create tenant-b --gpu 0.5 --priority 50
```

`labctl` calls the **Run:ai REST API**, the same API the Run:ai UI and CLI use. Run:ai does not let
anyone write projects with `kubectl`: its admission webhook allows only Run:ai's own services, so the
API is the one front door for tenants. Here is the call `labctl` made, so nothing is hidden:

```bash,run
TOK=$(curl -s --cacert /etc/runai/ca.crt https://runai.lab/api/v1/token -H 'Content-Type: application/json' \
  -d '{"grantType":"password","username":"admin@acme.lab","password":"Lab-Admin-1!"}' | jq -r .accessToken)
curl -s --cacert /etc/runai/ca.crt https://runai.lab/api/v1/org-unit/projects -H "Authorization: Bearer $TOK" \
  | jq '.projects[] | {id, name, parentId, gpu: .resources[0].gpu.deserved}'
```

Run:ai then writes the cluster object, an ordinary `kai.resources/v1alpha1` Project. That object is
what the tenancy controller reads; it never writes to it:

```bash,run
kubectl get projects.kai.resources
kubectl get projects.kai.resources tenant-a -o yaml | sed -n '/^spec:/,$p'
```

Look at `spec.parent` (the department), `spec.queues[].resources.gpu.deserved` (the quota),
`spec.queues[].priority`, and `status.namespace`. The namespace is where the project's workloads run.
The controller reads it from the status and never guesses it from the name.

Open the **Run:ai UI** tab and sign in as `admin@acme.lab` / `Lab-Admin-1!`. Under
**Organization → Projects** you see both tenants with their GPU quotas. This is where a Run:ai admin
works every day; nothing about BNK appears here, and nothing needs to.

# Step 2: Put one model per tenant on the same GPU

Each tenant gets one model server that asks for half a GPU:

```bash,run
labctl model deploy tenant-a --fraction 0.5
labctl model deploy tenant-b --fraction 0.5
```

Where did the Run:ai scheduler put them?

```bash,run
for t in tenant-a tenant-b; do
  NS=$(kubectl get projects.kai.resources $t -o jsonpath='{.status.namespace}')
  kubectl get pods -n "$NS" -o wide -l app.kubernetes.io/part-of=model-server
  kubectl get pods -n "$NS" -l app.kubernetes.io/part-of=model-server \
    -o custom-columns='POD:.metadata.name,SCHEDULER:.spec.schedulerName,GPU-FRACTION:.metadata.annotations.gpu-fraction,QUEUE:.metadata.labels.runai/queue'
done
```

Both pods run on the `gpu` node, each with `gpu-fraction: 0.5`, placed by `runai-scheduler`, the Run:ai
scheduler. Two tenants now share one physical GPU. That is good for utilisation, and it is exactly why
the next challenge matters: whoever sends more requests gets more of that GPU, unless something stops
them first.

> The GPUs come from NVIDIA's fake-gpu-operator and the models are vLLM simulators. Scheduling and
> fractions are real Run:ai behaviour; GPU memory isolation is not something this lab can show.

# Step 3: See the BNK tenants that appeared

You did not create anything in BNK. Ask the controller what it built:

```bash,run
labctl tenant show tenant-a
```

The same objects with plain `kubectl`. Everything carries the label `tenancy.f5.com/project`:

```bash,run
kubectl get gateway -n f5-bnk -l tenancy.f5.com/project -o wide
NS=$(kubectl get projects.kai.resources tenant-a -o jsonpath='{.status.namespace}')
kubectl get httproute,inferencepools.inference.networking.k8s.io,f5-epps -n "$NS"
kubectl get f5-big-cne-irules,netpolicies -n f5-bnk -l tenancy.f5.com/project=tenant-a
```

Per tenant that is a **Gateway** with its own address (named after the project, so `tenant-a`),
an **HTTPRoute** and **InferencePool** in the tenant's namespace, an **F5EPP** Endpoint Picker, and a
**F5BigCneIrule** attached by a **NetPolicy**.

The iRule holds the rate limit. The controller computes it as 20 requests per second per GPU of quota:

```bash,run
for t in tenant-a tenant-b; do
  echo -n "$t limit: "
  kubectl get f5-big-cne-irules -n f5-bnk $t-limit -o jsonpath='{.spec.iRule}' | grep -o 'rl_n > [0-9]*'
done
```

`tenant-a` gets 40 (2 GPU), `tenant-b` gets 10 (0.5 GPU). The controller also derives a priority class
from the Run:ai rank (Medium 150 is Critical, Low 50 is Sheddable) and posts it as an event on the
Run:ai project:

```bash,run
kubectl get events -A --field-selector involvedObject.kind=Project,reason=BNKTenantReady \
  -o custom-columns='PROJECT:.involvedObject.name,MESSAGE:.message'
```

In this lab BNK enforces the rate limit; the priority class is recorded, not yet enforced.

# Step 4: Chat through each tenant's front door

```bash,run
labctl vip tenant-a
labctl chat tenant-a "Say hello in five words."
labctl chat tenant-b "Say hello in five words."
```

Every answer is tagged with the Run:ai project it was served for. Check the header on a raw request.
The address comes straight from the Gateway's status, which is where `labctl vip` reads it:

```bash,run
VIP=$(kubectl get gateway -n f5-bnk tenant-b -o jsonpath='{.status.addresses[0].value}')
curl -si http://$VIP:8000/v1/models | grep -i -E '^HTTP|x-bnk-tenant'
```

# Step 5: Change the quota in Run:ai, watch the limit follow

Give `tenant-b` a full GPU. Running `labctl tenant create` again updates the project through the Run:ai
API. You could do the same in the **Run:ai UI** tab: edit `tenant-b`, set GPU quota to 1, save.
Either way, Run:ai is the only place you change it:

```bash,run
labctl tenant create tenant-b --gpu 1 --priority 50
sleep 15
kubectl get f5-big-cne-irules -n f5-bnk tenant-b-limit -o jsonpath='{.spec.iRule}' | grep -o 'rl_n > [0-9]*'
```

The limit is now 20. Put it back to half a GPU for the next challenge:

```bash,run
labctl tenant create tenant-b --gpu 0.5 --priority 50
```

# Step 6: Delete a tenant, BNK forgets it

Create a throwaway tenant, check BNK built it, then delete it:

```bash,run
labctl tenant create tenant-c --gpu 0.25 --priority 10
sleep 10
kubectl get gateway,f5-big-cne-irules,netpolicies -n f5-bnk -l tenancy.f5.com/project=tenant-c
labctl tenant delete tenant-c
sleep 15
kubectl get gateway,f5-big-cne-irules,netpolicies -n f5-bnk -l tenancy.f5.com/project=tenant-c
```

The second listing is empty. When the Run:ai project goes, the controller removes every BNK object
labelled with it, and its address is held back for 15 minutes before any new tenant can get it. On
start-up it also sweeps for tenants whose project vanished while it was down. No front door is left
serving a tenant that no longer exists.

The controller's own view of all this:

```bash,run
kubectl -n bnk-runai-tenancy logs deploy/bnk-runai-tenancy --tail=20
```

Click **Check** when both tenants answer.
