# Use ARGs to define default build-time variables for TensorFlow version and target platform
ARG TENSORFLOW_VERSION=v2.14.0

FROM --platform=$BUILDPLATFORM golang:1.22.3-bookworm as buildenv

# Install zip utility along with other dependencies
RUN apt-get update && apt-get install -y \
    curl \
    git \
    sudo \
    zip \
    gcc-aarch64-linux-gnu \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /root/src

ARG TENSORFLOW_VERSION

# Download TensorFlow headers
RUN git clone --branch ${TENSORFLOW_VERSION} --filter=blob:none --depth 1 --no-checkout https://github.com/tensorflow/tensorflow.git \
    && git -C tensorflow config core.sparseCheckout true \
    && echo "**/*.h" >> tensorflow/.git/info/sparse-checkout \
    && git -C tensorflow checkout

ARG TARGETPLATFORM

# Determine PLATFORM based on TARGETPLATFORM
RUN PLATFORM='unknown'; \
    case "${TARGETPLATFORM}" in \
        "linux/amd64") PLATFORM='linux_amd64' ;; \
        "linux/arm64") PLATFORM='linux_arm64' ;; \
        *) echo "Unsupported platform: '${TARGETPLATFORM}'" && exit 1 ;; \
    esac; \
# Download and configure precompiled TensorFlow Lite C library for the determined platform
    curl -L \
    "https://github.com/tphakala/tflite_c/releases/download/${TENSORFLOW_VERSION}/tflite_c_${TENSORFLOW_VERSION}_${PLATFORM}.tar.gz" | \
    tar -C "/usr/local/lib" -xz \
    && ldconfig

FROM --platform=$BUILDPLATFORM buildenv as build

# Compile BirdNET-Go
COPY . BirdNET-Go
ARG TARGETPLATFORM
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    PLATFORM='unknown'; \
    case "${TARGETPLATFORM}" in \
        "linux/amd64") PLATFORM='linux_amd64' ;; \
        "linux/arm64") PLATFORM='linux_arm64' ;; \
        *) echo "Unsupported platform: '${TARGETPLATFORM}'" && exit 1 ;; \
    esac; \
    cd BirdNET-Go && make ${PLATFORM}

# Create final image using a multi-platform base image
FROM debian:bookworm-slim

# Install ALSA library and SOX
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libasound2 \
    ffmpeg \
    sox \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /usr/local/lib/libtensorflowlite_c.so /usr/local/lib/
RUN ldconfig

# Add symlink to /config directory where configs can be stored
VOLUME /config
RUN mkdir -p /root/.config && ln -s /config /root/.config/birdnet-go

VOLUME /data
WORKDIR /data

# Make port 8080 available to the world outside this container
EXPOSE 8080

COPY --from=build /root/src/BirdNET-Go/bin /usr/bin/

ENTRYPOINT ["/usr/bin/birdnet-go"]
CMD ["realtime"]
