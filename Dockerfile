ARG TENSORFLOW_VERSION=2.17.1
ARG ONNXRUNTIME_VERSION=1.25.1
# OpenVINO toolkit pin for the runtime libraries bundled into the images. Keep the
# release/build in sync with the Taskfile OPENVINO_RELEASE / OPENVINO_BUILD values;
# the SHA256s are per-arch (arm64 matches the Taskfile OPENVINO_SHA256 header pin).
ARG OPENVINO_RELEASE=2026.2
ARG OPENVINO_BUILD=2026.2.0.21903.52ddc073857
ARG OPENVINO_SHA256_AMD64=86896e9347cd160370d16f80fa2c49c2b7a51ec33b55cea6493c7dc7c4c61c55
ARG OPENVINO_SHA256_ARM64=8ce45467967e22fddb83a6b72a8bd1f9bfa6f43351e1ca2eaf5251064fe17767

FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS buildenv

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
ARG ONNXRUNTIME_VERSION

# Download ONNX Runtime for the target platform
RUN ONNX_ARCH=$(case "${TARGETPLATFORM}" in \
        "linux/amd64") echo "x64" ;; \
        "linux/arm64") echo "aarch64" ;; \
        *) echo "Error: unsupported platform ${TARGETPLATFORM}" >&2; exit 1 ;; \
    esac) && \
    echo "Downloading ONNX Runtime ${ONNXRUNTIME_VERSION} for ${ONNX_ARCH}" && \
    curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-${ONNX_ARCH}-${ONNXRUNTIME_VERSION}.tgz" \
        -o /tmp/onnxruntime.tgz && \
    mkdir -p /tmp/onnxruntime && \
    tar -xzf /tmp/onnxruntime.tgz -C /tmp/onnxruntime --strip-components=1 && \
    cp /tmp/onnxruntime/lib/libonnxruntime*.so* /home/dev-user/lib/ && \
    rm -rf /tmp/onnxruntime /tmp/onnxruntime.tgz

# OpenVINO runtime provisioning for the target platform. One toolkit download
# serves two purposes: (1) the arch-independent C API headers are staged into the
# Taskfile OpenVINO header cache (.cache/openvino) with a matching .build-id stamp,
# so the check-openvino task treats them as up-to-date and does not re-download
# during the build; (2) the arch-specific runtime .so set is staged into
# /home/dev-user/lib/openvino for the final image. Only the libraries the backend
# needs are kept (core, C API, ONNX + IR frontends, the arch CPU plugin, the amd64
# iGPU plugin, and TBB); the unused frontends and plugins are dropped to save size.
# The SHA256 is verified per arch before extraction (a moved or tampered upstream
# archive can still return HTTP 200).
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
    echo "Downloading OpenVINO ${OPENVINO_BUILD} (${OV_SUFFIX})"; \
    curl -fsSL "${OV_URL}" -o /tmp/openvino.tgz; \
    echo "${OV_SHA256}  /tmp/openvino.tgz" | sha256sum -c -; \
    mkdir -p /tmp/ov; \
    tar -xzf /tmp/openvino.tgz -C /tmp/ov --strip-components=1 \
        "${OV_BASE}/runtime/include" \
        "${OV_BASE}/runtime/lib/${OV_LIBDIR}" \
        "${OV_BASE}/runtime/3rdparty/tbb/lib"; \
    mkdir -p .cache/openvino; \
    rm -rf .cache/openvino/include; \
    cp -a /tmp/ov/runtime/include .cache/openvino/include; \
    test -f .cache/openvino/include/openvino/c/openvino.h; \
    printf '%s\n' "${OPENVINO_BUILD}" > .cache/openvino/.build-id; \
    mkdir -p /home/dev-user/lib/openvino; \
    OV_SRC="/tmp/ov/runtime/lib/${OV_LIBDIR}"; \
    cp -a "${OV_SRC}"/libopenvino.so* /home/dev-user/lib/openvino/; \
    cp -a "${OV_SRC}"/libopenvino_c.so* /home/dev-user/lib/openvino/; \
    cp -a "${OV_SRC}"/libopenvino_onnx_frontend.so* /home/dev-user/lib/openvino/; \
    cp -a "${OV_SRC}"/libopenvino_ir_frontend.so* /home/dev-user/lib/openvino/; \
    cp -a "${OV_SRC}/${OV_CPU_PLUGIN}"* /home/dev-user/lib/openvino/; \
    if [ -n "${OV_GPU_PLUGIN}" ]; then cp -a "${OV_SRC}/${OV_GPU_PLUGIN}"* /home/dev-user/lib/openvino/; fi; \
    find /tmp/ov/runtime/3rdparty/tbb/lib -name '*.so*' -exec cp -a {} /home/dev-user/lib/openvino/ \; ; \
    test -e /home/dev-user/lib/openvino/libtbb.so.12; \
    rm -rf /tmp/openvino.tgz /tmp/ov; \
    echo "Staged OpenVINO runtime libraries: $(ls /home/dev-user/lib/openvino | wc -l) entries"

