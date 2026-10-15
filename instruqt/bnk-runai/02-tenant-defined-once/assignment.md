---
slug: tenant-defined-once
type: challenge
title: Define a tenant once
teaser: Create two projects in Run:ai and watch BNK build a front door, a rate limit
  and a route for each, with nothing configured in BNK by hand.
notes:
- type: image
  url: ../assets/notes-02.png
tabs:
- title: Terminal
  type: terminal
  hostname: control
# TODO(verify on a live sandbox): the Run:ai UI is served by ingress-nginx (hostNetwork) on the runai
# node with host rule runai.lab. Instruqt's service proxy sends its own Host header, so the ingress may
# answer 404 and Keycloak may redirect to https://runai.lab. If so, add a catch-all ingress rule or
# move the UI behind a NodePort service and point this tab at it.
- title: Run:ai UI
  type: service
  hostname: runai
  port: 443
  protocol: https
- title: Lab code
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

# Step 1: Create two Run:ai projects

`tenant-a` belongs to department `acme`, gets 2 GPUs and a high priority. `tenant-b` gets half a GPU and
a low priority.

```bash,run
labctl tenant create tenant-a --gpu 2 --priority 150 --department acme
labctl tenant create tenant-b --gpu 0.5 --priority 50
```

`labctl` is a thin wrapper. What it created are ordinary Run:ai objects, `kai.resources/v1alpha1`
Projects, cluster-scoped:

```bash,run
kubectl get projects.kai.resources
kubectl get projects.kai.resources tenant-a -o yaml | sed -n '/^spec:/,$p'
```

Look at `spec.parent` (the department), `spec.queues[].resources.gpu.deserved` (the quota),
`spec.queues[].priority`, and `status.namespace`. The namespace is where the project's workloads run.
The controller reads it from the status and never guesses it from the name.

Open the **Run:ai UI** tab (user `admin@acme.lab`, password `Lab-Admin-1!`). Under **Projects** you
will see both tenants with their quotas.

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
    -o custom-columns='POD:.metadata.name,SCHEDULER:.spec.schedulerName,GPU-FRACTION:.metadata.annotations.gpu-fraction,QUEUE:.metadata.labels.kai\.scheduler/queue'
done
```

Both pods run on the `gpu` node, each with `gpu-fraction: 0.5`, placed by `kai-scheduler`, the Run:ai
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
kubectl get gateway -n f5-bnk -l tenancy.f5.com/project
NS=$(kubectl get projects.kai.resources tenant-a -o jsonpath='{.status.namespace}')
kubectl get httproute,inferencepool,f5epp -n "$NS"
kubectl get f5-big-cne-irules,netpolicy -n f5-bnk -l tenancy.f5.com/project=tenant-a
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

`tenant-a` gets 40 (2 GPU), `tenant-b` gets 10 (0.5 GPU). The controller also records each project's
priority class on the Run:ai project event: 150 maps to Critical, 50 to Sheddable.

```bash,run
kubectl get events -A --field-selector involvedObject.kind=Project,reason=BNKTenantReady \
  -o custom-columns='PROJECT:.involvedObject.name,MESSAGE:.message'
```

In this release BNK enforces the rate limit; the priority class is recorded for the scheduling work
that follows.

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

Give `tenant-b` a full GPU. `labctl tenant create` applies the project, so running it again updates it
(you could also edit the quota in the Run:ai UI):

```bash,run
labctl tenant create tenant-b --gpu 1 --priority 50
sleep 5
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
kubectl get gateway,f5-big-cne-irules,netpolicy -n f5-bnk -l tenancy.f5.com/project=tenant-c
labctl tenant delete tenant-c
sleep 10
kubectl get gateway,f5-big-cne-irules,netpolicy -n f5-bnk -l tenancy.f5.com/project=tenant-c
```

The second listing is empty. The controller holds a finalizer on each Run:ai project, so the BNK
config is removed before the project is gone. There is no orphaned front door left serving a tenant
that no longer exists.

The controller's own view of all this:

```bash,run
kubectl -n bnk-runai-tenancy logs deploy/bnk-runai-tenancy --tail=20
```

Click **Check** when both tenants answer.
