#!/usr/bin/env bash
#
# Smoke test: .local (mDNS) names must resolve inside the container through the
# host's avahi-daemon socket, and everything else must behave as before. Guards:
#   1. Image contents: the nsswitch hosts line (files mdns4_minimal dns, no
#      [NOTFOUND=return]), no avahi-daemon (libnss-mdns only Recommends it),
#      localhost still resolves, and the Go binary is a cgo build without the
#      netgo tag (otherwise Go ignores nsswitch and never asks nss-mdns).
#   2. libc resolution of a .local name through a fake Avahi socket mounted
#      read-only at /run/avahi-daemon (module + socket path + connect over :ro).
#   3. The FFmpeg path: ffprobe reaches the connect stage instead of failing DNS.
#   4. Without the mount a .local lookup fails fast.
#
# Usage: Docker/test/mdns-nss-smoke.sh <image-ref>
#
# The fake Avahi server runs on the host (needs python3); the app is never started
# (--entrypoint overrides), so no config or data volumes are needed.
set -euo pipefail

IMAGE="${1:?usage: mdns-nss-smoke.sh <image-ref>}"
NAME="smoke-test.local"
EXPECTED_HOSTS='hosts:          files mdns4_minimal dns'

workdir=$(mktemp -d)
server_pid=""
cleanup() {
    [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
    rm -rf "$workdir"
}
trap cleanup EXIT

fail() { echo "FAIL: $*"; exit 1; }
run() { docker run --rm --entrypoint "$@"; }

echo "==> 1. image contents: nsswitch hosts line, no avahi-daemon, localhost, cgo build"
# One container run covers the static checks. avahi-daemon must be absent
# (libnss-mdns only Recommends it); step 6 reads the Go build info of the binary
# in place, since the Go resolver only consults nsswitch in a cgo build without netgo.
static_out=$(docker run --rm --entrypoint bash "$IMAGE" -c '
    grep "^hosts:" /etc/nsswitch.conf
    if dpkg -s avahi-daemon >/dev/null 2>&1; then echo "AVAHI-INSTALLED"; fi
    getent hosts localhost >/dev/null && echo "LOCALHOST-OK"
    grep -aq "build.CGO_ENABLED=1" /usr/bin/birdnet-go && echo "CGO-OK"
    if grep -a "build.-tags=" /usr/bin/birdnet-go | grep -q netgo; then echo "NETGO-TAG"; fi
')
echo "$static_out" | sed 's/^/    /'
printf '%s\n' "$static_out" | grep -qxF "$EXPECTED_HOSTS" || fail "hosts line is not '$EXPECTED_HOSTS'"
printf '%s\n' "$static_out" | grep -qx "AVAHI-INSTALLED" && fail "avahi-daemon is installed in the image (libnss-mdns must be installed with --no-install-recommends)"
printf '%s\n' "$static_out" | grep -qx "LOCALHOST-OK" || fail "getent hosts localhost failed"
printf '%s\n' "$static_out" | grep -qx "CGO-OK" || fail "binary build info has no CGO_ENABLED=1"
printf '%s\n' "$static_out" | grep -qx "NETGO-TAG" && fail "binary was built with the netgo tag; Go would bypass nss-mdns"

echo "==> 2. libc resolves $NAME through a read-only Avahi socket mount"
# Fake Avahi simple-protocol server: nss-mdns sends "RESOLVE-HOSTNAME-IPV4 <name>"
# and expects "+ <ifindex> <protocol> <name> <address>" back.
mkdir -p "$workdir/avahi"
chmod 755 "$workdir/avahi"
cat > "$workdir/fake_avahi.py" <<'PYEOF'
import os, socketserver, sys

NAME = sys.argv[2]

class Handler(socketserver.StreamRequestHandler):
    def handle(self):
        line = self.rfile.readline().decode().strip()
        if line == "RESOLVE-HOSTNAME-IPV4 " + NAME:
            self.wfile.write(("+ 1 0 %s 127.0.0.1\n" % NAME).encode())
        else:
            self.wfile.write(b"- Not found\n")

path = sys.argv[1]
server = socketserver.ThreadingUnixStreamServer(path, Handler)
os.chmod(path, 0o666)
print("ready", flush=True)
server.serve_forever()
PYEOF
python3 "$workdir/fake_avahi.py" "$workdir/avahi/socket" "$NAME" > "$workdir/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 50); do
    [ -S "$workdir/avahi/socket" ] && break
    sleep 0.1
done
[ -S "$workdir/avahi/socket" ] || { cat "$workdir/server.log"; fail "fake Avahi server did not start"; }

resolved=$(docker run --rm --entrypoint getent -v "$workdir/avahi:/run/avahi-daemon:ro" "$IMAGE" ahostsv4 "$NAME" || true)
echo "    $resolved" | head -1
printf '%s\n' "$resolved" | grep -q '^127\.0\.0\.1 ' || fail "getent ahostsv4 $NAME did not return 127.0.0.1"

echo "==> 3. ffprobe resolves $NAME and reaches the connect stage"
probe_err=$(docker run --rm --entrypoint timeout -v "$workdir/avahi:/run/avahi-daemon:ro" "$IMAGE" \
    5 ffprobe -v error -rw_timeout 3000000 "rtsp://$NAME:9/x" 2>&1 >/dev/null || true)
echo "$probe_err" | sed 's/^/    /'
printf '%s\n' "$probe_err" | grep -q 'Failed to resolve hostname' && fail "ffprobe could not resolve $NAME"
printf '%s\n' "$probe_err" | grep -q 'Connection refused' || fail "ffprobe stderr has no 'Connection refused' (expected a connect attempt to 127.0.0.1:9)"

echo "==> 4. without the mount a .local lookup fails fast"
start=$SECONDS
rc=0
docker run --rm --entrypoint timeout "$IMAGE" 5 getent hosts "$NAME" >/dev/null 2>&1 || rc=$?
elapsed=$((SECONDS - start))
echo "    getent hosts $NAME: rc=$rc"
[ "$rc" -ne 0 ] || fail "$NAME resolved without any Avahi mount"
[ "$rc" -ne 124 ] || fail "getent hosts $NAME hung for 5s without the mount (elapsed ${elapsed}s incl. container start)"

echo "PASS: .local names resolve through the mounted host Avahi socket; nsswitch, image contents, FFmpeg path and build flags are as expected"
