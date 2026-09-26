# BASE_IMAGE selects the runtime base for the final app stage. It MUST be declared
# at global scope (before the first FROM) so the `FROM ${BASE_IMAGE}` below can use
# it. Defaults to the local `runtime-base` stage, so a from-scratch `docker build .`
# and the PR container-test build are fully self-contained. CI publish builds
# override it with the pre-published, digest-pinned
# ghcr.io/tphakala/birdnet-go-base@sha256:... so app-only releases dedup against
# immutable base layers regardless of BuildKit cache state.
ARG BASE_IMAGE=runtime-base

ARG TENSORFLOW_VERSION=2.17.1
# TensorFlow Lite C runtime library release (github.com/tphakala/tflite_c). Kept in
# sync with TENSORFLOW_VERSION (headers) and the Taskfile TFLITE_VERSION var; the
# runtime-base stage downloads the matching prebuilt libtensorflowlite_c.so.
ARG TFLITE_VERSION=v2.17.1
ARG ONNXRUNTIME_VERSION=1.25.1
# OpenVINO toolkit pin for the runtime libraries bundled into the images. Keep the
# release/build in sync with the Taskfile OPENVINO_RELEASE / OPENVINO_BUILD values;
# the SHA256s are per-arch (arm64 matches the Taskfile OPENVINO_SHA256 header pin).
ARG OPENVINO_RELEASE=2026.2
ARG OPENVINO_BUILD=2026.2.0.21903.52ddc073857
ARG OPENVINO_SHA256_AMD64=86896e9347cd160370d16f80fa2c49c2b7a51ec33b55cea6493c7dc7c4c61c55
ARG OPENVINO_SHA256_ARM64=8ce45467967e22fddb83a6b72a8bd1f9bfa6f43351e1ca2eaf5251064fe17767

# Intel NEO (compute-runtime) and IGC (intel-graphics-compiler) pin for
# amd64 iGPU inference. Only used on linux/amd64; arm64 has no Intel GPU plugin.
ARG NEO_VERSION=26.22.38646.4
ARG IGC_VERSION=2.36.3+21719
ARG IGC_TAG=v2.36.3
ARG GMMLIB_VERSION=22.10.0
ARG NEO_SHA256_IGC_CORE=9e0975ac75015b431ebb2da81a802b9fd1e28a3c270313a97569cd1e6a6c6048
ARG NEO_SHA256_IGC_OPENCL=350a52331e784bb7fb9ed42e993b5c44b7e6562fc74d2cf3102b29b6a576fa85
ARG NEO_SHA256_OPENCL_ICD=6fdac2e8a2aacf844ebfd90521bf7102b3ebb44f69c1bced1a9785a7ce96a3c2
ARG NEO_SHA256_IGDGMM=6031a63d6e8a12ce61c14efc15f2c8e727061286e3820b8594e6d00615e04d54
ARG NEO_SHA256_ZE_GPU=8bef9f24e03f826f93c076081bda13c6ac3afbd9e42b9fb8f298fab652330e2f

# Legacy Intel NEO/IGC track for Gen8/Gen9/Gen11 iGPUs (e.g. Coffee Lake UHD 630).
# Current compute-runtime releases (NEO_VERSION below) dropped support for these
# generations starting at 24.35.30872.22; Intel ships continued support via a
# parallel -legacy1 package track. Both tracks install side-by-side without
# conflict as long as the legacy IGC pin (non-"-2" package names) matches exactly
# what intel-opencl-icd-legacy1/intel-level-zero-gpu-legacy1 depend on.
# See: https://github.com/intel/compute-runtime/blob/master/LEGACY_PLATFORMS.md
ARG LEGACY_NEO_VERSION=24.35.30872.22
# The level-zero-gpu package carries its own 1.3.x version string that differs
# from the 24.35.x compute-runtime release tag it ships under.
ARG LEGACY_ZE_GPU_VERSION=1.3.30872.22
ARG LEGACY_IGC_TAG=igc-1.0.17537.20
ARG LEGACY_IGC_VERSION=1.0.17537.20
ARG LEGACY_SHA256_IGC_CORE=7f2af5b0e567a43625a748effb744d0b3c96acf805467d099e46eee617e11b2a
ARG LEGACY_SHA256_IGC_OPENCL=ac2088331d55c7de15bd57373f73630e95b40e1275934bcfd96bf0c3e03769a7
ARG LEGACY_SHA256_OPENCL_ICD=1ac4639c148ab6c9db6bc5a7733ca40bac3782cccc5b3da8e8cb7da8e8f20b1b
ARG LEGACY_SHA256_ZE_GPU=54d42056c627dd36eaaf3dcfad5d80fb90e9c0d65e4a4d5ab8b0e8ed1277891b

FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS buildenv

# Pass BUILD_VERSION through to the build stage
ARG BUILD_VERSION
ENV BUILD_VERSION=${BUILD_VERSION:-unknown}

# Install Task and other dependencies
RUN apt-get update -q && apt-get install -q -y \
    curl \
    git \
    sudo \
    zip \
    gcc-aarch64-linux-gnu && \
    rm -rf /var/lib/apt/lists/*

# Install Node.js v24 from NodeSource
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
    apt-get install -y nodejs && \
    rm -rf /var/lib/apt/lists/*

# Install Task
RUN sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin

# Create dev-user for building and devcontainer usage
RUN groupadd --gid 10001 dev-user && \
    useradd --uid 10001 --gid dev-user --shell /bin/bash --create-home dev-user && \
    usermod -aG sudo dev-user && \
    usermod -aG audio dev-user && \
    echo '%sudo ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers && \
    mkdir -p /home/dev-user/src && \
    mkdir -p /home/dev-user/lib && \
    mkdir -p /home/dev-user/.cache && \
    mkdir -p /home/dev-user/.npm && \
    chown -R dev-user:dev-user /home/dev-user

USER dev-user
WORKDIR /home/dev-user/src/BirdNET-Go

# Copy all source files first to have Git information available
COPY --chown=dev-user . ./

# Pre-build frontend in the shared buildenv stage so it runs once.
# Without this, multi-platform builds run npm install concurrently
# per platform, exhausting memory on CI runners and corrupting packages.
ENV PUPPETEER_SKIP_DOWNLOAD=true
RUN task frontend-build

# Enter Build stage
FROM --platform=$BUILDPLATFORM buildenv AS build
ARG BUILD_VERSION
ENV BUILD_VERSION=${BUILD_VERSION:-unknown}

# Sentry DSN baked into the binary at link time (consumed by the Taskfile
# SENTRY_DSN var). Empty by default so unofficial builds ship telemetry off;
# official CI passes it via --build-arg SENTRY_DSN. The final runtime image is a
# separate stage, so this build-time value never lingers as a runtime ENV.
ARG SENTRY_DSN
ENV SENTRY_DSN=${SENTRY_DSN:-}

# Project identity baked into the binary at link time (consumed by the Taskfile
# PROJECT_* vars). Empty by default so builds use the upstream defaults in
# internal/branding; forks pass --build-arg PROJECT_NAME / PROJECT_REPO_URL /
# PROJECT_COMMUNITY_URL to rebrand. Self-hosters can instead override at runtime
# with the BIRDNET_GO_PROJECT_* environment variables.
ARG PROJECT_NAME
ENV PROJECT_NAME=${PROJECT_NAME:-}
ARG PROJECT_REPO_URL
ENV PROJECT_REPO_URL=${PROJECT_REPO_URL:-}
ARG PROJECT_COMMUNITY_URL
ENV PROJECT_COMMUNITY_URL=${PROJECT_COMMUNITY_URL:-}

ARG TARGETPLATFORM

# OpenVINO C API header provisioning for the compile. The openvino-tagged backend
# dlopens libopenvino_c at runtime, so the runtime .so set is NOT needed here (it
# ships via the runtime-base stage below); only the arch-independent C API headers
# are required at compile time. Stage them into the Taskfile OpenVINO header cache
# (.cache/openvino) with a matching .build-id stamp so the check-openvino task
# treats them as up-to-date and does not re-download during the build. The SHA256
# is verified per arch before extraction (a moved or tampered upstream archive can
# still return HTTP 200).
ARG OPENVINO_RELEASE
ARG OPENVINO_BUILD
ARG OPENVINO_SHA256_AMD64
ARG OPENVINO_SHA256_ARM64
RUN set -eu; \
    case "${TARGETPLATFORM}" in \
        "linux/amd64") OV_SUFFIX=x86_64; OV_SHA256="${OPENVINO_SHA256_AMD64}" ;; \
        "linux/arm64") OV_SUFFIX=arm64;  OV_SHA256="${OPENVINO_SHA256_ARM64}" ;; \
        *) echo "Error: unsupported platform ${TARGETPLATFORM}" >&2; exit 1 ;; \
    esac; \
    OV_BASE="openvino_toolkit_ubuntu22_${OPENVINO_BUILD}_${OV_SUFFIX}"; \
    OV_URL="https://storage.openvinotoolkit.org/repositories/openvino/packages/${OPENVINO_RELEASE}/linux/${OV_BASE}.tgz"; \
    echo "Downloading OpenVINO ${OPENVINO_BUILD} (${OV_SUFFIX}) headers"; \
    curl -fsSL "${OV_URL}" -o /tmp/openvino.tgz; \
    echo "${OV_SHA256}  /tmp/openvino.tgz" | sha256sum -c -; \
    mkdir -p /tmp/ov; \
    tar -xzf /tmp/openvino.tgz -C /tmp/ov --strip-components=1 "${OV_BASE}/runtime/include"; \
    mkdir -p .cache/openvino; \
    rm -rf .cache/openvino/include; \
    cp -a /tmp/ov/runtime/include .cache/openvino/include; \
    test -f .cache/openvino/include/openvino/c/openvino.h; \
    printf '%s\n' "${OPENVINO_BUILD}" > .cache/openvino/.build-id; \
    rm -rf /tmp/openvino.tgz /tmp/ov

# Build assets and compile BirdNET-Go (non-embedded, TFLite/ONNX + native OpenVINO).
# The noembed_linux_* targets default OPENVINO=true and download the TFLite C
# library (linked via CGO_LDFLAGS -ltensorflowlite_c) into DOCKER_LIB_DIR for the
# compile. The runtime .so sets (ONNX, TFLite, OpenVINO) ship via the runtime-base
# stage, not from here. The backend self-gates (non-A76 arm64, or amd64 without
# Intel GPU drivers) and falls back to ONNX Runtime, so enabling OpenVINO here is
# safe for every deployment. OPENVINO_BUILD is passed through to the task so the
# check-openvino header-cache guard compares against the SAME build id this stage
# staged into .cache/openvino above, making the guard a guaranteed no-op.
# Note: frontend-build (including Tailwind) is handled as a dependency of noembed_* tasks
RUN --mount=type=cache,target=/go/pkg/mod,uid=10001,gid=10001 \
    --mount=type=cache,target=/home/dev-user/.cache/go-build,uid=10001,gid=10001 \
    task check-tensorflow && \
    TARGET=$(echo ${TARGETPLATFORM} | tr '/' '_') && \
    echo "Building non-embedded version with BUILD_VERSION=${BUILD_VERSION}" && \
    BUILD_VERSION="${BUILD_VERSION}" SENTRY_DSN="${SENTRY_DSN}" PROJECT_NAME="${PROJECT_NAME}" PROJECT_REPO_URL="${PROJECT_REPO_URL}" PROJECT_COMMUNITY_URL="${PROJECT_COMMUNITY_URL}" DOCKER_LIB_DIR=/home/dev-user/lib task noembed_${TARGET} OPENVINO_BUILD="${OPENVINO_BUILD}"

# ============================================================================
# runtime-base: the heavy, rarely-changing runtime library layer.
#
# This stage is published separately as ghcr.io/<repo>-base:base-<sha> (the tag is
# content-addressed on this Dockerfile; see .github/workflows/base-image.yml). The
# final app stage below does `FROM ${BASE_IMAGE}` where BASE_IMAGE defaults to this
# local stage (so a from-scratch `docker build .` and the PR container-test build
# are fully self-contained), and CI publish builds override BASE_IMAGE with the
# pre-published base pinned by digest so app-only releases stack only the
# model/script/binary layers on top of immutable base blobs (structural layer
# dedup, independent of BuildKit cache state).
#
# It downloads its own runtime .so sets rather than copying from the `build`
# stage, so building `--target runtime-base` never drags in the Go compile. The
# pins are the top-of-file ARGs (the single source of truth); content-addressing
# the base tag on this Dockerfile means any pin change yields a new base tag and a
# rebuild, so the shipped runtime libs never drift from what the binary linked and
# compiled against.
# ============================================================================
FROM --platform=$TARGETPLATFORM debian:trixie-slim AS runtime-base
ARG TARGETPLATFORM

# Install ALSA library and SOX for audio processing, tini (a tiny init run as
# PID 1, see the final-stage ENTRYPOINT note), and other system utilities for
# debugging.
RUN apt-get update -q && apt-get install -q -y --no-install-recommends \
    adduser \
    ca-certificates \
    libasound2 \
    ffmpeg \
    sox \
    libsox-fmt-mp3 \
    procps \
    iproute2 \
    net-tools \
    curl \
    wget \
    nano \
    vim \
    less \
    tzdata \
    tzdata-legacy \
    jq \
    strace \
    lsof \
    bash-completion \
    gosu \
    tini \
    && rm -rf /var/lib/apt/lists/*

# ONNX Runtime (used by all arches; arm64 relies on it exclusively). onnxruntime
# is dlopened at runtime, never linked at compile, so it is provisioned here in
# the base rather than in the `build` stage. `tar --no-same-owner` extracts the
# archive as root (GNU tar as root otherwise restores the archive's stored UIDs),
# so the libraries land root-owned under /usr/lib and a non-root runtime process
# sharing an unrelated UID cannot overwrite them. The same flag is used for the
# TFLite and OpenVINO extractions below for the same reason.
ARG ONNXRUNTIME_VERSION
RUN set -eu; \
    ONNX_ARCH=$(case "${TARGETPLATFORM}" in \
        "linux/amd64") echo "x64" ;; \
        "linux/arm64") echo "aarch64" ;; \
        *) echo "Error: unsupported platform ${TARGETPLATFORM}" >&2; exit 1 ;; \
    esac); \
    echo "Downloading ONNX Runtime ${ONNXRUNTIME_VERSION} for ${ONNX_ARCH}"; \
    curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-${ONNX_ARCH}-${ONNXRUNTIME_VERSION}.tgz" \
        -o /tmp/onnxruntime.tgz; \
    mkdir -p /tmp/onnxruntime; \
    tar --no-same-owner -xzf /tmp/onnxruntime.tgz -C /tmp/onnxruntime --strip-components=1; \
    cp -a /tmp/onnxruntime/lib/libonnxruntime*.so* /usr/lib/; \
    rm -rf /tmp/onnxruntime /tmp/onnxruntime.tgz

# TensorFlow Lite C runtime library: installed for all arches. The stock default
# classifier is ONNX on arm64 (reduced memory) and TFLite on amd64, but every arch
# ships libtensorflowlite_c so a user-supplied custom `.tflite` model path loads in
# any container. No TFLite interpreter is created unless a .tflite model is actually
# loaded, so the arm64 memory win for the ONNX default is preserved. Downloaded from
# the same prebuilt release the Taskfile download-tflite task uses; the bare soname
# symlink is created so a bare-soname dlopen/link resolves.
ARG TFLITE_VERSION
RUN set -eu; \
    TFA=$(case "${TARGETPLATFORM}" in \
        "linux/amd64") echo "linux_amd64" ;; \
        "linux/arm64") echo "linux_arm64" ;; \
        *) echo "Error: unsupported platform ${TARGETPLATFORM}" >&2; exit 1 ;; \
    esac); \
    TF_VER="${TFLITE_VERSION#v}"; \
    echo "Downloading TFLite C library ${TFLITE_VERSION} for ${TFA}"; \
    wget -q "https://github.com/tphakala/tflite_c/releases/download/${TFLITE_VERSION}/tflite_c_${TFLITE_VERSION}_${TFA}.tar.gz" -O /tmp/tflite.tar.gz; \
    tar --no-same-owner -xzf /tmp/tflite.tar.gz -C /tmp; \
    cp -a "/tmp/libtensorflowlite_c.so.${TF_VER}" /usr/lib/; \
    ln -sf "libtensorflowlite_c.so.${TF_VER}" /usr/lib/libtensorflowlite_c.so; \
    test -e /usr/lib/libtensorflowlite_c.so; \
    rm -rf /tmp/tflite.tar.gz "/tmp/libtensorflowlite_c.so.${TF_VER}"

# OpenVINO runtime libraries (both arches). The openvino-tagged binary dlopens
# libopenvino_c at runtime and self-gates: a non-A76 arm64 CPU, or an amd64 host
# without Intel GPU drivers, falls back to ONNX Runtime cleanly. Only the libraries
# the backend needs are kept (core, C API, ONNX + IR frontends, the arch CPU plugin,
# the amd64 iGPU plugin, and TBB); the unused frontends and plugins are dropped to
# save size. The SHA256 is verified per arch before extraction (a moved or tampered
# upstream archive can still return HTTP 200). Symlinks are preserved so the
# bare-soname dlopen resolves.
ARG OPENVINO_RELEASE
ARG OPENVINO_BUILD
ARG OPENVINO_SHA256_AMD64
ARG OPENVINO_SHA256_ARM64
RUN set -eu; \
    case "${TARGETPLATFORM}" in \
        "linux/amd64") OV_SUFFIX=x86_64; OV_LIBDIR=intel64; OV_SHA256="${OPENVINO_SHA256_AMD64}"; OV_CPU_PLUGIN=libopenvino_intel_cpu_plugin.so; OV_GPU_PLUGIN=libopenvino_intel_gpu_plugin.so ;; \
        "linux/arm64") OV_SUFFIX=arm64;  OV_LIBDIR=aarch64; OV_SHA256="${OPENVINO_SHA256_ARM64}"; OV_CPU_PLUGIN=libopenvino_arm_cpu_plugin.so;   OV_GPU_PLUGIN="" ;; \
        *) echo "Error: unsupported platform ${TARGETPLATFORM}" >&2; exit 1 ;; \
    esac; \
    OV_BASE="openvino_toolkit_ubuntu22_${OPENVINO_BUILD}_${OV_SUFFIX}"; \
    OV_URL="https://storage.openvinotoolkit.org/repositories/openvino/packages/${OPENVINO_RELEASE}/linux/${OV_BASE}.tgz"; \
    echo "Downloading OpenVINO ${OPENVINO_BUILD} (${OV_SUFFIX}) runtime"; \
    curl -fsSL "${OV_URL}" -o /tmp/openvino.tgz; \
    echo "${OV_SHA256}  /tmp/openvino.tgz" | sha256sum -c -; \
    mkdir -p /tmp/ov; \
    tar --no-same-owner -xzf /tmp/openvino.tgz -C /tmp/ov --strip-components=1 \
        "${OV_BASE}/runtime/lib/${OV_LIBDIR}" \
        "${OV_BASE}/runtime/3rdparty/tbb/lib"; \
    OV_SRC="/tmp/ov/runtime/lib/${OV_LIBDIR}"; \
    cp -a "${OV_SRC}"/libopenvino.so* /usr/lib/; \
    cp -a "${OV_SRC}"/libopenvino_c.so* /usr/lib/; \
    cp -a "${OV_SRC}"/libopenvino_onnx_frontend.so* /usr/lib/; \
    cp -a "${OV_SRC}"/libopenvino_ir_frontend.so* /usr/lib/; \
    cp -a "${OV_SRC}/${OV_CPU_PLUGIN}"* /usr/lib/; \
    if [ -n "${OV_GPU_PLUGIN}" ]; then cp -a "${OV_SRC}/${OV_GPU_PLUGIN}"* /usr/lib/; fi; \
    find /tmp/ov/runtime/3rdparty/tbb/lib -name '*.so*' -exec cp -a {} /usr/lib/ \; ; \
    test -e /usr/lib/libtbb.so.12; \
    rm -rf /tmp/openvino.tgz /tmp/ov

# Install Intel NEO compute runtime for amd64 iGPU inference including legacy support.
# NEO provides the OpenCL ICD that lets OpenVINO talk to the Intel iGPU when
# /dev/dri is passed through from the host. This step only runs on linux/amd64;
# arm64 has no Intel GPU plugin.
ARG NEO_VERSION
ARG IGC_VERSION
ARG IGC_TAG
ARG GMMLIB_VERSION
ARG NEO_SHA256_IGC_CORE
ARG NEO_SHA256_IGC_OPENCL
ARG NEO_SHA256_OPENCL_ICD
ARG NEO_SHA256_IGDGMM
ARG NEO_SHA256_ZE_GPU
ARG LEGACY_NEO_VERSION
ARG LEGACY_ZE_GPU_VERSION
ARG LEGACY_IGC_TAG
ARG LEGACY_IGC_VERSION
ARG LEGACY_SHA256_IGC_CORE
ARG LEGACY_SHA256_IGC_OPENCL
ARG LEGACY_SHA256_OPENCL_ICD
ARG LEGACY_SHA256_ZE_GPU

RUN if [ "$TARGETPLATFORM" = "linux/amd64" ]; then \
      apt-get update -q && apt-get install -q -y --no-install-recommends \
        ocl-icd-libopencl1 libdrm2 libdrm-intel1 && \
      mkdir -p /tmp/neo && cd /tmp/neo && \
      wget -q \
        "https://github.com/intel/intel-graphics-compiler/releases/download/${IGC_TAG}/intel-igc-core-2_${IGC_VERSION}_amd64.deb" \
        "https://github.com/intel/intel-graphics-compiler/releases/download/${IGC_TAG}/intel-igc-opencl-2_${IGC_VERSION}_amd64.deb" \
        "https://github.com/intel/compute-runtime/releases/download/${NEO_VERSION}/intel-opencl-icd_${NEO_VERSION}-0_amd64.deb" \
        "https://github.com/intel/compute-runtime/releases/download/${NEO_VERSION}/libigdgmm12_${GMMLIB_VERSION}_amd64.deb" \
        "https://github.com/intel/compute-runtime/releases/download/${NEO_VERSION}/libze-intel-gpu1_${NEO_VERSION}-0_amd64.deb" \
        "https://github.com/intel/intel-graphics-compiler/releases/download/${LEGACY_IGC_TAG}/intel-igc-core_${LEGACY_IGC_VERSION}_amd64.deb" \
        "https://github.com/intel/intel-graphics-compiler/releases/download/${LEGACY_IGC_TAG}/intel-igc-opencl_${LEGACY_IGC_VERSION}_amd64.deb" \
        "https://github.com/intel/compute-runtime/releases/download/${LEGACY_NEO_VERSION}/intel-opencl-icd-legacy1_${LEGACY_NEO_VERSION}_amd64.deb" \
        "https://github.com/intel/compute-runtime/releases/download/${LEGACY_NEO_VERSION}/intel-level-zero-gpu-legacy1_${LEGACY_ZE_GPU_VERSION}_amd64.deb" && \
      printf '%s\n' \
        "${NEO_SHA256_IGC_CORE}  intel-igc-core-2_${IGC_VERSION}_amd64.deb" \
        "${NEO_SHA256_IGC_OPENCL}  intel-igc-opencl-2_${IGC_VERSION}_amd64.deb" \
        "${NEO_SHA256_OPENCL_ICD}  intel-opencl-icd_${NEO_VERSION}-0_amd64.deb" \
        "${NEO_SHA256_IGDGMM}  libigdgmm12_${GMMLIB_VERSION}_amd64.deb" \
        "${NEO_SHA256_ZE_GPU}  libze-intel-gpu1_${NEO_VERSION}-0_amd64.deb" \
        "${LEGACY_SHA256_IGC_CORE}  intel-igc-core_${LEGACY_IGC_VERSION}_amd64.deb" \
        "${LEGACY_SHA256_IGC_OPENCL}  intel-igc-opencl_${LEGACY_IGC_VERSION}_amd64.deb" \
        "${LEGACY_SHA256_OPENCL_ICD}  intel-opencl-icd-legacy1_${LEGACY_NEO_VERSION}_amd64.deb" \
        "${LEGACY_SHA256_ZE_GPU}  intel-level-zero-gpu-legacy1_${LEGACY_ZE_GPU_VERSION}_amd64.deb" \
        | sha256sum -c - && \
      dpkg -i *.deb && \
      rm -rf /tmp/neo /var/lib/apt/lists/*; \
    fi

# Refresh the loader cache once all runtime libraries are installed.
RUN ldconfig

# ============================================================================
# Stock model selection. arm64 ships ONNX-only stock models (the reduced-memory
# INT8-ARM default); other arches ship the TFLite models. Each arch's set is
# assembled in its own `scratch` stage and a single reproducible COPY pulls the
# selected set into the final image, so the image never carries the other arch's
# models or a duplicated copy (a plain COPY, unlike a stage-then-`cp`+`rm`, adds
# no whiteout-masked duplicate layer). Custom models are supplied by the user at
# runtime under /data/models, not shipped here.
# ============================================================================
FROM scratch AS models-amd64
COPY --from=build /home/dev-user/src/BirdNET-Go/internal/classifier/data/*.tflite /

FROM scratch AS models-arm64
COPY --from=build /home/dev-user/src/BirdNET-Go/internal/classifier/data/*.onnx /

# TARGETARCH is a predefined build arg (amd64/arm64), automatically in scope for
# FROM, so it selects the matching scratch stage without an explicit declaration.
FROM models-${TARGETARCH} AS models

# ============================================================================
# Final application image. BASE_IMAGE defaults to the local runtime-base stage
# (self-contained builds: local `docker build`, PR container-test, first build);
# CI publish builds override it with the pre-published base pinned by digest
# (ghcr.io/<repo>-base@sha256:..., resolved from the content-addressed base-<sha>
# tag) so app-only releases dedup against the immutable base layers. Everything
# the base already installed (apt packages, ONNX/TFLite/OpenVINO runtime libs,
# Intel NEO) is inherited; this stage only adds the arch-selected models, helper
# scripts, and the birdnet-go binary. BASE_IMAGE is declared at global scope near
# the top of this file.
# ============================================================================
FROM ${BASE_IMAGE} AS final

# Copy the arch-selected stock models, then normalize permissions explicitly.
# Do NOT use `COPY --chmod` here: when the source is a stage root, BuildKit applies
# the mode to the destination directory itself, so `--chmod=644` leaves /models
# non-traversable (0644) and a non-root runtime user gets "permission denied"
# opening a model (Buildah instead keeps the dir 0755, so the two builders
# disagree and a local Buildah test passes while CI/BuildKit fails). The explicit
# chmod guarantees a traversable dir (0755) and world-readable files (0644) on
# every builder. The single arch set is still copied once, so the ~116 MB
# duplicate model layer stays gone.
COPY --from=models --chown=root:root / /models/
RUN chmod 0755 /models && find /models -mindepth 1 -type f -exec chmod 0644 {} +

# Include reset_auth tool and startup scripts from the build stage.
COPY --from=build --chmod=755 /home/dev-user/src/BirdNET-Go/reset_auth.sh /usr/bin/
COPY --from=build --chmod=755 /home/dev-user/src/BirdNET-Go/Docker/entrypoint.sh /usr/bin/
COPY --from=build --chmod=755 /home/dev-user/src/BirdNET-Go/Docker/startup-wrapper.sh /usr/bin/

# Create config and data directories with proper permissions for rootless compatibility
# Make them world-writable so non-root users can create subdirectories
RUN mkdir -p /config /data/clips /data/models && \
    chmod 777 /config /data /data/clips /data/models
VOLUME /config
VOLUME /data
WORKDIR /data

# Make ports available to the world outside this container
# 80, 443 for AutoTLS (automatic HTTPS certificates)
# 8080 application standard HTTP web interface port
# 8090 Prometheus metrics endpoint
EXPOSE 80 443 8080 8090

COPY --from=build /home/dev-user/src/BirdNET-Go/bin /usr/bin/

# Add container labels for metadata and compatibility information
LABEL org.opencontainers.image.title="BirdNET-Go"
LABEL org.opencontainers.image.description="Real-time bird sound identification using BirdNET with ONNX Runtime and OpenVINO support"
LABEL org.opencontainers.image.source="https://github.com/tphakala/birdnet-go"
LABEL org.opencontainers.image.documentation="https://github.com/tphakala/birdnet-go/blob/main/README.md"
LABEL org.opencontainers.image.url="https://github.com/tphakala/birdnet-go"
LABEL org.opencontainers.image.vendor="tphakala"

# Container runtime compatibility labels
LABEL container.runtime.docker="true"
LABEL container.runtime.podman="true"
LABEL container.runtime.oci="true"

# Podman-specific compatibility information
LABEL podman.compatible="true"
LABEL podman.rootless="true"
LABEL podman.userns="keep-id"
LABEL podman.network.bridge="true"

# Usage information for different runtimes
LABEL usage.docker="docker run -d --name birdnet-go -p 8080:8080 -v ./config:/config -v ./data:/data --device /dev/snd:/dev/snd ghcr.io/tphakala/birdnet-go:latest"
LABEL usage.podman="podman run -d --name birdnet-go -p 8080:8080 -v ./config:/config -v ./data:/data --device /dev/snd:/dev/snd ghcr.io/tphakala/birdnet-go:podman-latest"
LABEL usage.compose.docker="Use Docker/docker-compose.yml"
LABEL usage.compose.podman="Use Podman/podman-compose.yml"

# Add healthcheck to monitor container status
# Uses /health endpoint and validates JSON status via jq to avoid false positives
# from HTTP->HTTPS 308 redirects (curl -f treats 3xx as success).
# Extended start-period for low-power devices (e.g., Raspberry Pi)
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s --retries=3 \
    CMD curl -fs --connect-timeout 2 --max-time 3 http://localhost:8080/health | jq -e '.status == "healthy"' >/dev/null || curl -fsk --connect-timeout 2 --max-time 3 https://localhost:8443/health | jq -e '.status == "healthy"' >/dev/null || curl -fsk --connect-timeout 2 --max-time 3 https://localhost:443/health | jq -e '.status == "healthy"' >/dev/null || exit 1

# Container startup execution chain:
# 0. tini - A tiny init run as PID 1. This is defense-in-depth, not a bug fix:
#    the bash wrapper below already forwards signals and (via its SIGCHLD
#    handling) reaps processes reparented to PID 1, and birdnet-go Wait()s the
#    ffmpeg/sox children it spawns. tini makes correct PID 1 behaviour (orphan
#    reaping + signal handling) intrinsic to the image instead of relying on the
#    shell's semantics, so it stays correct across future entrypoint changes and
#    matches Docker's own --init default. Baked into the image so every deploy
#    path (docker/podman run, compose, quadlet, k8s) is covered without needing
#    --init / init: true, which on Podman would require catatonit on the host.
#    `-s` enables subreaper mode so it still reaps its subtree if a user also
#    passes --init (tini as PID 2).
#
# 1. entrypoint.sh - Sets up user permissions, timezone, device access, and performs
#    pre-flight checks (disk space, config writability). Handles both rootful and
#    rootless container modes. Exits early with clear error messages if checks fail.
#
# 2. startup-wrapper.sh - Wraps the application to capture output, detect errors,
#    and forward signals (SIGTERM/SIGINT) for graceful shutdown. Provides formatted
#    error messages with resolution steps if startup fails.
#
# 3. birdnet-go - The actual application (specified in CMD below)
#
# Environment variables affecting startup:
#   BIRDNET_UID / BIRDNET_GID        - User/group ID for file ownership (default: 1000)
#   BIRDNET_STARTUP_FAIL_DELAY       - Seconds to wait before exit on error (default: 10)
#   TZ                                - Timezone configuration (e.g., "America/Denver")
#   BIRDNET_MODELPATH                 - Optional custom model file path
#
# This layered approach ensures:
#   - Proper error visibility in container logs
#   - Clean signal handling for orchestration (Docker, Kubernetes)
#   - Early failure detection before wasting resources
#   - Actionable error messages for troubleshooting
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/usr/bin/entrypoint.sh", "/usr/bin/startup-wrapper.sh"]
CMD ["birdnet-go", "realtime"]
