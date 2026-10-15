# How Run:ai gets into the lab

NVIDIA open-sourced the Run:ai **code** (dsx-ai-factory/run-ai), but it does not publish ready-made
images for it. Only 3 of about 50 images are public on `nvcr.io/nvidia/runai` (checked 2026-10-15),
so Run:ai has to be compiled once before any cluster can run it. NVIDIA's only build path is
`local-dev.sh`, which compiles everything and then deploys it on a throwaway Kind cluster.

We split that in two: a one-off **builder** that compiles and publishes the images, and the **lab**,
which only pulls them.

```
builder sandbox ──compile once──▶ Docker Hub (iracic82/<service>)
                                          │ pull
                                          ▼
 lab sandbox: real BNK 2.4 cluster ──▶ helm install Run:ai control plane + cluster chart
              (control-plane, dpu, gpu, runai nodes)
```

| | Builder sandbox | The real lab |
|---|---|---|
| Purpose | Compile Run:ai once, push the images to Docker Hub | What learners and the GTC demo use |
| Cluster | Throwaway Kind cluster inside one VM, only to prove the images start | Real kubeadm cluster with BNK 2.4 (control-plane, dpu, gpu, runai nodes) |
| Run:ai | Built from source with NVIDIA's `local-dev.sh` | Pulled ready-made from `iracic82/<service>`, nothing compiled |
| Lifetime | Thrown away once the images are pushed | Every lab sandbox |

## Builder

- Instruqt track: `instruqt/runai-image-build` (private, owner f5), on host image `f5/test`
  (Ubuntu 24.04, 200 GB disk).
- Script: `image-build/build-and-publish.sh` runs `local-dev.sh` (compile about 50 services, deploy on
  Kind with fake GPUs as a smoke test).
- `image-build/publish-images.sh` copies every `localhost:5001/runai-local/<service>:<tag>` image to
  `docker.io/iracic82/<service>:<tag>` and lists them in `/root/runai-images.txt`.
- Needs the Instruqt team secret `DOCKERHUB_TOKEN`. It reaches the script through the environment
  only.
- Disk is the reason for the host image: NVIDIA asks for 100 GB free. One build leaves about 44 GB
  of caches plus about 8 GB of images. Lab sandboxes only need the 8 GB of images.

Tags follow `local-dev.sh`: control plane `0.0.0`, cluster operator and hooks `0.0.0-dev`.

## Lab install

The lab installs the same two Helm charts `local-dev.sh` uses, with the image registry pointed at
Docker Hub instead of the local registry:

1. **Control plane** (`control-plane/control-plane-chart`, release `runai-backend`, namespace
   `runai-backend`) with `global.image.registry=docker.io/iracic82` (charts compose `<registry>/<service>:<tag>`, so images keep their built names).
2. **Register the cluster** through the control-plane API (Keycloak token, then
   `POST /v1/k8s/clusters`, then `GET /api/v1/clusters/<uuid>/cluster-install-info` for the
   bootstrap token). These are the same calls `local-dev.sh` makes in `provision_cluster_credentials`.
3. **Cluster chart** (`runai-cluster/cluster/runai-chart`, release `runai-cluster`, namespace
   `runai`) with the cluster UUID, the bootstrap token and the Docker Hub registry. It brings the
   Run:ai Scheduler and resource management (`kai.resources` projects) with it.
4. **fake-gpu-operator** on the gpu node for simulated GPUs.

The tenancy controller (`controller/`) then watches the Run:ai projects that step 3 creates and
writes one BNK tenant per project.
