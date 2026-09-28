# Kubernetes Module System — Phase 3 (NRI) Design

Date: 2026-09-25
Parent design: [Kubernetes Module System — Design Plan](https://claude.ai/code/artifact/a487684a-c66e-44ae-a7d3-fca6f0f4c189)
Builds on: [Phase 1 + 2 design](2026-09-25-module-system-phase1-2-design.md)

Phase 3 adds the runtime half of the module system: a node-level NRI plugin
and an OCI hook. Together they do what admission cannot. Admission can't see
the image's environment, the node's libraries or the image's filesystem; the
plugin and hook can.

## Scope

In scope:

- The `module-nri` NRI plugin (a DaemonSet):
  - `LD_LIBRARY_PATH` prepend and `BRICS_MODULES`;
  - `maskPaths`;
  - host-module discovery and binds;
  - PVC binds;
  - the catalogue check;
  - fail-closed through the NRI validator;
  - a per-node `ModuleNodeReport`.
- The `module-hook` OCI hook: it places host-module dependencies in the
  image's ld cache and runs the glibc check.
- API additions:
  - the `ModuleNodeReport` kind;
  - `spec.source.host.preferHost`.
- Admission additions:
  - the `modules.brics.ac.uk/containers` and `required-plugins.noderesource.dev/pod`
    annotations;
  - the report-writer policy.
- Controller addition: node labels from reports.
- Unit, envtest and kind e2e tests using stub host libraries.

Out of scope:

- Real Slingshot libraries and `fi_pingpong` (the parent's Isambard gate).
- `PATH`-style entries (parent open question 6).
- Scheduling by the new node labels.
- Spack image pipeline and CXI driver cut-over (parent phases 4 and 5).

**Known limit:** host modules need a glibc-based image with a writable
`/etc`, because their dependencies are added to the image's ld cache. The
image does not need its own `ldconfig`. Pods with
`readOnlyRootFilesystem: true` that load a host module fail with a clear
message.

## Components

| Unit | Kind | Responsibility |
|---|---|---|
| `cmd/module-nri` | binary | NRI plugin (index `50`, name `modules`); runs discovery; writes the node report |
| `cmd/module-hook` | static binary | OCI `createContainer` hook: ld cache update and glibc check |
| `internal/discovery` | package | parse `ld.so.cache`, walk `DT_NEEDED`, place libraries, hash the closure |
| `internal/nri` | package | CreateContainer adjustments, catalogue check, PVC host-path resolution |
| `internal/hook` | package | rootfs-relative ld conf writing, glibc comparison (`ldconfig` behind an interface) |
| `api/v1alpha1` | types | `ModuleNodeReport`; `HostSource.PreferHost` |
| `internal/controller` | reconciler | new `NodeLabelReconciler`: reports → node labels |

### Deployment

The DaemonSet `module-nri` runs in `modulesNamespace` (a pod can only mount
PVCs in its own namespace) and tolerates all
taints, so it runs on every node. Its init container copies `module-hook` to
the host directory `nri.hookDir`, which defaults to `/opt/brics-modules/bin`.
The main container mounts:

| Mount | Source | Purpose |
|---|---|---|
| `/var/run/nri/nri.sock` | hostPath | NRI connection |
| `/host` | hostPath `/`, read-only, `HostToContainer` propagation | discovery and `/host/proc/1/mountinfo` |
| `/pvcs/<claim>` | each PVC in `nri.pvcs`, read-only | PVC module sources |

It gets its node name from the downward API (`spec.nodeName`). It runs
privileged, because it must read host mount tables and library files, and it
uses a service account whose token carries the node-name claim.

Plugin RBAC is a ClusterRole with:

- `get/list/watch` on `modules`;
- `get/create/update/patch` on `modulenodereports` and
  `modulenodereports/status`;
- `get` on `nodes`, used to set the report's owner reference.

Controller RBAC gains `get/list/watch` on `modulenodereports` and
`get/list/watch/patch` on `nodes`.

### Helm values added

```yaml
nri:
  image: {repository: ghcr.io/bristol-supercomputing/module-nri, tag: ""}
  hookDir: /opt/brics-modules/bin
  pvcs: []                  # claim names in modulesNamespace to mount
  discoveryInterval: 10m
  resources: {}
```

`module-nri` and `module-hook` ship in one image. The image is multi-binary,
and its init container runs `cp /module-hook <hookDir>/`.

## API

### `spec.source.host.preferHost`

`preferHost` is an optional list of up to 16 sonames. Each must match the root
soname pattern. A listed dependency is placed in `lib/` (prepended) instead of
`deps/`. The field falls under the existing spec immutability.

### `ModuleNodeReport`

`ModuleNodeReport` is cluster-scoped, in group `modules.brics.ac.uk/v1alpha1`,
and named after its node. Its owner reference is the Node, so it is deleted
with the Node. It has a status subresource and no spec.

```yaml
status:
  discoveredAt: 2026-09-25T18:00:00Z
  modules:                                  # one per host Module in the catalogue, sorted by name
  - module: fakefabric-host
    family: fakefabric
    ready: true
    closureHash: sha256:<64 hex>
    glibcRequired: "2.34"                   # "" when nothing versioned from glibc is needed
    lib:  [{soname: libfakefabric.so.1, path: /usr/lib/brics-test/libfakefabric.so.1.0.0}]
    deps: [{soname: libfakedep.so.1,    path: /usr/lib/brics-test/libfakedep.so.1.0.0}]
    missing: []
    message: ""
```

- `path` is the real host path, with symlinks resolved.
- `ready` is true when every root and every dependency resolved.
- `message` explains the first problem found, for example
  `root libcxi.so.1 not in /etc/ld.so.cache` or
  `libfabric.so.1 needs libnl-3.so.200: not found`.
- The report is patched only when its content changes. `discoveredAt` also
  changes then.

## Discovery (`internal/discovery`)

Discovery runs at plugin start and every `discoveryInterval`. It covers each
Module with `source.host` in the plugin's watch cache, and it also runs
whenever a host Module is created or deleted. All host paths are read under
`/host`.

1. **Cache.** Parse `/host/etc/ld.so.cache` in the `glibc-ld.so.cache1.1`
   format. Keep entries whose flags match the node's architecture (x86-64 or
   AArch64 with the ELF libc6 type). The result maps soname → path. If several
   entries share a soname, the first one in cache order wins, as with `ld.so`.
2. **Roots.** Resolve each `discover.roots` soname through the cache. A root
   that isn't found goes into `missing`, and the module is not ready.
3. **Walk.** Read each library with `debug/elf`. For each `DT_NEEDED` entry
   not already visited, search in this order:
   1. `DT_RPATH`, only if the object has no `DT_RUNPATH`;
   2. `DT_RUNPATH`;
   3. the cache;
   4. `/lib64`, `/usr/lib64`, `/lib`, `/usr/lib`, and
      `/lib/<multiarch>` and `/usr/lib/<multiarch>` for the node architecture.

   `$ORIGIN` and `${ORIGIN}` expand to the host directory of the object being
   read. `$LIB` and `$PLATFORM` are not supported: an entry containing them is
   skipped.
4. **Path-like names.** A `DT_NEEDED` entry containing `/` (or `.`/`..`)
   is a path to `ld.so`, not a soname. The module is not ready, because the
   entry would otherwise become a bind destination outside `<mount>/deps`.
5. **Exclusions.** `libc.so.6`, `libm.so.6`, `libpthread.so.0`, `libdl.so.2`,
   `librt.so.1` and any `ld-linux*` are never included or walked. Host glibc
   must never enter a container.
6. **glibc floor.** Over the closure, take the highest `GLIBC_2.x` in any
   `.gnu.version_r` entry whose library is one of the excluded glibc sonames.
   `DynamicVersionNeeds` provides these entries. Compare versions
   numerically, component by component.
7. **Containment.** Symlinks are resolved inside `/host`. A resolved path
   outside `/host`, or one that isn't a regular file, is an error, and the
   module is not ready.
8. **Placement.** Roots and `preferHost` entries go to `lib`; everything else
   goes to `deps`. Both lists are sorted by soname.
9. **Hash.** `closureHash` is the SHA-256 of lines of the form
   `lib|deps <soname> <path> <size>`, sorted, joined by `\n`.

A discovery failure for one module never stops the others. The report and the
plugin's in-memory state are replaced together, atomically.

## CreateContainer (`internal/nri`)

The plugin acts only on containers whose pod carries at least one
`resolved.modules.brics.ac.uk/*` annotation and whose name appears in the
comma-separated `modules.brics.ac.uk/containers` annotation. Other containers
pass through unchanged.

### 1. Catalogue check

For each `resolved.modules.brics.ac.uk/<family>` annotation with value `v`,
take the module name `n` from before the first `;`. Then all of these must
hold:

- Module `n` exists in the watch cache;
- `n`'s `status.resolved.annotations[<same key>] == v`.

Otherwise the container fails with
`module annotation resolved.modules.brics.ac.uk/<family>=<v> does not match the Module catalogue (not found, not resolved, or changed); the pod must be recreated`.
Containerd gives each plugin 2 seconds per request, so this check reads only
the cache.

### 2. Parse

The fields come from the phase-2 value format: `rank`, `src`, `mount`, `lib`,
`mask`. The catalogue check has already verified them.

### 3. Adjust

Modules are handled in descending rank, then by name.

- **`LD_LIBRARY_PATH`:**
  - The new value is the concatenation of every module's `lib` directories,
    in that order, followed by the container's current value.
  - The current value is the first `LD_LIBRARY_PATH=` in the container's
    env, which is the image `ENV` merged with the pod env.
  - Duplicates and empty entries are removed.
  - The variable is set only when at least one module has a `lib` field.
- **`BRICS_MODULES`:** module names in the same order, joined by `:`.
- **`mask`:** for each path, a `tmpfs` mount with options
  `ro,nosuid,nodev,noexec,size=0`.
- **`src=host`:**
  - The plugin looks up the module's entry in the current discovery state.
    If the module is not ready, or has no entry, the container fails with
    `host module <n> is not available on node <node>: <message>`.
  - For each `lib` entry it binds the file to `<mount>/lib/<soname>`, and for
    each `deps` entry to `<mount>/deps/<soname>`. Bind options are
    `bind,ro,nosuid,nodev,rprivate`, and the source is the host `path`.
  - The module is recorded for the hook.
- **`src=pvc:<claim>/<subPath>`:**
  - The claim must be in `nri.pvcs`. Otherwise the container fails with
    `pvc claim <claim> is not mounted by module-nri; add it to nri.pvcs`.
  - The plugin resolves `subPath` inside its own mount of the claim with
    `fsutil.ResolveIn`. A symlink in the PV therefore resolves inside the
    PV, and one that climbs out is an error. The result must be a
    directory.
  - It binds `<hostPath(claim)>/<resolved subPath>` to `<mount>` with
    `rbind,ro,nosuid,nodev,rprivate`.
- **Hook:** if any host module was recorded, the plugin adds one
  `createContainer` hook:

  ```
  <hookDir>/module-hook --deps <mount>/deps [--deps …] --glibc <module>=<ver> [--glibc …]
  ```

  A `--glibc` argument is omitted when that module's `glibcRequired` is
  empty.

### PVC host path

At start, and whenever a claim is first used, the plugin resolves each
`/pvcs/<claim>` mount in two steps:

1. In `/proc/self/mountinfo`, find the entry whose mount point is
   `/pvcs/<claim>`. Take its `major:minor` and `root`.
2. In `/host/proc/1/mountinfo`, find an entry with the same `major:minor`
   whose `root` is a prefix of ours.
   - First prefer mounts under our own pod's
     `/var/lib/kubelet/pods/<uid>/`; the pod UID comes from the downward API.
     Shared-device volumes also appear under other pods' directories, and
     those can be unmounted at any time.
   - Then prefer the longest root. The host
   path is that entry's mount point joined with the remainder of our root.

If no entry matches, the claim is unusable, and the container fails with the
message above, prefixed with `cannot locate host path for`.

## OCI hook (`internal/hook`, `cmd/module-hook`)

The runtime runs the hook as a `createContainer` hook. That means it runs in
the container's mount namespace, after the mounts are set up and before
`pivot_root`. It runs with host-root privileges, on an image rootfs the user
controls. So it follows two rules:

- **It never executes code from the image.** `ldconfig` is a trusted,
  statically linked binary shipped in the `module-nri` image. `install-hook`
  copies it next to `module-hook`.
- **Every rootfs access goes through Go's `os.Root`.** The kernel then
  confines symlink resolution to the rootfs, with no gap between resolving a
  path and opening it.

Steps:

1. Read the OCI state JSON from stdin, and read `<bundle>/config.json`. The
   rootfs is `root.path`, joined to `bundle` when relative.
2. Write `etc/ld.so.conf.d/zz-brics-modules.conf`.
   - First it lists the image's trusted directories that exist (`/lib64`,
     `/usr/lib64`, `/lib`, `/usr/lib`), then one `--deps` directory per
     line.
   - `ldconfig` scans the trusted directories last. Without them first, a
     library the image keeps only in `/usr/lib` would lose to the host copy
     (verified in a spike). Multiarch directories are already listed by the
     image's earlier `conf.d` files.
   - Whatever the image already has at that name is removed and the file
     recreated with `O_EXCL`, so an image symlink is never followed.
   - If `etc/ld.so.conf` has no `include ld.so.conf.d/*.conf` line, append
     one.
   - A permission or read-only error fails with
     `host modules need a writable /etc in the image (readOnlyRootFilesystem is not supported): <err>`.
   - Any other error, such as a symlink escaping the rootfs, fails with
     `writing the image's ld config: <err>`.
