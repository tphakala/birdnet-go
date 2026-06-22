#!/usr/bin/env bash
#
# Smoke test: the container must start as an arbitrary, non-root UID with no
# /etc/passwd entry (K8s runAsNonRoot / OpenShift) WITHOUT the operator setting
# HOME or BIRDNET_UID/BIRDNET_GID.
#
# This guards two regression classes that previously broke rootless startup:
#   1. Entrypoint writing /etc/localtime under `set -e` (timezone block) — fails
#      with "Permission denied" for non-root users.
#   2. Config resolution falling back to HOME=/ → `mkdir /.config: permission
#      denied` → "Cannot load settings" → startup abort.
#
# Usage: Docker/test/arbitrary-uid-smoke.sh <image-ref>
#
# Notes on the environment (this is the part that matters):
#   --user 568:568   arbitrary UID/GID with no passwd entry (do NOT pass
#                    BIRDNET_UID/GID, and do NOT set HOME — that would mask
#                    the bug this test exists to catch).
#   --tmpfs mounts   writable by any UID, the docker equivalent of a K8s
#                    emptyDir + fsGroup. A host bind mount owned by another UID
#                    would fail the entrypoint's writability preflight for an
#                    unrelated reason. /data needs >=1GB for the preflight.
set -euo pipefail

IMAGE="${1:?usage: arbitrary-uid-smoke.sh <image-ref>}"
UID_GID="${SMOKE_UID_GID:-568:568}"
TIMEOUT_SECONDS="${SMOKE_TIMEOUT:-90}"

# Markers, kept in one place so assertions and docs stay in sync.
START_MARKER="BirdNET-Go starting"
FAIL_MARKERS="APPLICATION STARTUP FAILED|Cannot load settings|permission denied|/etc/localtime.*Permission denied"

echo "==> Starting '$IMAGE' as --user $UID_GID (no HOME, no BIRDNET_UID)"
cid=$(docker run -d \
    --user "$UID_GID" \
    --tmpfs /config:size=64m \
    --tmpfs /data:size=1200m \
    -e TZ=Europe/Helsinki \
    "$IMAGE")
cleanup() { docker rm -f "$cid" >/dev/null 2>&1 || true; }
trap cleanup EXIT

status="timeout"
deadline=$((SECONDS + TIMEOUT_SECONDS))
while [ "$SECONDS" -lt "$deadline" ]; do
    logs=$(docker logs "$cid" 2>&1 || true)
    if grep -q "$START_MARKER" <<<"$logs"; then
        status="ok"
        break
    fi
    if grep -Eiq "$FAIL_MARKERS" <<<"$logs"; then
        status="failed"
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
    echo "FAIL: container did not reach startup as an arbitrary UID (status=$status)"
    exit 1
fi

# Config must resolve under the writable /config mount, not /.
# Capture logs into a variable rather than piping docker logs directly into
# grep -q: with pipefail, grep -q exits on first match → closes the pipe →
# docker logs receives SIGPIPE (exit 141) → pipeline returns non-zero even
# though grep matched. Using a here-string avoids the SIGPIPE race entirely.
final_logs=$(docker logs "$cid" 2>&1 || true)
if ! grep -Eq "config_file=/config(/|[[:space:]]|$)" <<<"$final_logs"; then
    echo "FAIL: config_file did not resolve under /config (HOME fallback regressed?)"
    exit 1
fi

echo "PASS: started as $UID_GID with no HOME override; config resolved under /config"
