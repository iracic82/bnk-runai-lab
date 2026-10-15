---
slug: dynamo-on-runai
type: challenge
title: NVIDIA Dynamo on Run:ai, behind BNK
teaser: Run the real Dynamo frontend and KV-aware router on simulated GPUs. Run:ai places
  the workers, BNK serves the front door.
notes:
- type: image
  url: ../assets/notes-05-dynamo.png
tabs:
- title: Terminal
  type: terminal
  hostname: control
- title: Lab code
  type: code
  hostname: control
  path: /root/lab/platform/dynamo
- title: GPU node
  type: terminal
  hostname: gpu
difficulty: intermediate
timelimit: 2700
enhanced_loading: null
---
NVIDIA Dynamo is a serving layer for large models. Its frontend takes OpenAI-style requests and its
router picks a worker, preferring the one that already holds the prompt's KV cache. Here Dynamo runs
as one more Run:ai project. Run:ai decides where its workers sit on the GPUs. BNK gives the project its
front door.

![Dynamo on Run:ai behind BNK](../assets/05-dynamo-flow.png)

What is real and what is simulated:

| Real | Simulated |
|---|---|
| Dynamo frontend, KV-aware router, etcd and NATS | **Mocker**, Dynamo's own simulated engine, instead of vLLM |
| Run:ai project, queue, scheduler and GPU fractions | The GPUs (fake-gpu-operator) |
| BNK 2.4 Gateway, VIP, route and rate limit | The DPU (Dynamo does not use it here) |

Mocker is part of Dynamo. It registers like a real worker, keeps a simulated KV cache, publishes KV
events to the router and returns generated tokens. It loads only the tokenizer, not model weights.

The **Lab code** tab shows the manifests. There is no Dynamo operator here: plain Deployments, so you
can read every flag.

# 1. Deploy Dynamo as a Run:ai project

The script creates Run:ai project `dynamo` with 1 GPU of quota, waits for Run:ai to give it a namespace
and for BNK to give it a VIP, then starts etcd, NATS, the Dynamo frontend and two Mocker workers.
The first run pulls the Dynamo image, so give it a few minutes.

```bash,run
bash /root/lab/platform/dynamo/deploy.sh
```

Load the namespace and VIP into your shell:

```bash,run
source /root/lab/platform/dynamo/env.sh
```

# 2. See Run:ai place the workers

Every pod asks for the Run:ai scheduler and joins the `dynamo` queue. The two workers each ask for
half a GPU.

```bash,run
kubectl -n $NS get pods -o custom-columns='POD:.metadata.name,NODE:.spec.nodeName,SCHEDULER:.spec.schedulerName,QUEUE:.metadata.labels.kai\.scheduler/queue,GPU:.metadata.annotations.gpu-fraction'
```

The Run:ai project and its queue. One GPU deserved, two half-GPU workers placed against it:

```bash,run
kubectl get projects.kai.resources dynamo -o jsonpath='{.spec.queues[0].resources.gpu}{"\n"}'
kubectl get queues.scheduling.run.ai -o wide | grep -E 'NAME|dynamo'
```

Run:ai packs fractions onto real (here simulated) GPU devices. On the **GPU node** tab you can list the
fake A10s the operator exposes:

```bash,run
kubectl get node gpu -o jsonpath='{.status.allocatable.nvidia\.com/gpu}{" GPUs on gpu node\n"}'
```

# 3. Send requests through BNK

The tenancy controller turned the project into a BNK tenant. Its Gateway holds the VIP, and
`dynamo-route` sends the OpenAI paths to the Dynamo frontend:

```bash,run
kubectl get gateways.gateway.networking.k8s.io -n f5-bnk dynamo
kubectl -n $NS get httproutes.gateway.networking.k8s.io
```

Ask the frontend which models its workers registered:

```bash,run
curl -s http://$VIP:8000/v1/models | jq .
```

A chat request through the VIP. `nvext.extra_fields` asks Dynamo to report which worker served it, and
`-i` shows BNK's `X-BNK-Tenant` header:

```bash,run
curl -si http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' -d '{
  "model": "Qwen/Qwen3-0.6B",
  "messages": [{"role": "user", "content": "Hello from the BNK front door"}],
  "max_tokens": 16,
  "nvext": {"extra_fields": ["worker_id"]}
}'
```

