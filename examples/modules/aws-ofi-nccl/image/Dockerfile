# Builds aws-ofi-nccl from source and packages it as an image module. Use
# this instead of ../pvc/ when the k8s cluster's nodes don't have the
# Lustre filesystem (/projects/public/alma/...) mounted, so there's nothing
# to discover on the node.
#
# Unlike nccl, this still needs the node's Cray libfabric (with the CXI
# provider for Slingshot) to *build* against — it's proprietary HPE software,
# not on any package registry. On AlmaLinux it now installs to the node's
# regular system paths rather than an isolated /opt/cray tree, so assemble a
# --with-libfabric-style prefix from those paths first, on any GH200 node:
#   mkdir -p libfabric-1.22.0/lib libfabric-1.22.0/include
#   cp /usr/lib64/libfabric.so* /usr/lib64/libcxi.so* /usr/lib64/libnl-3.so* libfabric-1.22.0/lib/
#   cp -r /usr/include/rdma libfabric-1.22.0/include/   # fabric.h etc.; adjust if this differs
# (the unversioned libfabric.so symlink ./configure needs is created below,
# since a node without libfabric-devel won't have it to copy)
#
# libcxi.so.1 and libnl-3.so.200 are needed here too, not just at runtime:
# linking any executable against libfabric.so.1 requires its own NEEDED
# libraries to be resolvable (libcxi, in turn needing libnl-3), or ld
# refuses the link entirely. libnl-3 has to come from the same node too,
# not apt: apt's generic build resolves the plain "file not found" problem
# but doesn't define these symbols under the same version tag (@libnl_3)
# AlmaLinux's build does, so it still fails with "undefined reference to
# ...@libnl_3" even once installed — a real ABI mismatch, not a missing
# package. libcurl/libjson-c don't have this problem (see the apt-get
# install line below), so those still come from apt.
#
# The built .so still dynamically links against libfabric.so.1 and libcxi.so.1
# at runtime, so this module keeps dependsOn: [libfabric-host] regardless
# (see module.yaml) — building against a copy doesn't remove that.
#
# Build for GH200 (aarch64) — Isambard nodes are aarch64 natively, so no
# cross-arch emulation needed; podman-hpc is a drop-in for podman here:
#   podman build --build-arg AWS_OFI_NCCL_TAG=v1.20.0 \
#     -t ghcr.io/isambard-sc/modules/aws-ofi-nccl:1.20.0 .
#   podman push --digestfile digest ghcr.io/isambard-sc/modules/aws-ofi-nccl:1.20.0
#   cat digest    # sha256:…

# Matches the 610-series driver's CUDA 13.2 support. Not strictly required —
# a binary built against an older toolkit still runs under a newer driver
# (see the note above the CUDA_IMAGE default in ../../nccl/image/Dockerfile
# for why toolkit/driver version don't need to match going this direction —
# check the exact tag against NGC's catalog before relying on it).
ARG CUDA_IMAGE=nvcr.io/nvidia/cuda:13.2.0-devel-ubuntu24.04
ARG AWS_OFI_NCCL_TAG=v1.20.0
# gdrcopy adds GPUDirect RDMA copy support (FI_HMEM_CUDA_USE_GDRCOPY). It's
# an optional extra, not required for RDMA to work at all; drop the
# --with-gdrcopy configure flag below and unset FI_HMEM_CUDA_USE_GDRCOPY in
# module.yaml to skip it.
ARG GDRCOPY_TAG=v2.6

FROM ${CUDA_IMAGE} AS build
ARG AWS_OFI_NCCL_TAG
ARG GDRCOPY_TAG
# libcurl4t64/libjson-c5: real (generic, ABI-stable) runtime deps of
# libfabric's CXI provider — fine to satisfy from apt purely to let the
# build-time link succeed. Dynamic linking only records the SONAME, not
# which copy was used to build, so the actual container still uses whatever
# libfabric-host provides at runtime regardless. libnl-3.so.200 (needed by
# libcxi.so.1, one level deeper) is NOT included here: apt's generic build
# doesn't define its symbols under the same version tag AlmaLinux's does,
# so it has to come from the node instead (see the comment above).
RUN apt-get update && apt-get install -y --no-install-recommends \
    git make autoconf automake libtool pkg-config ca-certificates libhwloc-dev \
    libcurl4t64 libjson-c5 \
    && rm -rf /var/lib/apt/lists/*

# Copied from a GH200 node (see comment above); NOT fetched from a registry.
COPY libfabric-1.22.0/ /opt/libfabric/
# Only libfabric.so.1 (the SONAME) exists on a node without libfabric-devel
# installed; ./configure's link check wants the unversioned libfabric.so
# that -devel packages normally symlink in, so create it ourselves.
RUN ln -sf libfabric.so.1 /opt/libfabric/lib/libfabric.so

RUN git clone --depth 1 --branch "${GDRCOPY_TAG}" https://github.com/NVIDIA/gdrcopy.git /gdrcopy
RUN make -C /gdrcopy lib -j"$(nproc)" CUDA=${CUDA_HOME} \
    && make -C /gdrcopy lib_install DESTLIB=/opt/gdrcopy/lib DESTINC=/opt/gdrcopy/include

RUN git clone --depth 1 --branch "${AWS_OFI_NCCL_TAG}" https://github.com/aws/aws-ofi-nccl.git /src
WORKDIR /src
# --with-libfabric only adds -L/opt/libfabric/lib, which is enough to find
# libfabric.so itself for our own -lfabric request, but ld doesn't reuse -L
# to resolve *libfabric.so's own* NEEDED entries (libcxi.so.1, resolved
# separately from libcurl/libjson-c since apt installs those to the default
# system path ld always searches) — hence the explicit ld warning:
# "not found (try using -rpath or -rpath-link)". -rpath-link (not -rpath)
# is link-time only, so it doesn't end up baked into the built .so's own
# runtime search path — appropriate here since /opt/libfabric/lib won't
# exist in the deployed container; the real libcxi.so.1 comes from
# libfabric-host's bind mount at a different path entirely.
# CUDA_HOME isn't actually exported by this base image (only PATH/LD_LIBRARY_PATH
# point at it), even though the toolkit is always at /usr/local/cuda —
# without a fallback, --with-cuda="${CUDA_HOME}" silently passes an empty
# value and configure never finds CUDA at all ("requires either CUDA, ROCm,
# or Neuron runtime").
RUN ./autogen.sh && LDFLAGS='-Wl,-rpath-link,/opt/libfabric/lib' ./configure \
    --prefix=/out \
    --with-cuda="${CUDA_HOME:-/usr/local/cuda}" \
    --with-libfabric=/opt/libfabric \
    --with-gdrcopy=/opt/gdrcopy \
    --disable-tests \
    || (cat config.log; exit 1) \
    && make -j"$(nproc)" install

# No /out/include/ here: unlike nccl, aws-ofi-nccl is a plugin with no
# public C API, so `make install` never creates one to copy.
FROM scratch
COPY --from=build /out/lib/ /lib/
COPY --from=build /opt/gdrcopy/lib/ /deps/
