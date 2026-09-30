# k8s-modules

Installs the `Module` CRD, the module controller, the admission policies
that inject modules into pods, and the `module-nri` DaemonSet.

To publish modules once the chart is installed, see
[docs/writing-modules.md](../../../docs/writing-modules.md).

## Node requirements

- Kubernetes 1.36+ (MutatingAdmissionPolicy v1, image volumes, and DRA
  extended resources, which are beta and on by default in 1.36).
- containerd with CDI enabled (the default in containerd 2).
- containerd 2.x with NRI enabled and its default validator switched on,
  so pods that load modules fail closed when `module-nri` is not running:

  ```toml
  [plugins."io.containerd.nri.v1.nri".default_validator]
    enable = true
  ```

  Without the validator, a module pod on a node without the plugin starts
  without its modules. Leave the validator's `tolerate_missing_plugins_annotation`
  unset: any annotation it names would let a module pod opt out.

## Dynamic Resource Allocation

Pods request modules through resource limits
(`modules.isambard.ac.uk/<module>: 1` or
`family.modules.isambard.ac.uk/<family>: 1`), which the scheduler turns into
ResourceClaims. `module-nri` is also a DRA kubelet plugin, so the DaemonSet
mounts `/var/lib/kubelet/plugins/modules.isambard.ac.uk`,
`/var/lib/kubelet/plugins_registry` and `/var/run/cdi`; the chart does this for
you. The controller creates a DeviceClass per Module and per family.

- Users can't create ResourceClaims or ResourceClaimTemplates for module
  device classes, and the implicit
  `deviceclass.resource.kubernetes.io/<class>.modules.isambard.ac.uk` resource
  names are rejected. Only the scheduler's extended-resource claim is
  allowed. That claim is created by `system:kube-scheduler`, and the claim
  guard allows only that user. A distribution that runs the scheduler under
  another identity needs the policy in
  `templates/admission-resourceclaims.yaml` adjusted.
- Init containers can't request modules.
- The `modules.isambard.ac.uk/host-<family>` node labels and
  `ModuleNodeReport` still exist, but scheduling no longer needs them.
- `module-nri`'s RBAC allows writing ResourceSlices, but an admission policy
  (`templates/admission-resourceslices.yaml`) confines it to slices of the
  `modules.isambard.ac.uk` driver on its own node, taken from its node-bound
  service account token.

## Limits

- Pods loading `host` modules need a glibc-based image and a writable
  `/etc` (`readOnlyRootFilesystem: true` is not supported). module-hook
  rebuilds the image's ld cache with its own static `ldconfig`; it never runs
  code from the image.
- Every claim used by a `pvc` module must be listed in `nri.pvcs` and exist
  in `modulesNamespace`. Its contents must be writable only by admins: `subPath` is
  resolved inside the PV when a container is created, but a writer could
  still swap a directory for a symlink between that check and the bind.
- Pods that load modules carry `required-plugins.noderesource.dev/pod:
  '["modules"]'` and may not set any other `required-plugins.noderesource.dev`
  annotation (a container-scoped one would override it).

## Upgrading

Pods admitted before the module system moved to DRA carry the
`modules.isambard.ac.uk/containers` annotation and no ResourceClaim, so the
new `module-nri` can't give them their host and PVC modules. It refuses
their containers instead (`pod was admitted by an older module system`),
so they fail when a container restarts. After upgrading from such a
version, recreate every pod that loads modules, for example with
`kubectl rollout restart` on its workload.

## Uninstalling

`helm uninstall` leaves behind the DeviceClasses the controller created and
the CDI specs `module-nri` wrote under `/var/run/cdi` on each node. Remove
the DeviceClasses with:

```bash
kubectl delete deviceclass -l modules.isambard.ac.uk/managed=true
```

and, if needed, the files `/var/run/cdi/modules.isambard.ac.uk-*.json` on
each node.

## Troubleshooting

- Pods start but lack their `host` or `pvc` modules (no files under the
  module's mount, no `module-hook` run): containerd's CDI support is off.
  Check that `enable_cdi` is `true` and `cdi_spec_dirs` includes
  `/var/run/cdi` under `[plugins."io.containerd.cri.v1.runtime"]` (both are
  the default in containerd 2).
