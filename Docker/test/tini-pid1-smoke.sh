#!/usr/bin/env bash
#
# Smoke test: tini must be PID 1 in the container and must reap orphaned
# grandchildren. The image bakes tini in as the outermost ENTRYPOINT so correct
# PID 1 behaviour (orphan reaping + signal handling) is intrinsic to the image
# on every runtime (docker/podman run, compose, quadlet, k8s) without needing
# --init / init: true. This guards two regression classes:
#   1. The ENTRYPOINT losing its `tini -s --` prefix (PID 1 becomes the shell).
#   2. The image dropping the tini package.
#
# Usage: Docker/test/tini-pid1-smoke.sh <image-ref>
#
# Parsing (awk/grep) runs on the host, not in the container, so the image only
# needs cat/ps/bash/sleep (all present); the runner supplies awk.
set -euo pipefail

IMAGE="${1:?usage: tini-pid1-smoke.sh <image-ref>}"
TIMEOUT_SECONDS="${SMOKE_TIMEOUT:-60}"

# Run normally (root entrypoint -> gosu drop -> wrapper -> app), the default
# deployment path. tmpfs mounts are writable by any UID and give /data the
# >=1GB the entrypoint preflight requires.
echo "==> Starting '$IMAGE'"
cid=$(docker run -d \
    --tmpfs /config:size=64m \
    --tmpfs /data:size=1200m \
    -e TZ=UTC \
    "$IMAGE")
cleanup() { docker rm -f "$cid" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Wait until the container is running and PID 1 is readable. tini is PID 1 from
# the start (it is the entrypoint), so this does not need a full app boot.
pid1=""
deadline=$((SECONDS + TIMEOUT_SECONDS))
while [ "$SECONDS" -lt "$deadline" ]; do
    state=$(docker inspect -f '{{.State.Status}}' "$cid" 2>/dev/null || echo unknown)
    if [ "$state" = "running" ]; then
        # cat may lose an exec race while the container is still coming up, so
        # tolerate a transient failure here and retry on the next tick.
        pid1=$(docker exec "$cid" cat /proc/1/comm 2>/dev/null || true)
        [ -n "$pid1" ] && break
    elif [ "$state" = "exited" ] || [ "$state" = "dead" ]; then
        echo "----- container logs (tail) -----"
        docker logs "$cid" 2>&1 | tail -30
        echo "---------------------------------"
        echo "FAIL: container exited prematurely (status=$state) before PID 1 could be checked"
        exit 1
    fi
    sleep 1
done

echo "==> PID 1 comm: '${pid1:-<none>}'"
if [ "$pid1" != "tini" ]; then
    echo "----- container logs (tail) -----"
    docker logs "$cid" 2>&1 | tail -30
    echo "---------------------------------"
    echo "FAIL: PID 1 is '${pid1:-<none>}', expected 'tini'"
    exit 1
fi

# Reaping: orphan a grandchild to PID 1 (the `( sleep 1 & )` subshell exits at
# once, reparenting the sleep to PID 1) and confirm tini reaps it after it dies,
# leaving no <defunct> zombie.
# No 2>/dev/null || true here: the container is confirmed running, so a failure
# to inject the orphan is a real error that must fail the test loudly rather than
# silently pass the reaping check with no orphan ever created.
docker exec "$cid" bash -c '( sleep 1 & )'
sleep 3
zombies=$(docker exec "$cid" ps -eo stat,pid,ppid,comm | awk 'NR>1 && $1 ~ /^Z/')
if [ -n "$zombies" ]; then
    echo "FAIL: zombie process present after the orphan exited (tini did not reap):"
    echo "$zombies"
    exit 1
fi

echo "PASS: tini is PID 1 and reaped an orphaned grandchild (no zombies)"
