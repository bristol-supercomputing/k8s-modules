#!/usr/bin/env bash
set -euo pipefail
CLUSTER=${KIND_CLUSTER:-k8s-modules}
if [[ -z "${CONTAINER_CLI:-}" ]]; then
  if command -v docker >/dev/null 2>&1; then CONTAINER_CLI=docker; else CONTAINER_CLI=podman; fi
fi
[[ "$CONTAINER_CLI" == podman ]] && export KIND_EXPERIMENTAL_PROVIDER=podman
kind delete cluster --name "$CLUSTER"
$CONTAINER_CLI rm -f kind-registry >/dev/null 2>&1 || true
