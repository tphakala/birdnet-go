#!/usr/bin/env bash
#
# Smoke test: the image HEALTHCHECK must follow BIRDNET_WEBSERVER_PORT.
#
# Host-networking deployments (Docker/docker-compose.host.yml, the Unraid host
# template) set the web port through BIRDNET_WEBSERVER_PORT instead of a port
# mapping. The health check must then probe that port, and must not fall back
# to 8080, where under host networking another service on the host could answer.
# An invalid value is ignored by the app, so the check must fall back to 8080.
#
# The test runs the image's own HEALTHCHECK command (read with docker inspect)
# through docker exec, so it checks exactly what Docker runs, without waiting
# for the 30 s health interval.
#
# Usage: Docker/test/healthcheck-port-smoke.sh <image-ref>
#
# Environment notes: same as arbitrary-uid-smoke.sh (arbitrary UID, tmpfs
# /config and /data, /data >= 1 GB for the entrypoint preflight).
set -euo pipefail

IMAGE="${1:?usage: healthcheck-port-smoke.sh <image-ref>}"
UID_GID="${SMOKE_UID_GID:-568:568}"
TIMEOUT_SECONDS="${SMOKE_TIMEOUT:-120}"
CUSTOM_PORT=18080
UNUSED_PORT=18081

HC_CMD=$(docker inspect -f '{{index .Config.Healthcheck.Test 1}}' "$IMAGE")
if [ -z "$HC_CMD" ]; then
    echo "FAIL: image has no HEALTHCHECK command"
    exit 1
fi

cids=()
cleanup() {
    for c in "${cids[@]}"; do docker rm -f "$c" >/dev/null 2>&1 || true; done
}
trap cleanup EXIT

start() { # [docker run args...]
    docker run -d --user "$UID_GID" \
        --tmpfs /config:size=64m --tmpfs /data:size=1200m \
        "$@" "$IMAGE"
}

# check <cid> [docker exec args...]: run the HEALTHCHECK command once.
check() {
    local cid="$1"
    shift
    docker exec "$@" "$cid" sh -c "$HC_CMD" >/dev/null 2>&1
}

wait_healthy() { # <cid> <label>
    local deadline=$((SECONDS + TIMEOUT_SECONDS))
    while [ "$SECONDS" -lt "$deadline" ]; do
        if check "$1"; then
            return 0
        fi
        if [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null || echo false)" != "true" ]; then
            break
        fi
        sleep 2
    done
    echo "FAIL: $2: health check never passed"
    docker logs "$1" 2>&1 | tail -40
    exit 1
}

fails=0
expect() { # <description> <want: pass|fail> <cid> [docker exec args...]
    local desc="$1" want="$2" cid="$3"
    shift 3
    local got=fail
    if check "$cid" "$@"; then got=pass; fi
    if [ "$got" = "$want" ]; then
        echo "ok:   $desc ($got)"
    else
        echo "FAIL: $desc (want $want, got $got)"
        fails=$((fails + 1))
    fi
}

echo "==> Default port: app on 8080, BIRDNET_WEBSERVER_PORT unset"
default_cid=$(start)
cids+=("$default_cid")
wait_healthy "$default_cid" "default port"
expect "unset variable probes 8080" pass "$default_cid"
expect "invalid value falls back to 8080" pass "$default_cid" -e BIRDNET_WEBSERVER_PORT=abc
expect "out-of-range value falls back to 8080" pass "$default_cid" -e BIRDNET_WEBSERVER_PORT=70000
expect "inner space is invalid, falls back to 8080" pass "$default_cid" -e "BIRDNET_WEBSERVER_PORT=18 080"
expect "custom port is not answered by 8080" fail "$default_cid" -e "BIRDNET_WEBSERVER_PORT=$UNUSED_PORT"

echo "==> Custom port: app on $CUSTOM_PORT via BIRDNET_WEBSERVER_PORT"
custom_cid=$(start -e "BIRDNET_WEBSERVER_PORT=$CUSTOM_PORT")
cids+=("$custom_cid")
wait_healthy "$custom_cid" "custom port"
expect "custom port probed" pass "$custom_cid"
expect "surrounding spaces are trimmed" pass "$custom_cid" -e "BIRDNET_WEBSERVER_PORT= $CUSTOM_PORT "

if [ "$fails" -ne 0 ]; then
    echo "FAIL: $fails health check case(s) failed"
    exit 1
fi
echo "PASS: health check follows BIRDNET_WEBSERVER_PORT"
