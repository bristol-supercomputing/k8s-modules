# k8s-modules

An Lmod-style module system for Kubernetes. Admins publish versioned
`Module` resources (`modules.isambard.ac.uk/v1alpha1`); users request them
per container in resource limits:

```yaml
spec:
  containers:
  - name: trainer
    resources:
      limits:
        modules.isambard.ac.uk/aws-ofi-nccl-1.18.0: 1   # an exact module
        family.modules.isambard.ac.uk/nccl: 1           # a family's default
  - name: sidecar                                        # gets no modules
```

or, as a shortcut for every container (or those `load-containers` lists),
with an annotation:

```yaml
metadata:
  annotations:
    modules.isambard.ac.uk/load: aws-ofi-nccl-1.18.0
```

- **module-controller** resolves each Module's dependency closure into its
  `status` and creates a DeviceClass per Module and per family.
- **Admission policies** (a MutatingAdmissionPolicy and several
  ValidatingAdmissionPolicies, no webhook) inject image volumes, mounts and
  env into the requesting containers, and reject conflicting, forged or
  unpermitted requests.
- **module-nri**, a DaemonSet, is two node plugins:
  - a DRA kubelet plugin that publishes the Modules ready on its node, so
    the scheduler only places pods where their modules can run, and writes
    CDI edits that bind host libraries (with split placement and a glibc
    check through the `module-hook` OCI hook) and PVC modules;
  - an NRI plugin that orders the `LD_LIBRARY_PATH` prepend, applies masks
    and makes pods fail closed when the plugin is absent.

**Writing modules:** [`docs/writing-modules.md`](docs/writing-modules.md)
covers image modules (preferred), PVC modules (prototyping) and host modules
(kernel-matched libraries), with worked examples.

Designs: [`docs/superpowers/specs/`](docs/superpowers/specs/).

## Install

```bash
helm install k8s-modules oci://ghcr.io/isambard-sc/charts/k8s-modules \
  --version <chart version> --namespace module-system --create-namespace
```

Nodes need Kubernetes 1.36+ and containerd 2.x with CDI (on by default) and
NRI's default validator enabled; see [the chart README](deploy/helm/k8s-modules/README.md).

Images: `ghcr.io/isambard-sc/module-controller` and
`ghcr.io/isambard-sc/module-nri`. They are built by
[`build.yaml`](.github/workflows/build.yaml):
- `main` and `sha` tags on every push to `main`;
- `X.Y.Z` and `X.Y` tags for `vX.Y.Z` git tags;
- `pr-N` tags for same-repo PRs.

[`release-chart.yaml`](.github/workflows/release-chart.yaml) publishes the
chart whenever the version in `Chart.yaml` changes. The chart's default
image tag is its `appVersion`, so tag `v<appVersion>` to publish those images.

## Develop

```bash
make build test test-envtest   # CGO_ENABLED=0 is exported by the Makefile
make kind-up test-e2e          # kind v1.36 on docker or podman, local registry
make kind-down
```

`hack/gen-testdata.sh` regenerates the committed ELF fixtures.
