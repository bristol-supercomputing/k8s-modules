# k8s-modules

Installs the `Module` CRD, the module controller, the admission policies
that inject modules into pods, and the `module-nri` DaemonSet.

## Node requirements

- Kubernetes 1.36+ (MutatingAdmissionPolicy v1, image volumes).
- containerd 2.x with NRI enabled and its default validator switched on,
  so pods that load modules fail closed when `module-nri` is not running:

  ```toml
  [plugins."io.containerd.nri.v1.nri".default_validator]
    enable = true
  ```

  Without the validator, a module pod on a node without the plugin starts
  without its modules. Leave the validator's `tolerate_missing_plugins_annotation`
  unset: any annotation it names would let a module pod opt out.

## Limits

- Pods loading `host` modules need a glibc-based image and a writable
  `/etc` (`readOnlyRootFilesystem: true` is not supported). module-hook
  rebuilds the image's ld cache with its own static `ldconfig`; it never runs
  code from the image.
- Every claim used by a `pvc` module must be listed in `nri.pvcs` and exist
  in `modulesNamespace`.
- Pods that load modules carry `required-plugins.noderesource.dev/pod:
  '["modules"]'` and may not set any other `required-plugins.noderesource.dev`
  annotation (a container-scoped one would override it).