The text is filler from Mocker. The path is real: BNK VIP, Dynamo frontend, router, worker, back.

# 4. Watch the KV-aware router

The frontend runs with `--router-mode kv`. It tracks which worker holds which prompt blocks and sends
a repeated prefix back to the worker that has it cached.

Send ten different prompts. They spread across both workers:

```bash,run
for i in $(seq 1 10); do
  curl -s http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' -d "{
    \"model\": \"$MODEL\", \"max_tokens\": 8, \"nvext\": {\"extra_fields\": [\"worker_id\"]},
    \"messages\": [{\"role\": \"user\", \"content\": \"Prompt number $i $RANDOM about something new\"}]}" \
  | jq -r '.nvext.worker_id.decode_worker_id'
done | sort | uniq -c
```

Now one long shared prefix, ten times. The router keeps choosing the worker that already holds it:

```bash,run
DOC=$(printf 'Our refund policy says the following. %.0s' $(seq 1 60))
for i in $(seq 1 10); do
  curl -s http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' -d "{
    \"model\": \"$MODEL\", \"max_tokens\": 8, \"nvext\": {\"extra_fields\": [\"worker_id\"]},
    \"messages\": [{\"role\": \"user\", \"content\": \"$DOC Question $i?\"}]}" \
  | jq -r '.nvext.worker_id.decode_worker_id'
done | sort | uniq -c
```

You can also pin a request to one worker with the `x-dynamo-worker-instance-id` header. Take an ID from
the output above:

```bash,run
WID=$(curl -s http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"max_tokens\":4,\"nvext\":{\"extra_fields\":[\"worker_id\"]},\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" \
  | jq -r '.nvext.worker_id.decode_worker_id')
echo "pinning to worker $WID"
for i in 1 2 3; do
  curl -s http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' \
    -H "x-dynamo-worker-instance-id: $WID" \
    -d "{\"model\":\"$MODEL\",\"max_tokens\":4,\"nvext\":{\"extra_fields\":[\"worker_id\"]},\"messages\":[{\"role\":\"user\",\"content\":\"pinned $i $RANDOM\"}]}" \
    | jq -r '.nvext.worker_id.decode_worker_id'
done
```

The router and worker logs show the same story from the inside:

```bash,run
kubectl -n $NS logs deploy/dynamo-frontend --tail=20
kubectl -n $NS logs -l app=dynamo-decode --prefix --tail=10
```

# 5. Optional: disaggregated serving

Dynamo can split a request into a prefill stage and a decode stage on different workers. This swaps the
two aggregated workers for one prefill and one decode worker, still half a GPU each:

```bash,run
bash /root/lab/platform/dynamo/deploy.sh disagg
kubectl -n $NS get pods -l dynamo.role=worker -o custom-columns='POD:.metadata.name,STAGE:.metadata.labels.dynamo\.stage,SCHEDULER:.spec.schedulerName,GPU:.metadata.annotations.gpu-fraction'
```

Now the response names both workers:

```bash,run
curl -s http://$VIP:8000/v1/chat/completions -H 'Content-Type: application/json' -d "{
  \"model\": \"$MODEL\", \"max_tokens\": 8, \"nvext\": {\"extra_fields\": [\"worker_id\"]},
  \"messages\": [{\"role\": \"user\", \"content\": \"split me into prefill and decode\"}]}" | jq .nvext
```

Go back to the aggregated setup before you click **Check**:

```bash,run
bash /root/lab/platform/dynamo/deploy.sh agg
```

# Recap

- Dynamo ran as an ordinary Run:ai project. Run:ai scheduled its workers on GPU fractions inside the
  project's quota.
- BNK gave the project its front door without any Dynamo-specific setup: the same Gateway, VIP and rate
  limit every Run:ai project gets. A plain HTTPRoute hands requests to the Dynamo frontend.
- Dynamo's router chose the worker by KV cache. BNK picks the tenant and enforces its limits; Dynamo
  picks the GPU worker inside it.
- F5 has announced that BNK uses Dynamo runtime signals for inference-aware routing. This lab does not
  show that feature: it routes to Dynamo with Gateway API kinds verified on BNK 2.4.

Click **Check** to finish.
