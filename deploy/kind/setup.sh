#!/usr/bin/env bash
# Creates a kind cluster with a local registry, builds and pushes the
# controller and nccl-fake images, installs the chart and one Module.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLUSTER=${KIND_CLUSTER:-k8s-modules}
NODE_IMAGE=${KIND_NODE_IMAGE:-kindest/node:v1.36.4}
REG_NAME=kind-registry
REG_PORT=${REG_PORT:-5001}
BUILD_ARGS=${BUILD_ARGS:-}
if [[ -z "${CONTAINER_CLI:-}" ]]; then
  if command -v docker >/dev/null 2>&1; then CONTAINER_CLI=docker; else CONTAINER_CLI=podman; fi
fi
PUSH_FLAGS=()
if [[ "$CONTAINER_CLI" == podman ]]; then
  export KIND_EXPERIMENTAL_PROVIDER=podman
  PUSH_FLAGS=(--tls-verify=false)
fi

step() { printf '\n==> %s\n' "$*"; }

step "1. cluster and registry"
if ! $CONTAINER_CLI inspect "$REG_NAME" >/dev/null 2>&1; then
  $CONTAINER_CLI run -d --restart=always -p "127.0.0.1:${REG_PORT}:5000" --name "$REG_NAME" docker.io/library/registry:3
fi
# `kind get nodes`, not `kind get clusters`: the latter breaks on podman 6
# (kind v0.31 formats `podman ps` labels as a map, podman 6 returns a list).
if [[ -z "$(kind get nodes --name "$CLUSTER" 2>/dev/null)" ]]; then
  kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --config "$ROOT/deploy/kind/kind-config.yaml"
fi
$CONTAINER_CLI network connect kind "$REG_NAME" 2>/dev/null || true
for node in $(kind get nodes --name "$CLUSTER"); do
  $CONTAINER_CLI exec "$node" mkdir -p "/etc/containerd/certs.d/${REG_NAME}:5000"
  printf '[host."http://%s:5000"]\n' "$REG_NAME" |
    $CONTAINER_CLI exec -i "$node" tee "/etc/containerd/certs.d/${REG_NAME}:5000/hosts.toml" >/dev/null
done
kubectl config use-context "kind-$CLUSTER" >/dev/null

step "1b. stub host libraries on every node"
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS --target host-libs -t localhost/brics-host-libs:dev "$ROOT/test/images/host-libs"
LIBS=$(mktemp -d)
CID=$($CONTAINER_CLI create localhost/brics-host-libs:dev /none)
$CONTAINER_CLI cp "$CID:/usr/lib/brics-test" "$LIBS/"
$CONTAINER_CLI rm "$CID" >/dev/null
for node in $(kind get nodes --name "$CLUSTER"); do
  $CONTAINER_CLI exec "$node" rm -rf /usr/lib/brics-test
  $CONTAINER_CLI cp "$LIBS/brics-test" "$node:/usr/lib/brics-test"
  echo /usr/lib/brics-test | $CONTAINER_CLI exec -i "$node" tee /etc/ld.so.conf.d/brics-test.conf >/dev/null
  $CONTAINER_CLI exec "$node" ldconfig
  $CONTAINER_CLI exec "$node" sh -c 'mkdir -p /var/lib/brics-test/pvc/lib && echo stub > /var/lib/brics-test/pvc/lib/libpvc.so'
done
rm -rf "$LIBS"

step "2. build and push images"
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS -t "localhost:${REG_PORT}/module-controller:dev" "$ROOT"
$CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/module-controller:dev"
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS -t "localhost:${REG_PORT}/modules/nccl-fake:2.29.2" "$ROOT/test/images/nccl-fake"
$CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/modules/nccl-fake:2.29.2"
DIGEST=$(curl -fsSI \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  -H 'Accept: application/vnd.docker.distribution.manifest.v2+json' \
  "http://localhost:${REG_PORT}/v2/modules/nccl-fake/manifests/2.29.2" |
  awk -F': ' 'tolower($1)=="docker-content-digest"{print $2}' | tr -d '\r')
[[ "$DIGEST" == sha256:* ]] || { echo "could not read nccl-fake digest" >&2; exit 1; }
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS -f "$ROOT/Dockerfile.nri" -t "localhost:${REG_PORT}/module-nri:dev" "$ROOT"
$CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/module-nri:dev"
for target in client client-old; do
  # shellcheck disable=SC2086
  $CONTAINER_CLI build $BUILD_ARGS --target "$target" -t "localhost:${REG_PORT}/fakefabric-$target:dev" "$ROOT/test/images/host-libs"
  $CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/fakefabric-$target:dev"
done

step "3. install chart"
kubectl create namespace module-system --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f "$ROOT/deploy/kind/pv-spack-apps.yaml"
helm upgrade --install k8s-modules "$ROOT/deploy/helm/k8s-modules" \
  --namespace module-system \
  --set image.repository="${REG_NAME}:5000/module-controller" \
  --set image.tag=dev --set image.pullPolicy=Always \
  --set nri.image.repository="${REG_NAME}:5000/module-nri" \
  --set nri.image.tag=dev --set nri.image.pullPolicy=Always \
  --set "nri.pvcs={spack-apps}" \
  --set "registryAllowlist={${REG_NAME}:5000/modules/}" \
  --wait --timeout 3m
kubectl -n module-system rollout restart deployment/module-controller daemonset/module-nri
kubectl -n module-system rollout status deployment/module-controller --timeout=2m
kubectl -n module-system rollout status daemonset/module-nri --timeout=3m

step "4. create nccl-2.29.2"
kubectl apply -f - <<EOF
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
      reference: ${REG_NAME}:5000/modules/nccl-fake@${DIGEST}
  env:
    prependPath:
      LD_LIBRARY_PATH: [lib]
    set:
      NCCL_DEBUG: WARN
EOF
kubectl wait module/nccl-2.29.2 --for=condition=Resolved --timeout=2m
echo "ready: kubectl --context kind-$CLUSTER get modules"