3. Run `<hookDir>/ldconfig -X -r <rootfs>`, with a 30 s timeout.
   `ldconfig` chroots into the rootfs itself, and `-X` leaves the image's
   library symlinks alone.
4. glibc check, run when any `--glibc` argument was given:
   - Open `libc.so.6` through the root, searching `/lib64`, `/usr/lib64`,
     `/lib/<multiarch>`, `/usr/lib/<multiarch>`, `/lib` and `/usr/lib`, in
     that order.
   - From its `DynamicVersions`, take the highest `GLIBC_2.x` it defines.
   - For each `--glibc m=v` where `v` is greater than the image's version,
     fail with
     `module <m> needs GLIBC_<v>; the image's libc.so.6 provides up to GLIBC_<have>`.
   - If there is no `libc.so.6`, fail with
     `image has no glibc libc.so.6; host modules require a glibc-based image`.

The hook writes errors to stderr and exits 1, which fails the container.

## Admission additions (chart)

- **MutatingAdmissionPolicy** (`modules.brics.ac.uk`): wherever it applies a
  Module, it also sets two annotations:
  - `modules.brics.ac.uk/containers`, set to
    `object.spec.containers.map(c, c.name).join(',')`;
  - `required-plugins.noderesource.dev/pod`, set to `["modules"]`.
