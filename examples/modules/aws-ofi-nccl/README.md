# aws-ofi-nccl module

Two worked examples of publishing `aws-ofi-nccl-1.20.0` — the network plugin that bridges
NCCL to `libfabric`, enabling RDMA over Slingshot 11 — for two different deployment
situations, the same split as [../nccl/](../nccl/):

| | Use when | Source |
|---|---|---|
| [**pvc/**](pvc/) | The k8s cluster's nodes have the Lustre filesystem (`/projects/public/alma/...`) mounted, with aws-ofi-nccl already Spack-installed there | [PVC module](../../../docs/writing-modules.md#2-pvc-modules-prototyping): mounted read-only via `subPath`, no image to build |
| [**image/**](image/) | Lustre isn't mounted on the k8s nodes | [Image module](../../../docs/writing-modules.md#1-image-modules-preferred): built from source, only needing a one-off copy of the node's libfabric to build against |

Only one should exist in the cluster at a time. Both carry env values matching the
bare-metal `brics/aws-ofi-nccl` Lmod module on Isambard-AI (AlmaLinux / `linux-neoverse_v2`).

(Not a **host** module: aws-ofi-nccl isn't ldconfig-registered on the nodes, so
`/etc/ld.so.cache` discovery — the mechanism [libfabric-host](../libfabric-host/) uses —
doesn't find it there, even though it's reachable on Lustre. That mechanism is reserved
for libraries that must match the node's kernel/driver stack, which this isn't.)

## Dependency direction

This is the one detail that doesn't follow the same shape as `nccl`: neither variant here
`dependsOn` `nccl-2.29.2`. Both only need the node's `libfabric-host`, so this module works
standalone against whatever NCCL a pod's own image already provides (e.g. an NGC
container). `nccl` goes the other way — `nccl-2.29.2.dependsOn: [aws-ofi-nccl-1.20.0]` —
so that loading `nccl` can never end up without the RDMA plugin and silently fall back to
~2 GB/s TCP sockets. Loading `aws-ofi-nccl-1.20.0` alone is fine; loading `nccl` alone
pulls this in.

## pvc/

The claim (`spack-apps` in the example) must be in the modules namespace, `ReadOnlyMany`,
and listed in the chart's `nri.pvcs` — see
[writing-modules.md](../../../docs/writing-modules.md#2-pvc-modules-prototyping) for
setting that up.

Unlike `nccl`, the raw per-build install directory isn't enough on its own: aws-ofi-nccl's
RPATH-linked Spack deps (`libhwloc`, `libpciaccess`, `libxml2`, the gcc runtime, `xz`,
`libiconv`) live in separate hash-suffixed directories elsewhere in the tree, which aren't
visible once only this one `subPath` is mounted. `pvc/module.yaml` points `subPath` at a
merged Spack view instead — create it once with:

```bash
spack view symlink -i /projects/public/alma/views/aws-ofi-nccl-1.20.0 aws-ofi-nccl@1.20.0
```

which symlinks aws-ofi-nccl and its Spack deps into one `lib/` directory. Then apply:

```bash
kubectl apply -f pvc/module.yaml
```

## image/

Builds aws-ofi-nccl `v1.20.0-aws` from source. Unlike `nccl`, it still needs the node's
Cray libfabric (with the CXI provider) to *build* against — that's proprietary HPE
software with no package registry. On AlmaLinux it lives under the node's regular
`/usr/lib64` and `/usr/include`, regardless of whether Lustre is mounted, so assemble a
build-time prefix from those paths first:

```bash
cd image
mkdir -p libfabric-1.22.0/lib libfabric-1.22.0/include
cp /usr/lib64/libfabric.so* libfabric-1.22.0/lib/
cp -r /usr/include/rdma libfabric-1.22.0/include/   # fabric.h etc.; adjust if this differs
podman build --build-arg AWS_OFI_NCCL_TAG=v1.20.0 \
  -t ghcr.io/isambard-sc/modules/aws-ofi-nccl:1.20.0 .
podman push --digestfile digest ghcr.io/isambard-sc/modules/aws-ofi-nccl:1.20.0
cat digest    # sha256:…
```

(Isambard nodes are aarch64 natively, so no `--platform`/emulation needed; `podman-hpc`
is a drop-in for `podman` if building on-system.)

The built `.so` still dynamically links against `libfabric.so.1`/`libcxi.so.1` at
runtime, so `image/module.yaml` keeps `dependsOn: [libfabric-host]` too — building
against a copy doesn't remove that requirement.

`gdrcopy` (for `FI_HMEM_CUDA_USE_GDRCOPY`) is bundled as an optional extra; drop the
Dockerfile's gdrcopy build, the `deps/` `COPY`, and that env var together if you don't
want it.

Paste the resulting digest into `image/module.yaml`'s `source.image.reference`, then:

```bash
kubectl apply -f image/module.yaml
kubectl get module aws-ofi-nccl-1.20.0 -o jsonpath='{.status.conditions}'
```

## Tuning

`FI_CXI_DEFAULT_CQ_SIZE` and `FI_CXI_DEFAULT_TX_SIZE` are workload-dependent (see the
Isambard NCCL guide's environment variable reference) — the values here are the site
defaults; a pod can override either in its own env, which wins over the module's.
