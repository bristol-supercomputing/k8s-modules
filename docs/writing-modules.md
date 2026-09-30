# Writing modules

A **Module** is a named, versioned bundle of shared libraries (plus optional
environment variables) that a pod can load. Modules are cluster-scoped
`modules.isambard.ac.uk/v1alpha1` resources that admins publish. Users
request them in a container's resource limits (or with a pod annotation
shortcut), the way they would run `module load` on Isambard.

This guide covers the three ways to provide a module's files:

| Source | Use it when | Files come from |
|---|---|---|
| [**Image volume**](#1-image-modules-preferred) (preferred) | Almost always: software built for containers, such as NCCL, aws-ofi-nccl or MPI | An OCI image, pinned by digest |
| [**PVC**](#2-pvc-modules-prototyping) | Prototyping and fast iteration: try a build before you package it | A ReadOnlyMany volume in the modules namespace |
| [**Host**](#3-host-modules-kernel-matched-libraries) | Libraries that must match the node's kernel or OS image, such as `libcxi`, the Cray `libfabric` or `libxpmem` | The node's own filesystem, found through `/etc/ld.so.cache` |

## How a module reaches a container

1. A container asks for a module in its resource limits. Only that container
   gets the module (and everything it depends on):

   ```yaml
   containers:
   - name: trainer
     resources:
       limits:
         modules.isambard.ac.uk/aws-ofi-nccl-1.18.0: 1   # an exact module
         family.modules.isambard.ac.uk/nccl: 1           # a family's default
   - name: sidecar                                        # gets no modules
   ```

   The quantity must be exactly `1`. The `load` annotation is a shortcut that
   adds exact limits to every container, or only to the containers that
   `load-containers` lists:

   ```yaml
   metadata:
     annotations:
       modules.isambard.ac.uk/load: aws-ofi-nccl-1.18.0,nccl   # a family name loads its default
       modules.isambard.ac.uk/load-containers: trainer          # optional
   ```

   `load-containers` needs `load`, must name containers that exist, and can't
   be changed after the pod is created.

2. **Admission** looks up each requested Module and its resolved dependency
   closure. For image modules, it adds an image volume and a read-only mount
   to the requesting container. A family request is rewritten to the exact
   default version. Admission also adds the module's `env.set` variables and
   annotations that record exactly what was loaded. It rejects the pod if a
   module is unknown, unresolved, not permitted in the namespace, or conflicts
   with another loaded module.
3. **The scheduler** places the pod only on nodes where every requested
   module, including its whole dependency closure, can run. A host module
   must have been discovered ready on the node, and a PVC module's claim must
   be mounted by `module-nri`. Host modules therefore no longer need a
   `nodeSelector`.
4. **module-nri**, the node plugin, is also a DRA kubelet plugin. It publishes
   one shareable device per ready Module on each node. When a container
   starts, it writes CDI edits that bind `pvc` and `host` module files and
   set `BRICS_MODULE_*` markers. Its NRI side then reads the markers,
   prepends every module's library directories to `LD_LIBRARY_PATH` (ahead of
   the image's own value, in rank order), sets `BRICS_MODULES` and applies
   `maskPaths`.

Behind the limits, the controller creates a DeviceClass for every Module and
for every family. Modules are only requested through the limits above: users
can't create their own ResourceClaims or ResourceClaimTemplates for module
device classes, and the implicit
`deviceclass.resource.kubernetes.io/<class>.modules.isambard.ac.uk` resource
names are rejected too.

Every module is mounted read-only at a fixed path:

```
/opt/modules/<family>/<version>
```

Init containers can't request modules (the pod is rejected), and ephemeral
containers don't get them.

A pod that requested a family default is tied to the version chosen at
admission. If the `default` label moves before the pod is scheduled, the pod
fails to start with `default for family X changed from A to B after admission;
recreate the pod`, so delete the pod and create it again.

## Fields common to every module

```yaml
apiVersion: modules.isambard.ac.uk/v1alpha1
kind: Module
metadata:
  name: nccl-2.29.2               # must be <family>-<version>
  labels:
    modules.isambard.ac.uk/family: nccl
    modules.isambard.ac.uk/default: "true"   # optional: a `nccl` family request picks this one (one per family)
spec:
  family: nccl                    # [a-z0-9-], at most 40 chars
  version: 2.29.2                 # at most 20 chars; keep it lowercase, since the name must be a DNS name
  source: { ... }                 # exactly one of image, pvc, host (below)
  dependsOn: [libfabric-host]     # exact Module names, never family names; at most 32
  env:
    prependPath:
      LD_LIBRARY_PATH: [lib]      # relative to the mount path; the only variable supported
    set:
      NCCL_DEBUG: WARN            # names must start with an allowed prefix (see below)
  maskPaths: [/opt/amazon]        # absolute image directories hidden under an empty tmpfs
  access:
    matchLabels: {team: hpc}      # namespace labels; omit to allow every namespace
```

Rules to keep in mind:

- **`spec` is immutable.** To change a module, publish a new version. To
  change which version a family request picks, move the
  `modules.isambard.ac.uk/default` label (the one mutable part).
- **`env.set` names must start with a prefix in `envPrefixAllowlist`.** The
  defaults are `NCCL_`, `FI_`, `CXIP_`, `UCX_` and `OMPI_`.
- **`env.set` cannot set these:** `LD_PRELOAD`, `LD_LIBRARY_PATH`, `LD_AUDIT`
  and `PATH`. `PATH` entries for binaries are not supported yet.
- **Conflicts:**
  - Two modules of the same family can't be loaded together.
  - Two modules can't set the same variable to different values.
  - Both are checked for dependencies too.
- **User env wins:** a variable the user sets explicitly in the container
  overrides the module's value.

After you apply a module, check that the controller resolved it:

```bash
kubectl get modules
kubectl get module nccl-2.29.2 -o jsonpath='{.status.conditions}'
```

A pod that loads a module whose `Resolved` condition is not `True` is
rejected with that condition's reason.

---

## 1. Image modules (preferred)

The module's files live in an OCI image, which kubelet mounts as a read-only
[image volume](https://kubernetes.io/docs/concepts/storage/volumes/#image).
Images are immutable and pinned by digest. They are cached per node and
pulled like any other image, so every pod on every node gets exactly the
same bytes.

### Build the image

The image is only a filesystem, with no OS and no entrypoint. Lay it out the
way the module should appear under `/opt/modules/<family>/<version>`:

```
lib/libnccl.so.2 -> libnccl.so.2.29.2
lib/libnccl.so.2.29.2
include/nccl.h            # optional: anything else you want visible
```

```dockerfile
# Dockerfile
FROM scratch
COPY lib/ /lib/
COPY include/ /include/
```

Build it for every node architecture, push it to a registry in the
cluster's `registryAllowlist` (default `registry.isambard/modules/`), and
record the digest:

```bash
# podman
podman build --platform linux/arm64 -t registry.isambard/modules/nccl:2.29.2 .
podman push --digestfile digest registry.isambard/modules/nccl:2.29.2
cat digest    # sha256:…

# or docker buildx, multi-arch
docker buildx build --platform linux/amd64,linux/arm64 --push \
  --metadata-file meta.json -t registry.isambard/modules/nccl:2.29.2 .
jq -r '."containerimage.digest"' meta.json
```

### Write the Module

```yaml
apiVersion: modules.isambard.ac.uk/v1alpha1
kind: Module
metadata:
  name: nccl-2.29.2
  labels:
    modules.isambard.ac.uk/family: nccl
    modules.isambard.ac.uk/default: "true"
spec:
  family: nccl
  version: 2.29.2
  source:
    image:
      # A tag is not accepted: the reference must end in @sha256:<digest>.
      reference: registry.isambard/modules/nccl@sha256:<64 hex digits>
  env:
    prependPath:
      LD_LIBRARY_PATH: [lib]
    set:
      NCCL_DEBUG: WARN
```

A module can build on others with `dependsOn`. For example, aws-ofi-nccl
needs NCCL and the host's libfabric:

```yaml
apiVersion: modules.isambard.ac.uk/v1alpha1
kind: Module
metadata:
  name: aws-ofi-nccl-1.18.0
  labels: {modules.isambard.ac.uk/family: aws-ofi-nccl}
spec:
  family: aws-ofi-nccl
  version: 1.18.0
  source:
    image:
      reference: registry.isambard/modules/aws-ofi-nccl@sha256:<digest>
  dependsOn: [nccl-2.29.2, libfabric-host]
  env:
    prependPath:
      LD_LIBRARY_PATH: [lib]
    set:
      NCCL_NET: "AWS Libfabric"
```

Loading `aws-ofi-nccl-1.18.0` loads the whole closure. In `LD_LIBRARY_PATH`,
each module comes ahead of the modules it depends on.

### Packaging software built elsewhere (e.g. Spack)

Spack builds record absolute RPATHs to their install prefix, such as
`/projects/…/nccl-2.29.2-<hash>/lib`. Inside the container those paths
don't exist, but that's harmless: the loader skips missing RPATH
directories and then searches `LD_LIBRARY_PATH`. What matters is that
every shared library the module needs is either:

- in the image itself, under one of its `prependPath` directories, or
- in a module it `dependsOn`.

Dependencies that aren't modules, such as hwloc or the gcc runtime, can be
copied into a second directory (e.g. `deps/`) and listed as well:
`LD_LIBRARY_PATH: [lib, deps]`. Text files that hard-code the original
prefix, such as configs and scripts, still point at the old path.

---

## 2. PVC modules (prototyping)

The module's files live on a ReadOnlyMany PersistentVolume, so you can
change them without building or pushing an image. That makes PVCs useful
for trying a build before you package it. Because the files can change
underneath running pods, prefer an image module once the build settles.

### Set up the claim (admin, once per volume)

The claim must be in the modules namespace (`modulesNamespace`, default
`module-system`), have the `ReadOnlyMany` access mode, and be listed in the
chart's `nri.pvcs`. The `module-nri` DaemonSet mounts every listed claim.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: spack-apps
  namespace: module-system
spec:
  accessModes: [ReadOnlyMany]
  storageClassName: <your RWX/ROX class, or "" with a pre-bound PV>
  resources: {requests: {storage: 100Gi}}
```

```bash
helm upgrade k8s-modules oci://ghcr.io/isambard-sc/charts/k8s-modules \
  --namespace module-system --reuse-values --set 'nri.pvcs={spack-apps}'
```

Only admins should be able to write the volume's contents. `subPath` is
resolved inside the volume every time a container is created, so a symlink
in the volume can't point outside it. But a writer could still swap a
directory for a symlink between that check and the bind.

### Write the Module

`subPath` is a directory inside the volume, relative and without `..`. It
is mounted at `/opt/modules/<family>/<version>`:

```yaml
apiVersion: modules.isambard.ac.uk/v1alpha1
kind: Module
metadata:
  name: nccl-2.30.0-rc1
  labels: {modules.isambard.ac.uk/family: nccl}
spec:
  family: nccl
  version: 2.30.0-rc1
  source:
    pvc:
      claimName: spack-apps
      subPath: linux-neoverse_v2/nccl-2.30.0-rc1-abcdef   # a directory in the volume
  env:
    prependPath:
      LD_LIBRARY_PATH: [lib]
```

If the claim is missing or isn't ReadOnlyMany, the Module stays
`Resolved=False/PVCInvalid`. If the claim isn't in `nri.pvcs`, the module
is not published on any node, so pods that request it stay `Pending`
(`pvc claim <name> is not mounted by module-nri; add it to nri.pvcs`).

---

## 3. Host modules (kernel-matched libraries)

Some libraries must match the node's kernel or OS image and can't be
shipped in an image, for example `libcxi`, the Cray `libfabric` and
`libxpmem` for Slingshot. A host module names only the **root** libraries.
`module-nri` finds them on each node and binds them into the container,
together with everything they depend on.

### Write the Module

```yaml
apiVersion: modules.isambard.ac.uk/v1alpha1
kind: Module
metadata:
  name: libfabric-host          # convention: version "host"
  labels:
    modules.isambard.ac.uk/family: libfabric
    modules.isambard.ac.uk/default: "true"
spec:
  family: libfabric
  version: host
  source:
    host:
      discover:
        roots: [libfabric.so.1, libcxi.so.1, libxpmem.so.0]   # sonames, not paths
      preferHost: [libjson-c.so.5]   # optional, see below
```

### What happens on each node

- **Discovery.** It runs at plugin start, every 10 minutes
  (`nri.discoveryInterval`), and whenever a host module is created or
  deleted:
  - Each root is looked up in the node's `/etc/ld.so.cache`.
  - Their `DT_NEEDED` dependencies are then walked, honouring RPATH,
    RUNPATH and `$ORIGIN`.
  - glibc (`libc`, `libm`, `libpthread`, `libdl`, `librt`, `ld-linux*`) is
    never included; the container uses the image's own glibc.
- **Split placement:**
  - Roots are bound into `<mount>/lib`, which is prepended to
    `LD_LIBRARY_PATH`.
  - Their dependencies (`libssl`, `libcurl`, `libz`, …) go into
    `<mount>/deps`. That directory is added to the image's ld cache after
    the image's own libraries, so the image's copies still win.
  - List a dependency in `preferHost` to force the host's copy instead.
- **glibc check.** If the closure needs a newer glibc than the image
  provides, the container fails with, for example,
  `module libfabric-host needs GLIBC_2.36; the image's libc.so.6 provides up to GLIBC_2.31`.

### Requirements on pods that load host modules

- A glibc-based image (not musl or distroless-static).
- A writable `/etc`, so `readOnlyRootFilesystem: true` is not supported.
  The OCI hook rebuilds the image's ld cache with its own trusted
  `ldconfig`; it never runs code from the image.

### Checking a node

Each node's plugin reports what it found:

```bash
kubectl get modulenodereports
kubectl get modulenodereport <node> -o yaml     # closure, hashes, glibc floor, missing sonames
```

Nodes where a host module's full closure was found also get a
`modules.isambard.ac.uk/host-<family>: "true"` label. The scheduler no longer
needs it: a pod that requests a host module is only placed on nodes where the
module is ready, so no `nodeSelector` is required. The labels and the reports
remain for inspection.

If a host module stops being ready after the pod is scheduled, the pod stays
`ContainerCreating` with `host module <name> is not available on node <node>:
<reason>`. If it isn't ready on any node, the pod stays `Pending` with a
`FailedScheduling` event.

---

## Common errors

| Message | Meaning |
|---|---|
| `module "X" was not loaded: not found, not resolved, not permitted in namespace "ns", or in conflict with another loaded module` | Check `kubectl get module X` and its conditions, and the module's `access.matchLabels` |
| `module "X" is not resolved: …` | A dependency is missing, there is a cycle, a conflict, or a bad PVC; the reason follows |
| `module "X" requires resolved.modules.isambard.ac.uk/<family>=…, but the pod has …` | Two loaded modules (or their dependencies) need different versions of one family, or set one variable differently |
| `image … is not in an allowed registry` | Push the module image to a registry in `registryAllowlist` |
| `env.set name X must start with one of …` | Rename the variable, or add its prefix to `envPrefixAllowlist` |
| `module "X" is already the default for family "f"` | Remove the default label from the old version first |
| `required plugin "modules" not present` | `module-nri` isn't running on that node; the pod fails closed rather than starting without its modules |
| `FailedScheduling … cannot allocate all claims` | No node can run every requested module: a host module isn't ready anywhere, or a PVC module's claim isn't in `nri.pvcs` |
| `default for family X changed from A to B after admission; recreate the pod` | The family's default label moved before the pod was scheduled; delete and recreate the pod |