- **`pods.modules.brics.ac.uk`:**
  - **CREATE:** if any `resolved.*` annotation is present, `containers` must
    equal the joined container names, and `required-plugins.noderesource.dev/pod` must
    equal `["modules"]`.
  - **CREATE:** if any `resolved.*` annotation is present, no other
    `required-plugins.noderesource.dev*` key may appear. NRI's validator
    prefers the `/container.<name>` key and the bare key over `/pod`, so
    either one would let the pod start without the plugin.
  - **UPDATE:** both keys, and every `required-plugins.noderesource.dev*`
    key, join the immutable set. Their matchCondition also covers them.
- **New ValidatingAdmissionPolicy `module-reports.modules.brics.ac.uk`:**
  - It matches `modulenodereports` and `modulenodereports/status` on CREATE
    and UPDATE.
  - If `request.userInfo.extra` has `authentication.kubernetes.io/node-name`,
    then `object.metadata.name` must equal that value.
  - Requests without that claim, such as admins or the controller, are not
    restricted by this policy. RBAC limits who can write at all.

## Controller addition

A `NodeLabelReconciler` watches `ModuleNodeReport` objects and Nodes. For
each node it computes the desired label set: `modules.brics.ac.uk/host-<family>: "true"`
for every report module with `ready: true`. It then patches the Node's labels.
It adds missing labels and removes stale `modules.brics.ac.uk/host-*` labels,
and it never touches any other label. A node with no report loses all
`host-*` labels.

