# DRA extended-resources spike

Answers the four open questions for option B (modules as shareable DRA devices).
Run on kind v1.36.4 (containerd 2.3.4, NRI 0.12.0) on 2026-09-29.

`driver/` is one binary that is both a DRA kubelet plugin and an NRI plugin, as
module-nri would be. It publishes one `allowMultipleAllocations` device per
module and writes a CDI spec per claim. `deploy.yaml` installs it plus a
DeviceClass per module; `pods.yaml` is the test workload; `q3-map.yaml` is the
MutatingAdmissionPolicy for question 3.

Run: build `driver` for linux, push to the kind registry, `kubectl apply -f deploy.yaml`,
create namespace `spike`, `kubectl apply -f pods.yaml`.

## Answers

1. **Sharing.** Yes. Three pods on one node (one with three containers) all got
   `nccl-2-29-2` at once, and one pod's two containers both requested it. Every
   request gets its own `shareID`. No consumable capacity is needed, only
   `allowMultipleAllocations: true`.
2. **CDI split.** The API server creates one claim per pod
   (`<pod>-extended-resources-<rand>`) with a request per container resource,
   named `container-<i>-request-<j>`. `pod.status.extendedResourceClaimStatus.requestMappings`
   maps each request to its container. The kubelet passes a container only the CDI
   devices of its own requests, so a container with no request gets nothing.
   `Device.Requests` in `PrepareResult` decides the mapping.
3. **MAP adding `limits`.** Yes, with `ApplyConfiguration`. Quantities must be
   plain strings (`"1"`); `quantity("1")` is rejected. Set `requests` and `limits`
   together (extended resources need them equal).
4. **NRI sees CDI env.** Yes. CDI edits are already in `ctr.Env` at NRI
   `CreateContainer`, so an NRI prepend to `LD_LIBRARY_PATH` composes.

## Gotchas for the design

- CDI edits from several requests in one container collide on the same env key
  and the last one wins (two modules both setting `LD_LIBRARY_PATH` left only one).
  CDI cannot compose a path list, so NRI must build `LD_LIBRARY_PATH` itself.
  Each CDI edit should carry a unique marker such as `BRICS_MODULE_<name>=1` so NRI
  can tell which modules a container has. CDI annotations are not visible to NRI.
- DeviceClass CEL has no `device.name`. Publish a `module` attribute on each
  device and select on `device.attributes["modules.isambard.ac.uk"].module`.
- The plugin data directory `/var/lib/kubelet/plugins/<driver>` must exist before
  the plugin starts; mount it as a `DirectoryOrCreate` hostPath.
- Device names must be DNS labels, so `nccl-2.29.2` becomes `nccl-2-29-2`; the
  extended resource name can keep the dots.
