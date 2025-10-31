ARG TFLITE_LIB_DIR=/usr/lib
ARG TENSORFLOW_VERSION=2.17.1

FROM --platform=$BUILDPLATFORM golang:1.25.3-trixie AS buildenv

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

# Enter Build stage
FROM --platform=$BUILDPLATFORM buildenv AS build
ARG BUILD_VERSION
ENV BUILD_VERSION=${BUILD_VERSION:-unknown}

ARG TARGETPLATFORM

# Skip puppeteer download during build (not needed for production)
ENV PUPPETEER_SKIP_DOWNLOAD=true

# Build assets and compile BirdNET-Go (non-embedded build)
RUN --mount=type=cache,target=/go/pkg/mod,uid=10001,gid=10001 \
    --mount=type=cache,target=/home/dev-user/.cache/go-build,uid=10001,gid=10001 \
    task check-tensorflow && \
    task download-assets && \
    task generate-tailwindcss && \
    TARGET=$(echo ${TARGETPLATFORM} | tr '/' '_') && \
    echo "Building non-embedded version with BUILD_VERSION=${BUILD_VERSION}" && \
    BUILD_VERSION="${BUILD_VERSION}" DOCKER_LIB_DIR=/home/dev-user/lib task noembed_${TARGET}

# Create final image using a multi-platform base image
FROM --platform=$TARGETPLATFORM debian:trixie-slim

# Copy model files to /models directory as separate cacheable layer
# This layer will be reused if model files haven't changed between builds
RUN mkdir -p /models
COPY --from=build /home/dev-user/src/BirdNET-Go/internal/birdnet/data/*.tflite /models/
# Set read permissions for model files
RUN chmod -R a+r /models/*.tflite 2>/dev/null || true
# Ensure directory is executable (browsable)
RUN chmod a+x /models

# Install ALSA library and SOX for audio processing, and other system utilities for debugging
RUN apt-get update -q && apt-get install -q -y --no-install-recommends \
    adduser \
    ca-certificates \
    libasound2 \
    ffmpeg \
    sox \
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

# Set TFLITE_LIB_DIR based on architecture
ARG TARGETPLATFORM
ARG TFLITE_LIB_DIR
RUN if [ "$TARGETPLATFORM" = "linux/arm64" ]; then \
        export TFLITE_LIB_DIR=/usr/aarch64-linux-gnu/lib; \
    else \
        export TFLITE_LIB_DIR=/usr/lib; \
    fi && \
    echo "Using TFLITE_LIB_DIR=$TFLITE_LIB_DIR"

# Copy TensorFlow Lite library from build stage
COPY --from=build /home/dev-user/lib/libtensorflowlite_c.so* ${TFLITE_LIB_DIR}/
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
LABEL org.opencontainers.image.description="Real-time bird sound identification using BirdNET"
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
# Extended start-period for low-power devices (e.g., Raspberry Pi)
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s --retries=3 \
    CMD curl -f http://localhost:8080/ || exit 1

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