## Containerd requirement

Nodes must enable NRI's default validator:

```toml
[plugins."io.containerd.nri.v1.nri".default_validator]
  enable = true
```

Without it, `required-plugins.noderesource.dev/pod` has no effect, and a module pod on a
node without the plugin starts without its modules. The kind config sets this
through `containerdConfigPatches`, and the chart's README states the
requirement.

## Failure modes

| Condition | Effect |
|---|---|
| Plugin not connected on a node | Module pods fail with `required plugin "modules" not present`; kubelet retries; other pods unaffected |
| Annotation doesn't match catalogue | Container fails; the pod must be recreated |
| Host module not ready on the node | Container fails, naming the missing sonames; the node lacks the `host-<family>` label |
| Image glibc too old | Hook fails, naming the module and both versions |
| Image without `ldconfig` or `libc.so.6`, or with a read-only `/etc`, loading a host module | Hook fails with the matching message |
| PVC claim not in `nri.pvcs` | Container fails, naming the Helm value to set |
| Module created seconds before the pod | Catalogue miss; container fails and succeeds on kubelet retry |

## Testing

### Fixtures

`hack/gen-testdata.sh` builds the fixtures in a Debian bookworm container
with podman, and commits them to `internal/discovery/testdata` and
`internal/hook/testdata`:

