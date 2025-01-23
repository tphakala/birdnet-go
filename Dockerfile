ARG TFLITE_LIB_DIR=/usr/lib
ARG TENSORFLOW_VERSION=2.17.1
ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG VERSION=unknown

FROM --platform=$TARGETPLATFORM golang:1.23.5-bookworm AS build

# Pass VERSION through to the build stage
ARG VERSION
ENV VERSION=$VERSION

# Install Task and other dependencies
RUN apt-get update -q && apt-get install -q -y \
    curl \
    git \
    sudo \
    zip \
    gcc-aarch64-linux-gnu

# Install python3-six
RUN apt-get install -q -y python3-six --no-install-recommends

# Install npm
RUN apt-get install -q -y npm --no-install-recommends

# Clean up apt cache
RUN rm -rf /var/lib/apt/lists/*

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
    chown -R dev-user:dev-user /home/dev-user

USER dev-user
WORKDIR /home/dev-user/src/BirdNET-Go

# Copy all source files first to have Git information available
COPY --chown=dev-user . ./

# Now run the tasks with VERSION env var
RUN task check-tensorflow && \
    task download-assets && \
    task generate-tailwindcss

# Compile BirdNET-Go
ARG TARGETPLATFORM
RUN --mount=type=cache,target=/go/pkg/mod,uid=10001,gid=10001 \
    --mount=type=cache,target=/home/dev-user/.cache/go-build,uid=10001,gid=10001 \
    TARGET=$(echo ${TARGETPLATFORM} | tr '/' '_') && \
    DOCKER_LIB_DIR=/home/dev-user/lib VERSION=${VERSION} task ${TARGET}

# Create final image using a multi-platform base image
FROM debian:bookworm-slim

# Install ALSA library and SOX
RUN apt-get update -q && apt-get install -q -y --no-install-recommends \
    ca-certificates \
    libasound2 \
    ffmpeg \
    sox

# Clean up apt cache
RUN rm -rf /var/lib/apt/lists/*

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
