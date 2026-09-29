# Builds NCCL from source and packages it as an image module: a scratch
# filesystem laid out as lib/libnccl.so.2 -> libnccl.so.2.29.2. Use this
# instead of ../pvc/ when the k8s cluster's nodes don't have the Lustre
# filesystem (/projects/public/alma/...) mounted, so there's nothing to
# discover on the node.
#
# Build for GH200 (aarch64, sm_90) — Isambard nodes are aarch64 natively, so
# no cross-arch emulation needed; podman-hpc is a drop-in for podman here:
#   podman build --build-arg NCCL_TAG=v2.29.2-1 \
#     -t ghcr.io/isambard-sc/modules/nccl:2.29.2 .
#   podman push --digestfile digest ghcr.io/isambard-sc/modules/nccl:2.29.2
#   cat digest    # sha256:…

# Matches the 610-series driver's CUDA 13.2 support. Not strictly required:
# NCCL only links against the stable driver API (libcuda.so) and whatever
# CUDA runtime the pod's own image brings, so a binary built with an older
# toolkit still runs fine under a newer driver (NVIDIA drivers are backward-
# compatible with older CUDA toolkits) — but no reason to lag either.
# Check the exact tag against NGC's catalog before relying on it.
ARG CUDA_IMAGE=nvcr.io/nvidia/cuda:13.2.0-devel-ubuntu24.04
ARG NCCL_TAG=v2.29.2-1

FROM ${CUDA_IMAGE} AS build
ARG NCCL_TAG
# python3 is needed by NCCL's own build (generates include/git_version.h).
RUN apt-get update && apt-get install -y --no-install-recommends git make ca-certificates python3 \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch "${NCCL_TAG}" https://github.com/NVIDIA/nccl.git /src
WORKDIR /src
# compute_90/sm_90 = GH200 (Grace Hopper); add gencodes here for other GPU targets.
# BUILDDIR, not PREFIX: `make src.build` only builds (into BUILDDIR, default
# $(PWD)/build); PREFIX is for a separate `make install` step we don't run.
RUN make -j"$(nproc)" src.build \
    NVCC_GENCODE="-gencode=arch=compute_90,code=sm_90" \
    BUILDDIR=/out

FROM scratch
COPY --from=build /out/lib/ /lib/
COPY --from=build /out/include/ /include/