# Build assets and compile BirdNET-Go (non-embedded, TFLite/ONNX + native OpenVINO).
# The noembed_linux_* targets default OPENVINO=true, and the runtime libraries
# staged above ship in the final image so the backend can dlopen libopenvino_c.
# The backend self-gates (non-A76 arm64, or amd64 without Intel GPU drivers) and
# falls back to ONNX Runtime, so enabling it here is safe for every deployment.
# OPENVINO_BUILD is passed through to the task so the check-openvino header-cache
# guard compares against the SAME build id this stage staged into .cache/openvino
# above. That makes the guard a guaranteed no-op (no re-download) and keeps the
# compiled-against headers and the shipped runtime .so set on one version even if
# the Taskfile default and this Dockerfile ARG ever drift.
# Note: frontend-build (including Tailwind) is handled as a dependency of noembed_* tasks
RUN --mount=type=cache,target=/go/pkg/mod,uid=10001,gid=10001 \
    --mount=type=cache,target=/home/dev-user/.cache/go-build,uid=10001,gid=10001 \
    task check-tensorflow && \
    TARGET=$(echo ${TARGETPLATFORM} | tr '/' '_') && \
    echo "Building non-embedded version with BUILD_VERSION=${BUILD_VERSION}" && \
    BUILD_VERSION="${BUILD_VERSION}" SENTRY_DSN="${SENTRY_DSN}" PROJECT_NAME="${PROJECT_NAME}" PROJECT_REPO_URL="${PROJECT_REPO_URL}" PROJECT_COMMUNITY_URL="${PROJECT_COMMUNITY_URL}" DOCKER_LIB_DIR=/home/dev-user/lib task noembed_${TARGET} OPENVINO_BUILD="${OPENVINO_BUILD}"

# Create final image using a multi-platform base image
FROM --platform=$TARGETPLATFORM debian:trixie-slim

# Copy model files to /models. arm64 ships ONNX-only (issue #1103); other arches
# ship the TFLite models. Stage all candidates in one cacheable layer, then keep
# only the set for the target architecture.
RUN mkdir -p /models /tmp/allmodels
COPY --from=build /home/dev-user/src/BirdNET-Go/internal/classifier/data/*.tflite /tmp/allmodels/
COPY --from=build /home/dev-user/src/BirdNET-Go/internal/classifier/data/*.onnx /tmp/allmodels/
ARG TARGETPLATFORM
RUN if [ "$TARGETPLATFORM" = "linux/arm64" ]; then \
        cp /tmp/allmodels/*.onnx /models/; \
    else \
        cp /tmp/allmodels/*.tflite /models/; \
    fi && \
    chmod -R a+r /models/ && \
    chmod a+x /models && \
    rm -rf /tmp/allmodels

# Install ALSA library and SOX for audio processing, and other system utilities for debugging
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
    && rm -rf /var/lib/apt/lists/*

# Copy ONNX Runtime libraries (used by all arches; arm64 relies on them exclusively).
# --chown=root:root because COPY --from preserves the build-stage owner (dev-user,
# UID 10001); system libraries must be root-owned so a runtime process cannot
# overwrite them if it happens to share that UID.
COPY --chown=root:root --from=build /home/dev-user/lib/libonnxruntime*.so* /usr/lib/

# TensorFlow Lite C library: installed for non-arm64 only. arm64 is ONNX-only
# (issue #1103) and the binary does not link libtensorflowlite_c. Stage it, then
# install per arch.
ARG TARGETPLATFORM
COPY --from=build /home/dev-user/lib/libtensorflowlite_c.so* /tmp/tflite-lib/
RUN if [ "$TARGETPLATFORM" != "linux/arm64" ]; then \
        cp /tmp/tflite-lib/libtensorflowlite_c.so* /usr/lib/; \
    fi && \
    rm -rf /tmp/tflite-lib && \
    ldconfig

# OpenVINO runtime libraries (both arches). The openvino-tagged binary dlopens
# libopenvino_c at runtime and self-gates: a non-A76 arm64 CPU, or an amd64 host
# without Intel GPU drivers, falls back to ONNX Runtime cleanly. The staging dir
# holds the arch-appropriate set (see the build stage), with symlinks preserved so
# the bare-soname dlopen resolves; ldconfig then refreshes the loader cache.
# --chown=root:root because COPY --from preserves the build-stage owner (dev-user,
# UID 10001); system libraries must be root-owned (see the ONNX Runtime copy above).
COPY --chown=root:root --from=build /home/dev-user/lib/openvino/ /usr/lib/
RUN ldconfig

# Include reset_auth tool from build stage
COPY --from=build /home/dev-user/src/BirdNET-Go/reset_auth.sh /usr/bin/
RUN chmod +x /usr/bin/reset_auth.sh

# Add entrypoint script for dynamic user creation
COPY --from=build /home/dev-user/src/BirdNET-Go/Docker/entrypoint.sh /usr/bin/
RUN chmod +x /usr/bin/entrypoint.sh

# Add startup wrapper script for error capture and display
COPY --from=build /home/dev-user/src/BirdNET-Go/Docker/startup-wrapper.sh /usr/bin/
RUN chmod +x /usr/bin/startup-wrapper.sh

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
ENTRYPOINT ["/usr/bin/entrypoint.sh", "/usr/bin/startup-wrapper.sh"]
CMD ["birdnet-go", "realtime"]
