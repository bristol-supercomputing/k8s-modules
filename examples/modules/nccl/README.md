# nccl module

Two worked examples of publishing `nccl-2.29.2`, for two different deployment
situations:

| | Use when | Source |
|---|---|---|
| [**pvc/**](pvc/) | The k8s cluster's nodes have the Lustre filesystem (`/projects/public/alma/...`) mounted, with NCCL already Spack-installed there | [PVC module](../../../docs/writing-modules.md#2-pvc-modules-prototyping): mounted read-only via `subPath`, no image to build |
| [**image/**](image/) | Lustre isn't mounted on the k8s nodes (e.g. before users are onboarded) | [Image module](../../../docs/writing-modules.md#1-image-modules-preferred): built from source, no dependency on the host filesystem |

Only one should actually exist in the cluster at a time — pick whichever matches your
environment. Both use the same name (`nccl-2.29.2`) and both carry the
`modules.brics.ac.uk/default` label, so pods load either one the same way:
`modules.brics.ac.uk/load: nccl` (or `nccl-2.29.2` explicitly).

(NCCL and aws-ofi-nccl aren't ldconfig-registered on the nodes, so a **host** module
— which discovers roots via `/etc/ld.so.cache` — isn't an option here the way it is for
[libfabric-host](../libfabric-host/); that mechanism is reserved for libraries that must
match the node's kernel/driver stack, which these aren't.)

## Full Slingshot 11 bandwidth is forced, not optional

NCCL on its own falls back to TCP sockets (~2 GB/s on an 8-GPU all-reduce instead of
~160 GB/s). Rather than leaving that as a "load this too if you remember," both variants
set `spec.dependsOn: [aws-ofi-nccl-1.20.0]`, so loading `nccl` always pulls in the RDMA
plugin. See [../aws-ofi-nccl/](../aws-ofi-nccl/) for that module (which comes in the same
two variants) and why the dependency only runs in this one direction — loading
`aws-ofi-nccl` alone is fine, loading `nccl` alone pulls it in.

## pvc/

The claim (`spack-apps` in the example) must be in the modules namespace, `ReadOnlyMany`,
and listed in the chart's `nri.pvcs` — see
[writing-modules.md](../../../docs/writing-modules.md#2-pvc-modules-prototyping) for
setting that up. Nothing to build; adjust `subPath` to the actual install directory
(`spack find -p nccl@2.29.2-1`) and apply:

```bash
kubectl apply -f pvc/module.yaml
```

## image/

Builds NCCL `v2.29.2-1` from source for GH200 (`sm_90`):

```bash
cd image
podman build --build-arg NCCL_TAG=v2.29.2-1 \
  -t ghcr.io/isambard-sc/modules/nccl:2.29.2 .
podman push --digestfile digest ghcr.io/isambard-sc/modules/nccl:2.29.2
cat digest    # sha256:…
```

(Isambard nodes are aarch64 natively, so no `--platform`/emulation needed; `podman-hpc`
is a drop-in for `podman` if building on-system.)

Paste the resulting digest into `image/module.yaml`'s `source.image.reference`, then:

```bash
kubectl apply -f image/module.yaml
kubectl get module nccl-2.29.2 -o jsonpath='{.status.conditions}'
```
