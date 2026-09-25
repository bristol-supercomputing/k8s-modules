#!/usr/bin/env bash
# Regenerates the committed ELF fixtures in internal/discovery/testdata and
# internal/hook/testdata. The fixtures are built for this machine's
# architecture, which is recorded in testdata/hostroot/ARCH.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CLI=${CONTAINER_CLI:-$(command -v docker >/dev/null 2>&1 && echo docker || echo podman)}
"$CLI" run --rm -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
	-v "$ROOT/test/images/host-libs/src:/src:ro" \
	-v "$ROOT/hack/testdata.sh:/testdata.sh:ro" \
	-v "$ROOT/internal:/out" \
	docker.io/library/debian:bookworm bash /testdata.sh
