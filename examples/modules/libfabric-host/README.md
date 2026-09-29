# libfabric-host module

Worked example of a [host module](../../../docs/writing-modules.md#3-host-modules-kernel-matched-libraries):
`libfabric`, `libcxi` and `libxpmem` must match the node's kernel and Slingshot 11 driver
stack, so unlike [nccl](../nccl/) and [aws-ofi-nccl](../aws-ofi-nccl/) — which each come in
a PVC and an image variant, since neither needs kernel/driver matching — this one has no
image variant at all: it can only be a host module (a PVC wouldn't help either; the point
isn't *where* the files live but that they must match this specific node). `module-nri`
finds these on each node, at the paths the `aws-ofi-nccl` image variant's Dockerfile
copies from by hand at build time — on AlmaLinux both `libfabric.so.1` and `libcxi.so.1`
now live under `/usr/lib64`.

There's nothing to build or push — no `Dockerfile`, no image reference. Just apply it:

```bash
kubectl apply -f module.yaml
```

## What `module-nri` does with it

On plugin start, every 10 minutes, and whenever this Module changes, each node:

1. Looks up `libfabric.so.1`, `libcxi.so.1` and `libxpmem.so.0` in its own
   `/etc/ld.so.cache`.
2. Walks their `DT_NEEDED` dependencies (honouring RPATH/RUNPATH/`$ORIGIN`), skipping
   glibc — the container always uses the image's own libc.
3. Binds the three roots into `lib/` (prepended to `LD_LIBRARY_PATH`) and everything else
   they pull in — on AlmaLinux this closure is large: `libcurl`, `libjson-c`, `libssl`/
   `libcrypto`, the Kerberos/LDAP/SASL stack, `libzstd`, `libnghttp2`, … — into `deps/`,
   added to the ld cache after the image's own libraries, so the image's copies win
   unless listed in `preferHost` (see `module.yaml`'s commented `libjson-c.so.5` note).

Check what a node actually found:

```bash
kubectl get modulenodereports
kubectl get modulenodereport <node> -o yaml
```

## Requirements this puts on pods

- A glibc-based image — not musl or distroless-static.
- A writable `/etc` — `readOnlyRootFilesystem: true` is not supported, since the OCI hook
  rebuilds the image's ld cache with `ldconfig`.

Schedule only onto nodes where discovery succeeded:

```yaml
spec:
  nodeSelector:
    modules.brics.ac.uk/host-libfabric: "true"
```

## Who depends on this

[`aws-ofi-nccl-1.20.0`](../aws-ofi-nccl/) has `dependsOn: [libfabric-host]` in both its
pvc and image variants — this is the only thing either variant needs from a host module;
everything else (the plugin itself, hwloc, gdrcopy) comes from the PVC or the image.