- `libfakedep.so.1`;
- `libfakefabric.so.1`, which needs `libfakedep.so.1` and `libc.so.6`, and
  calls `arc4random()` so that it requires `GLIBC_2.36`;
- `liborigin.so.1`, with `RUNPATH=$ORIGIN/sub`, and `sub/libsubdep.so.1`;
- a fake `libc.so.6` whose version script defines `GLIBC_2.2.5` up to
  `GLIBC_2.31`;
- an `ld.so.cache` produced by `ldconfig -C` over the fixture directory,
  with the fixture root recorded so tests can map it.

### Unit tests (run on macOS; pure Go)

- **`discovery`:**
  - cache parsing, including architecture filtering and first-entry-wins;
  - the walk: RUNPATH vs RPATH order, `$ORIGIN`, visited-set cycles, the
    glibc exclusions and the glibc floor;
  - `preferHost` placement;
  - a missing root and a missing dependency;
  - a symlink escaping `/host`;
  - hash stability.
- **`hook`:**
  - the conf file and `include` line written inside a temporary rootfs;
  - `ldconfig` lookup and runner invocation, using a fake runner;
  - the glibc comparison against the fixture `libc.so.6`;
  - read-only `/etc`, no `ldconfig` and no `libc`.
- **`nri`:**
  - `CreateContainer` against a fake catalogue and discovery state: env
    order and dedupe, `BRICS_MODULES`, mask mounts, host binds, PVC binds,
    and hook args;
  - init containers (not listed) skipped;
  - forged, stale and unknown annotations;
  - a host module not ready;
  - a claim not configured;
  - PVC host-path resolution from fixture `mountinfo` pairs.

