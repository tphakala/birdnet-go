ARG TFLITE_LIB_DIR=/usr/lib
ARG TENSORFLOW_VERSION=2.17.1
ARG TARGETPLATFORM=linux/amd64  # Default to linux/amd64 for local builds

FROM --platform=$TARGETPLATFORM golang:1.23.5-bookworm AS buildenv

# Install zip utility along with other dependencies
RUN apt-get update && apt-get install -y \
    curl \
    git \
    sudo \
    zip \
    npm \
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
COPY --chown=dev-user ./reset_auth.sh ./

# Download TensorFlow headers
RUN make check-tensorflow

# Download and configure precompiled TensorFlow Lite C library
RUN PLATFORM=$(echo ${TARGETPLATFORM} | tr '/' '_') && \
    echo "Building for platform: ${TARGETPLATFORM}" && \
    echo "Using library archive: tflite_c_v${TENSORFLOW_VERSION}_${PLATFORM}.tar.gz" && \
    make download-tflite TARGET=${PLATFORM}

FROM --platform=$TARGETPLATFORM buildenv AS build
WORKDIR /home/dev-user/src/BirdNET-Go

# First copy all source files
COPY --chown=dev-user . ./

# Then download assets and generate CSS with the input file available
RUN make download-assets
RUN make generate-tailwindcss

# Compile BirdNET-Go
ARG TARGETPLATFORM
RUN --mount=type=cache,target=/go/pkg/mod,uid=10001,gid=10001 \
    --mount=type=cache,target=/home/dev-user/.cache/go-build,uid=10001,gid=10001 \
    TARGET=$(echo ${TARGETPLATFORM} | tr '/' '_') && \
    make ${TARGET}

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

# Include reset_auth tool from build stage
COPY --from=build /home/dev-user/src/BirdNET-Go/reset_auth.sh /usr/bin/
RUN chmod +x /usr/bin/reset_auth.sh

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
