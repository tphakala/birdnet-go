#!/usr/bin/env bash
#
# Smoke test: a user-supplied custom TensorFlow Lite classifier model
# (birdnet.modelpath / BIRDNET_MODELPATH pointing at a .tflite file) must load
# via the TFLite backend inside the container image.
#
# This guards the regression class that PR #3536/#3544 (arm64 ONNX-only, notflite
# stub) reintroduced and PR #3864 fixed for arm64: a container that no longer links
# libtensorflowlite_c (or misroutes a .tflite to the ONNX backend) hard-fails a
# custom .tflite with "use an ONNX model". The amd64 image has always shipped TFLite,
# so this asserts that stays true end-to-end: the shipped libtensorflowlite_c actually
# dlopens at runtime AND the classifier selection routes a .tflite modelpath to the
# TFLite backend. Selection is unit-tested (TestCustomBirdNETV24ModelInfo,
# TestUsesONNXBackend, TestRemapV24ToONNXOnARM64); this covers the runtime/packaging
# dimension the unit tests cannot.
#
# The default model is a stock .tflite the amd64 image already ships in /models, so no
# extra CI asset is needed. It is loaded via BIRDNET_MODELPATH exactly as a real user's
# custom model would be: customBirdNETV24ModelInfo marks it IsStock=false, sets
# Backend=TFLite from the extension, and initializeTFLiteModel loads it from the path.
#
# Usage: Docker/test/custom-tflite-smoke.sh <image-ref> [model-path]
#   [model-path] should be a space-free absolute path inside the image (the log
#   encoder quotes values containing whitespace, which the literal marker match
#   would then miss); the default is space-free.
#
# The docker run/poll/cleanup harness mirrors arbitrary-uid-smoke.sh. Kept as a
# separate copy (not a shared helper) while there are only two smoke tests with
# divergent markers and check ordering; factor out a common lib if a third lands.
#
# Notes on the environment:
#   -e BIRDNET_MODELPATH   the custom model path; overrides birdnet.modelpath (env.go
#                          binds BIRDNET_MODELPATH -> birdnet.modelpath).
#   --tmpfs mounts         writable by any UID (K8s emptyDir equivalent). /data needs
#                          >=1GB for the entrypoint's disk preflight.
set -euo pipefail

IMAGE="${1:?usage: custom-tflite-smoke.sh <image-ref> [model-path]}"
# A stock classifier .tflite the amd64 image ships in /models (see Dockerfile). Any
# .tflite path exercises the same Tier-3 custom-model -> TFLite load path.
MODEL_PATH="${2:-/models/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite}"
TIMEOUT_SECONDS="${SMOKE_TIMEOUT:-90}"

# Markers, kept in one place so a production log refactor updates both sides.
# SUCCESS: the TFLite init path logs this (internal/classifier/birdnet.go:368). The
# birdnet module mirrors model-init to the console (logging.modules.birdnet.console_also
# defaults to true), so it reaches `docker logs`. It carries the model path
# (birdnet.go:360-362), so it also proves the CUSTOM path was taken, not the stock
# default (which logs a version string). Matched literally (grep -F): the path holds
# regex metacharacters, and a space-bearing path would be quoted by the log encoder.
SUCCESS_MARKER="BirdNET model initialized model=${MODEL_PATH}"
# FAIL (any of): the notflite stub hard-fail (internal/inference/tflite/stub_notflite.go),
# a model-init failure, or a startup abort. Static strings, matched as an ERE alternation.
FAIL_MARKERS="use an ONNX model|failed to initialize BirdNET|APPLICATION STARTUP FAILED|Cannot load settings"
# The .tflite misrouted to the ONNX backend logs this (internal/classifier/model_onnx.go:86)
# with OUR model path. Matched literally (grep -F, not folded into the ERE above) so a
# path with regex metacharacters cannot corrupt the pattern, and so a legitimately-loaded
# secondary ONNX model (e.g. perch) with a different path does not false-trip it.
ONNX_MISROUTE_MARKER="ONNX model initialized model=${MODEL_PATH}"

echo "==> Starting '$IMAGE' with BIRDNET_MODELPATH=$MODEL_PATH"
cid=$(docker run -d \
    -e BIRDNET_MODELPATH="$MODEL_PATH" \
    -e TZ=Europe/Helsinki \
    --tmpfs /config:size=64m \
    --tmpfs /data:size=1200m \
    "$IMAGE")
cleanup() { docker rm -f "$cid" >/dev/null 2>&1 || true; }
trap cleanup EXIT

status="timeout"
deadline=$((SECONDS + TIMEOUT_SECONDS))
while [ "$SECONDS" -lt "$deadline" ]; do
    logs=$(docker logs "$cid" 2>&1 || true)
    # Check failure first: a fail marker plus the success line (unlikely) should still fail.
    if grep -Eiq "$FAIL_MARKERS" <<<"$logs" || grep -qF "$ONNX_MISROUTE_MARKER" <<<"$logs"; then
        status="failed"
        break
    fi
    if grep -qF "$SUCCESS_MARKER" <<<"$logs"; then
        status="ok"
        break
    fi
    if [ "$(docker inspect -f '{{.State.Running}}' "$cid" 2>/dev/null || echo false)" != "true" ]; then
        status="exited"
        break
    fi
    sleep 2
done

echo "----- container logs (tail) -----"
docker logs "$cid" 2>&1 | tail -40
echo "---------------------------------"

if [ "$status" != "ok" ]; then
    echo "FAIL: custom .tflite ($MODEL_PATH) did not load via the TFLite backend (status=$status)"
    echo "      expected log marker: '$SUCCESS_MARKER'"
    exit 1
fi

echo "PASS: custom .tflite loaded via the TFLite backend ($MODEL_PATH)"
