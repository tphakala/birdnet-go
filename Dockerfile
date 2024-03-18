# Use ARGs to define default build-time variables for TensorFlow version and target platform
ARG TENSORFLOW_VERSION=v2.14.0

FROM golang:1.22.1-bookworm as build

# Pass in ARGs after FROM to use them in build stage
ARG TENSORFLOW_VERSION
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

WORKDIR /root/src

# Download TensorFlow headers
RUN git clone --branch ${TENSORFLOW_VERSION} --depth 1 https://github.com/tensorflow/tensorflow.git

# Compile BirdNET-Go
COPY . BirdNET-Go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd BirdNET-Go && make TARGETPLATFORM=${TARGETPLATFORM}

# Create final image using a multi-platform base image
FROM debian:bookworm-slim

# Install ALSA library and SOX
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libasound2 \
    ffmpeg \
    sox \
    ffmpeg \
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