### envtest

- The `ModuleNodeReport` CRD.
- The report-writer policy, via client impersonation with the node-name
  extra: the writer for node A is refused on B's report and allowed on A's.
- The controller adds and removes `host-*` labels from reports, and leaves
  other labels alone.
- The mutating policy sets `containers` and `required-plugins`. The pods
  policy rejects a wrong value on CREATE and any change on UPDATE.
- The `preferHost` CRD validation.

### kind e2e (build tag `e2e`)

`setup.sh` additions:

- Enable the default validator.
- Build `test/images/host-libs` and copy its `.so` files into each node's
  `/usr/lib/brics-test`. Add `/etc/ld.so.conf.d/brics-test.conf` and run the
  node's `ldconfig`.
- Build and push:
  - the `module-nri` image;
  - `fakefabric-client`: Debian bookworm plus the binary `fakefabric-check`,
    linked against `libfakefabric.so.1`, which prints the `libfakedep`
    variant it loaded. The image has its own variant `image` of
    `libfakedep.so.1` in `/usr/lib`.
  - `fakefabric-client-old`: the same binary on Debian buster (glibc 2.28).
- Create a `hostPath` PV (`ReadOnlyMany`, `/var/lib/brics-test/pvc`, with
  `lib/libpvc.so` inside) and the PVC `spack-apps` in `module-system`.
  Install the chart with `nri.pvcs={spack-apps}`.
- Create the Modules `fakefabric-host` (roots `[libfakefabric.so.1]`) and
  `fakefabric-prefer` (the same, with `preferHost: [libfakedep.so.1]`).

Tests:

1. **Image module:** a pod loading `nccl-2.29.2` sees `LD_LIBRARY_PATH`
   starting with `/opt/modules/nccl/2.29.2/lib` and
   `BRICS_MODULES=nccl-2.29.2`.
2. **Host module:** a `fakefabric-client` pod loading `fakefabric-host` runs
   `fakefabric-check` successfully. It prints the variant `image`, so the
   image's own dependency won. `ldconfig -p` in the container lists
   `/opt/modules/fakefabric/host/deps/libfakedep.so.1`.
3. **`preferHost`:** with `fakefabric-prefer` loaded, it prints `host`.
4. **glibc:** a `fakefabric-client-old` pod loading `fakefabric-host` fails
   with `module fakefabric-host needs GLIBC_2.36`. The stub
   `libfakefabric.so.1` calls `arc4random()`, which glibc added in 2.36, so
   it requires `GLIBC_2.36`. Debian buster has 2.28, and the node's bookworm
   has 2.36.
5. **Mask:** a Module with `maskPaths: [/opt/masked]` hides a file that the
   test image has there.
6. **PVC:** a Module with `pvc: {claimName: spack-apps, subPath: .}` exposes
   `/opt/modules/<family>/<version>/lib/libpvc.so`, read-only.
7. **Fail-closed:** the test patches the DaemonSet so it runs on no node.
   A module pod then reaches `CreateContainerError` with `required plugin`,
   while a plain pod runs. The test then restores the DaemonSet.
8. **Report and label:** every node's report shows `fakefabric-host` ready
   with an identical `closureHash`, and every node carries
   `modules.brics.ac.uk/host-fakefabric=true`.

## Gate

On kind v1.36.4 with containerd 2.3, all unit, envtest and e2e tests above
pass, together with the phase-1 and phase-2 suites.

## Open questions carried forward

- Parent questions 1–4, 6 and 7.
- **Discovery rate:** should discovery be triggered by package upgrades
  through inotify on `/etc/ld.so.cache`, instead of the fixed interval?
- **Read-only rootfs:** should host modules support `readOnlyRootFilesystem`
  pods? That would need the hook to generate an ld cache on a tmpfs mount
  over `/etc/ld.so.cache` instead of editing the image's.
