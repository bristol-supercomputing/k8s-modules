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
  without its modules.

## Limits

- Pods loading `host` modules need a glibc-based image with `ldconfig` and
  a writable `/etc` (`readOnlyRootFilesystem: true` is not supported).
- Every claim used by a `pvc` module must be listed in `nri.pvcs` and exist
  in `modulesNamespace`.
- Pods that load modules carry `required-plugins.noderesource.dev/pod:
  '["modules"]'`, which cannot be combined with other required plugins.
