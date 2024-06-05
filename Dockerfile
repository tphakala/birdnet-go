ARG TFLITE_LIB_DIR=/usr/lib

FROM --platform=$BUILDPLATFORM golang:1.22.3-bookworm as buildenv

# Install zip utility along with other dependencies
RUN apt-get update && apt-get install -y \
    curl \
    git \
    sudo \
    zip \
    gcc-aarch64-linux-gnu \
    && rm -rf /var/lib/apt/lists/*

# Create dev-user for building and devcontainer usage
RUN groupadd --gid 10001 dev-user; \
    useradd --uid 10001 --gid dev-user --shell /bin/bash --create-home dev-user; \
    usermod -aG sudo dev-user; \
    usermod -aG audio dev-user; \
    echo '%sudo ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers
USER dev-user

WORKDIR /home/dev-user/src/BirdNET-Go

COPY --chown=dev-user ./Makefile ./

# Download TensorFlow headers
ARG TENSORFLOW_VERSION
RUN make check-tensorflow

# Download and configure precompiled TensorFlow Lite C library
ARG TARGETPLATFORM
ARG TFLITE_LIB_DIR
RUN TFLITE_LIB_ARCH=$(echo ${TARGETPLATFORM} | tr '/' '_').tar.gz \
    make download-tflite

FROM --platform=$BUILDPLATFORM buildenv as build
WORKDIR /home/dev-user/src/BirdNET-Go

# Compile BirdNET-Go
COPY --chown=dev-user . ./
ARG TARGETPLATFORM
RUN --mount=type=cache,target=/go/pkg/mod,uid=10001,gid=10001 \
    --mount=type=cache,target=/home/dev-user/.cache/go-build,uid=10001,gid=10001 \
    make $(echo ${TARGETPLATFORM} | tr '/' '_')

# Create final image using a multi-platform base image
FROM debian:bookworm-slim

# Install ALSA library and SOX
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libasound2 \
    ffmpeg \
    sox \
    && rm -rf /var/lib/apt/lists/*

ARG TFLITE_LIB_DIR
COPY --from=build ${TFLITE_LIB_DIR}/libtensorflowlite_c.so ${TFLITE_LIB_DIR}
RUN ldconfig

# Add symlink to /config directory where configs can be stored
VOLUME /config
RUN mkdir -p /root/.config && ln -s /config /root/.config/birdnet-go

VOLUME /data
WORKDIR /data

# Make port 8080 available to the world outside this container
EXPOSE 8080

COPY --from=build /home/dev-user/src/BirdNET-Go/bin /usr/bin/

ENTRYPOINT ["/usr/bin/birdnet-go"]
CMD ["realtime"]
