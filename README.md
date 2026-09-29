# k8s-modules

An Lmod-style module system for Kubernetes. Admins publish versioned
`Module` resources (`modules.isambard.ac.uk/v1alpha1`); users load them into a
pod with an annotation:

```yaml
metadata:
  annotations:
    modules.isambard.ac.uk/load: aws-ofi-nccl-1.18.0
```

Modules go to every container in `spec.containers` unless
`modules.isambard.ac.uk/load-containers` lists which ones (e.g. `trainer`).

- **module-controller** resolves each Module's dependency closure into its
  `status`.
- **Admission policies** (a MutatingAdmissionPolicy and several
  ValidatingAdmissionPolicies, no webhook) inject image volumes, mounts and
  env, and reject conflicting, forged or unpermitted loads.
- **module-nri**, an NRI plugin DaemonSet, applies what only the runtime can:
  - `LD_LIBRARY_PATH` prepend;
  - host libraries (with split placement and a glibc check through the
    `module-hook` OCI hook);
  - PVC modules;
  - masks;
  - fail-closed start when the plugin is absent.

**Writing modules:** [`docs/writing-modules.md`](docs/writing-modules.md)
covers image modules (preferred), PVC modules (prototyping) and host modules
(kernel-matched libraries), with worked examples.

Designs: [`docs/superpowers/specs/`](docs/superpowers/specs/).

## Install

```bash
helm install k8s-modules oci://ghcr.io/isambard-sc/charts/k8s-modules \
  --version <chart version> --namespace module-system --create-namespace
```

Nodes need Kubernetes 1.36+ and containerd 2.x with NRI's default validator
enabled; see [the chart README](deploy/helm/k8s-modules/README.md).

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
