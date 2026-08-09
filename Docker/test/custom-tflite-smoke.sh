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
#   [model-path] should be an absolute path inside the image; the default is
#   /models/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite.
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

# Markers. The model-init line is "<message> ... model=<path>" (logfmt). We match the
# MESSAGE and the PATH on the SAME line rather than the exact "model=<path>" substring,
# so the check survives a log-encoder change (value quoting, JSON) and supports paths
# with spaces, while staying precise. Same-line coupling is load-bearing: the entrypoint
# echoes the path on its OWN line ("Custom model path configured: ...") and a secondary
# ONNX model (perch) logs the ONNX message with a DIFFERENT path, so a cross-line "path
# appears anywhere" check would false-pass (entrypoint echo) or false-fail (perch).
# SUCCESS: the TFLite init path logs this (internal/classifier/birdnet.go:368), mirrored
# to the console by logging.modules.birdnet.console_also (default true) so it reaches
# `docker logs`. The ONNX path logs a different message, so it discriminates the backend.
SUCCESS_MSG="BirdNET model initialized"
# FAIL (any of): the notflite stub hard-fail (internal/inference/tflite/stub_notflite.go),
# a model-init failure, or a startup abort. Static strings, matched as an ERE alternation.
FAIL_MARKERS="use an ONNX model|failed to initialize BirdNET|APPLICATION STARTUP FAILED|Cannot load settings"
# MISROUTE: OUR .tflite loaded via the ONNX backend logs this (internal/classifier/model_onnx.go:86).
ONNX_MISROUTE_MSG="ONNX model initialized"

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
    # Capture the message lines first (SIGPIPE-safe: never pipe grep into grep -q under
    # pipefail), then require OUR path on one of them, so message and path stay coupled
    # to the same line.
    init_lines=$(grep -F "$SUCCESS_MSG" <<<"$logs" || true)
    onnx_lines=$(grep -F "$ONNX_MISROUTE_MSG" <<<"$logs" || true)
    # Check failure first: a fail marker plus the success line (unlikely) should still fail.
    if grep -Eiq "$FAIL_MARKERS" <<<"$logs" || grep -qF "$MODEL_PATH" <<<"$onnx_lines"; then
        status="failed"
        break
    fi
    if grep -qF "$MODEL_PATH" <<<"$init_lines"; then
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
    echo "      expected '$SUCCESS_MSG' on the same log line as '$MODEL_PATH'"
    exit 1
fi

echo "PASS: custom .tflite loaded via the TFLite backend ($MODEL_PATH)"
