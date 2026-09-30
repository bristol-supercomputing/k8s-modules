# Per-container modules with DRA — Design

Date: 2026-09-29
Options doc: [Per-container modules: options](https://claude.ai/code/artifact/7053fc68-c5ad-4d66-a4dc-e042d0a25b0e) (option B)
Spike: branch `spike/dra-extended-resources`, `spike/dra/README.md`
Builds on: [Phase 1 + 2](2026-09-25-module-system-phase1-2-design.md), [Phase 3 (NRI)](2026-09-25-module-system-phase3-nri-design.md)

Today a loaded module goes into every container in `spec.containers`, and a
pod that needs a host module must pick a node with a `nodeSelector`. This
change makes each module a shareable DRA device that a container requests as
an extended resource. As a result:

- modules can be chosen per container and per module;
- the scheduler places pods only on nodes where their modules can run;
- NRI no longer depends on pod annotations for anything that matters to
  security.

## Decisions

| Question | Decision |
|---|---|
| Keep the annotation-only NRI path? | No. DRA is the only path; `load` stays as a shortcut |
| Family defaults | Family extended resource `family.modules.isambard.ac.uk/<family>`, backed by a per-family DeviceClass |
| User-made ResourceClaims for module classes | Denied; only the scheduler's extended-resource claim may use them |
| Narrowing `load` | New `modules.isambard.ac.uk/load-containers` annotation |
| Trust boundary | CDI (only for allocated claims) does binds and the hook; NRI does only env and masks, from markers it may not trust |

## Scope

In scope:

- **Driver:** a DRA kubelet plugin inside `module-nri`, covering:
  - ResourceSlice publishing, with readiness;
  - prepare and unprepare;
  - CDI specs.
- **NRI:** reads `BRICS_MODULE_*` markers instead of the `resolved.*` and
  `containers` annotations.
- **Controller:** one DeviceClass per Module and one per family.
- **Admission:**
  - the mutating policy works per container, from limits, `load` and
    `load-containers`;
  - the validating policies now also check limit keys;
  - init containers may not request modules;
  - a new ResourceClaim guard.
- **Chart:** kubelet plugin mounts and RBAC.
- **Tests:** unit, envtest and kind e2e.

Out of scope:

- Modules for init containers and native sidecars. Rejected for now.
- Removing host node labels and `ModuleNodeReport`. Both are kept; the
  labels are no longer needed for scheduling.
- Real Isambard validation.

## User API

```yaml
containers:
- name: trainer
  resources:
    limits:
      modules.isambard.ac.uk/aws-ofi-nccl-1.18.0: 1   # exact module
      family.modules.isambard.ac.uk/libfabric: 1      # family default
- name: sidecar                                        # gets nothing
```

- **Exact modules:** an exact module uses `modules.isambard.ac.uk/<module name>`.
- **Family defaults:** a family default uses
  `family.modules.isambard.ac.uk/<family>`.
- **Why two domains:** `foo-1` can be both a family name and the module
  `foo` version `1`, and two DeviceClasses must not share an extended
  resource name. So families get their own domain.
- **Quantity:** always `1`. Requests may be left out, since they default to
  limits.
- **Closures:** a container gets the whole closure of each module it requests.

The `load` shortcut is unchanged: `modules.isambard.ac.uk/load: nccl,aws-ofi-nccl-1.18.0`.

- **What it does now:** the mutating policy writes the exact limit into each
  selected container. For a family name, that is the family's current default.
- **Which containers:** by default every container in `spec.containers`. With
  `modules.isambard.ac.uk/load-containers: trainer,worker`, only the
  containers listed.
- **Mixing with limits:** limits and `load` can both be used in one pod.

The pod-wide rules are unchanged:

- one version per family;
- namespace `access`;
- modules must be resolved;
- `env.set` conflicts are caught by the controller.

## Components

| Unit | Change |
|---|---|
| `internal/dra` (new) | device and slice building, readiness, per-container edit planning, CDI spec writing; the `kubeletplugin.DRAPlugin` implementation |
| `internal/nri` | `Adjuster` reads markers from `ctr.Env`; the annotation and catalogue checks are gone |
| `internal/resolve` | `DeviceName`, `MarkerKey`, `DeviceClassName`, `FamilyClassName` helpers |
| `cmd/module-nri` | starts the kubelet plugin next to the NRI stub; republishes slices when discovery or Modules change |
| `internal/controller` | new `DeviceClassReconciler` |
| `api/v1alpha1` | constants: driver name, the two resource domains, `load-containers`, marker prefix; removes `AnnotationContainers` |
| chart | policies, DaemonSet mounts, RBAC, the claim guard policy |

## Names

| Thing | Form | Example |
|---|---|---|
| Driver | `modules.isambard.ac.uk` | |
| Device | the Module name as a DNS label (the `VolumeName` rules without the `mod-` prefix: a hash suffix if the name was changed or is too long) | `nccl-2-29-2` |
| Device attributes | `module` (string), `family` (string), `default` (bool) | |
| Module DeviceClass | `<module>.modules.isambard.ac.uk`; selector `attributes.module == "<module>"`; `extendedResourceName: modules.isambard.ac.uk/<module>` | `nccl-2.29.2.modules.isambard.ac.uk` |
| Family DeviceClass | `<family>.family.modules.isambard.ac.uk`; selector `family == "<family>" && default == true`; `extendedResourceName: family.modules.isambard.ac.uk/<family>` | `nccl.family.modules.isambard.ac.uk` |
| Marker env | `BRICS_MODULE_<NAME>`; NAME is the Module name upper-cased with non-alphanumerics as `_`, plus a hash suffix if that mapping lost information | `BRICS_MODULE_NCCL_2_29_2` |
| Marker value | the Module's canonical `resolved.*` annotation value (`name;rank=N;src=…;mount=…[;lib=…][;mask=…]`) | |
| CDI device | `modules.isambard.ac.uk/module=<claim uid>-<request>` | |

Module names are DNS subdomains of at most 63 characters, so both DeviceClass
names are valid.

The family class's selector is static. When the default label moves, only the
driver's slices change (the `default` attribute), so the controller does not
need to watch labels.

## Data flow

```
Pod CREATE
  MAP (per Module param)  → image mounts + env.set on requesting containers,
                            exact limits for load, resolved.* + required-plugins
  VAPs                    → unknown / access / conflict / init containers / immutability
scheduler                 → extended-resource claim <pod>-extended-resources-<rand>,
                            one request per (container, resource); devices only
                            on nodes whose slice lists them
kubelet prepare           → driver: CDI spec per claim (binds, hook, markers)
containerd CreateContainer→ CDI edits applied, then NRI: LD_LIBRARY_PATH, masks,
                            BRICS_MODULES from markers
```

### Mutating policy

It runs once per Module param, as today.

**When a container requests the param.** A container `c` requests the param
if any of these holds:

- `c.resources.limits` has `modules.isambard.ac.uk/<name>`;
- the Module is the default and `c.resources.limits` has
  `family.modules.isambard.ac.uk/<family>`;
- `load` names the Module, or names its family and the Module is the default,
  and `c` is selected: `load-containers` is absent, or lists `c.name`.

**What the param applies.** When the param applies (resolved and namespace
allowed) and at least one container requests it:

- **pod volumes:** its closure's image volumes;
- **each requesting container:**
  - the closure's image volume mounts;
  - `env.set` entries the container doesn't already set;
  - if the request came from `load`, the exact limit and request `"1"` for
    this Module. The request is a plain string, since `quantity()` is
    rejected;
- **pod annotations:** its `status.resolved.annotations`, and
  `required-plugins.noderesource.dev/pod: ["modules"]`.

A container that doesn't request the param is left alone. The
`modules.isambard.ac.uk/containers` annotation is gone.

### Validating policies

- **`pods`:**
  - The requested list now also covers every container's limit keys in both
    domains, and the same "unknown" check applies to them: a family must
    have a `resolved.*/<family>` key, and an exact name a value that starts
    with `<name>;`.
  - `load-containers` must name only existing containers and must not be
    empty. It needs `load`.
  - New rule: module limits on `initContainers` are rejected.
  - `load-containers` joins the set of immutable module annotations. The
    `containers` rule is removed.
- **`pod-consistency`:** `isRequested` also counts limit keys, so the
  namespace access and mismatch checks cover limits.
- **`resourceclaims` (new):**
  - On CREATE of a `resource.k8s.io` ResourceClaim, any request (or
    `firstAvailable` subrequest) whose `deviceClassName` ends with
    `.modules.isambard.ac.uk` is denied, unless
    `request.userInfo.username == 'system:kube-scheduler'`.
  - A ResourceClaimTemplate that uses such a class is always denied.
  - This stops users getting a module through a claim of their own, which
    would bypass namespace access.
  - A second, pod-side policy (`podclasses`) rejects resource keys
    `deviceclass.resource.kubernetes.io/<class>.modules.isambard.ac.uk`
    (the implicit DeviceClass extended-resource names) in container,
    initContainer and pod-level limits and requests. Without it they reach
    the scheduler, whose claim the guard lets through, bypassing the pod
    policies. Independently, the driver's prepare refuses any claim that is
    not the pod's `status.extendedResourceClaimStatus.resourceClaimName`.

### Driver: slices

For every Module whose status is resolved, the driver publishes a device
only if every member of its closure (`status.resolved.modules`) passes this
check on the node:

- **image:** always OK;
- **host:** the latest discovery result is ready;
- **pvc:** the DaemonSet mounts the claim (`--pvcs`).

Details:

- **Device fields:** each device has `allowMultipleAllocations: true` and the
  three attributes. `default` comes from the Module's default label.
- **Pool and slices:** the pool is the node name, one pool per node, split
  into slices of at most 64 devices.
- **When it republishes:** on any Module add, update or delete (including
  label changes), and after each discovery run. Updates are debounced, and
  the kubeletplugin helper's `PublishResources` reconciles the slices.

### Driver: prepare

Given the scheduler's claim, prepare:

1. **Finds the pod** from `status.reservedFor` (a single pod) and reads
   `pod.status.extendedResourceClaimStatus.requestMappings`, which maps a
   request to a container. It returns a recoverable error if either is
   missing.
2. **Checks each result for this driver.** It looks up the device's Module
   and requires it to be resolved. For a family resource, the Module must be
   the one the pod's `resolved.modules.isambard.ac.uk/<family>` annotation
   names. Otherwise it fails with "default for family X changed from A to B
   after admission; recreate the pod".
3. **Plans each container.** It takes the union of the closures of the
   container's requested Modules. The marker value is the member's
   annotation value from the requesting Module's `status.resolved`. When a
   member appears in more than one closure, the value with the highest rank
   wins. It then builds one set of container edits:
   - **host members:** a bind for each `lib/` and `deps/` file
     (`ro,nosuid,nodev,rprivate`). If the node's discovery result is not
     ready, prepare fails;
   - **pvc members:** an rbind of the resolved host path;
   - **every member:** its marker env;
   - **host members with deps or a glibc floor:** one `createContainer`
     hook, `module-hook --deps … --glibc …`.
4. **Attaches the edits to one request.** They go to that container's
   lowest-index request. The container's other requests get CDI devices
   with no edits, so overlapping closures never produce duplicate mounts.
5. **Writes the CDI file** atomically:
   `/var/run/cdi/modules.isambard.ac.uk-<claim uid>.json`.

Unprepare removes that file. Both calls are idempotent.

### NRI

`CreateContainer` finds the `BRICS_MODULE_*` entries in `ctr.Env` and parses
each value with `ParseValue`. It then:

- sorts the modules by rank (highest first), then by name;
- prepends each module's `lib` dirs to `LD_LIBRARY_PATH`;
- adds tmpfs masks;
- sets `BRICS_MODULES`.

A container with no markers is left alone. A malformed marker fails the
container, so a broken driver can't start pods without their modules.

**Forged markers.** A user can forge a marker with `env`, `envFrom` or the
image's ENV. That only reorders the container's own library path and masks
its own paths. Binds and the hook come only from CDI, and CDI edits are
produced only for allocated claims.

### Controller

`DeviceClassReconciler` watches Modules. It also owns the DeviceClasses it
creates, which carry the label `modules.isambard.ac.uk/managed: "true"`.

- **Module classes:** each resolved Module gets its class. Unresolved or
  deleted Modules lose theirs.
- **Family classes:** each family with at least one Module gets its class.
  It is deleted when the last Module of that family goes.
- **Managed fields:** only the selector and the extended resource name.

## Error handling

| Case | Result |
|---|---|
| Unknown or unresolved module in limits or `load` | pod rejected at admission (existing messages) |
| Module not allowed in the namespace | rejected at admission |
| Host module not ready on any node | pod stays Pending: `FailedScheduling … cannot allocate all claims` |
| Host module becomes not ready after scheduling | prepare fails; the pod stays `ContainerCreating` with the driver's message |
| Family default moved between admission and scheduling | prepare fails with a "recreate the pod" message |
| Module limits on an init container | rejected at admission |
| User ResourceClaim for a module class | rejected at admission |
| NRI plugin not running | containerd refuses the pod (`required-plugins`, unchanged) |
| Driver not running on a node | no slice, so no pods with modules are scheduled there |

## Chart

- **DaemonSet volumes (hostPath):**
  - `/var/lib/kubelet/plugins/modules.isambard.ac.uk` (`DirectoryOrCreate`);
  - `/var/lib/kubelet/plugins_registry`;
  - `/var/run/cdi` (`DirectoryOrCreate`).
- **RBAC:**
  - **module-nri:**
    - ResourceSlices: create, get, list, watch, update, patch, delete;
    - ResourceClaims: get;
    - pods: get;
    - nodes: get.
  - **controller:** DeviceClasses: get, list, watch, create, update, patch,
    delete.
- **New `admission-resourceclaims.yaml`** holds the claim guard.
- **kind:** `nri.discoveryInterval` is lowered to `15s`, so the readiness
  e2e can run quickly.

## Testing

**Unit tests:**

- **`internal/dra`:** readiness per source type; slice chunking; the name
  helpers; per-container planning, which covers union and dedupe, the
  highest rank winning, the family check, and the lowest request getting the
  edits; CDI JSON.
- **`internal/nri`:** marker parsing, ordering, masks, malformed markers,
  and containers with no markers.

**envtest:**

- **Mutating policy:**
  - exact limits;
  - family limits;
  - `load`;
  - `load-containers`;
  - merging with existing CPU limits;
  - a container that requests nothing is left alone;
  - closure mounts on only the requesting container.
- **Validating policies:**
  - an unknown limit;
  - a limit on an init container;
  - a bad `load-containers`;
  - access through limits;
  - the claim guard.
- **Controller:** creating and deleting DeviceClasses.

**kind e2e:**

- The existing suite, ported to `load`, which keeps its old meaning.
- Per-container selection: a sidecar gets nothing.
- `load-containers`.
- A family limit.
- Overlapping closures in one container.
- A host module that isn't ready leaves the pod Pending, and the pod
  schedules once the library appears and the next discovery runs.
- A user ResourceClaim is rejected.
