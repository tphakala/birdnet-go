FROM golang:1.22.0-bookworm as build

ARG TENSORFLOW_VERSION=v2.14.0
ARG PLATFORM=linux_amd64

# Download and configure precompiled TensorFlow Lite C library
RUN curl -L \
    https://github.com/tphakala/tflite_c/releases/download/${TENSORFLOW_VERSION}/tflite_c_${TENSORFLOW_VERSION}_${PLATFORM}.tar.gz | \
    tar -C "/usr/local/lib" -xz \
    && ldconfig

WORKDIR /root/src

# Download TensorFlow headers
RUN git clone --branch ${TENSORFLOW_VERSION} --depth 1 https://github.com/tensorflow/tensorflow.git

# Compile BirdNET-GO
COPY . BirdNET-Go
RUN cd BirdNET-Go && make

# Create final image
FROM debian:bookworm-slim
COPY --from=build /root/src/BirdNET-Go/bin /usr/bin/
COPY --from=build /usr/local/lib/libtensorflowlite_c.so /usr/local/lib/libtensorflowlite_c.so
RUN ldconfig

ENTRYPOINT ["/usr/bin/birdnet-go"]
CMD ["realtime"]
