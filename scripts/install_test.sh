#!/usr/bin/env bash
#
# Unit tests for the configuration helper functions in install.sh.
#
# install.sh edits a 2-space-indent YAML config and parses/regenerates a systemd unit with
# sed/awk. Those helpers have no other coverage, and several were converted to re-run-safe
# forms where a wrong indentation anchor or field-order assumption silently
# corrupts a real user config. This harness extracts the pure helpers from install.sh (the
# script has no source guard, so it cannot be sourced wholesale) and exercises them against
# the real config template, asserting both the intended change and that sibling keys are left
# untouched.
#
# Run: scripts/install_test.sh   (exit 0 = all pass). No external dependencies beyond awk/sed.

# Many globals here (colors, CONFIG_FILE, SILENT_MODE, the load_existing_service_config output
# vars, BIRDNET_AUDIO_FORMAT) are consumed only inside the eval'd functions extracted from
# install.sh, which shellcheck cannot see, so it flags them as unused. They are not.
# shellcheck disable=SC2034

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_SH="${REPO_ROOT}/install.sh"
CONFIG_TEMPLATE="${REPO_ROOT}/internal/conf/config.yaml"

if [ ! -f "$INSTALL_SH" ]; then
    echo "FATAL: install.sh not found at $INSTALL_SH" >&2
    exit 2
fi
if [ ! -f "$CONFIG_TEMPLATE" ]; then
    echo "FATAL: config template not found at $CONFIG_TEMPLATE" >&2
    exit 2
fi

# ---------------------------------------------------------------------------
# Test framework (minimal, dependency-free)
# ---------------------------------------------------------------------------
PASS=0
FAIL=0
CURRENT_TEST=""

it() { CURRENT_TEST="$1"; }

assert_eq() { # description expected actual
    if [ "$2" = "$3" ]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        printf 'FAIL [%s] %s\n' "$CURRENT_TEST" "$1" >&2
        printf '       expected: [%s]\n' "$2" >&2
        printf '       actual:   [%s]\n' "$3" >&2
    fi
}

assert_ok() { # description ; uses $? of previous command via explicit arg
    if [ "$2" -eq 0 ]; then PASS=$((PASS + 1)); else
        FAIL=$((FAIL + 1)); printf 'FAIL [%s] %s (rc=%s, expected 0)\n' "$CURRENT_TEST" "$1" "$2" >&2
    fi
}

assert_nonzero() { # description rc
    if [ "$2" -ne 0 ]; then PASS=$((PASS + 1)); else
        FAIL=$((FAIL + 1)); printf 'FAIL [%s] %s (rc=0, expected non-zero)\n' "$CURRENT_TEST" "$1" >&2
    fi
}

# ---------------------------------------------------------------------------
# Extract a single top-level function (name() { ... } closing at column 0) from
# install.sh and define it in this shell. install.sh formats every top-level
# function close as a bare "}" at column 0; inner braces are indented, so the
# first "^}$" after the header is the function's own close. (A here-doc emitting a
# column-0 "}" would break this assumption; none of the loaded functions do today.)
# ---------------------------------------------------------------------------
load_fn() {
    local fn="$1"
    local body
    body="$(awk -v fn="$fn" '
        $0 ~ "^"fn"\\(\\) \\{" { printing = 1 }
        printing { print }
        printing && /^\}$/ { exit }
    ' "$INSTALL_SH")"
    if [ -z "$body" ]; then
        echo "FATAL: could not extract function '$fn' from install.sh" >&2
        exit 2
    fi
    eval "$body"
}

# Stubs for the side-effecting helpers the extracted functions call.
print_message() { :; }
log_message() { :; }
log_command_result() { :; }
command_exists() { command -v "$1" >/dev/null 2>&1; }
check_directory_exists() { return 1; }
is_raspberry_pi() { return 1; }
resolve_host_timezone() { printf '%s' "$1"; }

# Globals the extracted functions reference (install.sh defines these at the top; the harness
# only pulls function bodies, so under set -u they must exist here).
RED=""; GREEN=""; YELLOW=""; NC=""; GRAY=""
CONFIG_FILE=""
SILENT_MODE=""
BIRDNET_PASSWORD=""
CONFIG_DIR="/tmp/birdnet-go-test-config"
DATA_DIR="/tmp/birdnet-go-test-data"
BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:nightly"
WEB_PORT="8080"
WEB_PORT_BIND_ADDR=""
BIND_TLS_PORTS="false"
TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"
METRICS_BIND_ADDR=""
CONFIGURED_TZ="UTC"

# Load the helpers under test (order matters: callees before callers).
for fn in \
    sed_escape_replacement \
    sed_escape_pattern \
    set_config_value \
    set_yaml_value \
    set_first_audio_source \
    _extract_bind_addr \
    load_existing_service_config \
    generate_systemd_service_content \
    apply_tls_settings \
    ensure_internal_port_8080 \
    configure_rtsp_in_config \
    configure_audio_format \
    configure_locale \
    configure_auth \
    rewrite_migrated_config_paths
do
    load_fn "$fn"
done

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fresh_config() { # -> path to a fresh copy of the template
    local p="${WORK}/cfg.$RANDOM.$RANDOM.yaml"
    cp "$CONFIG_TEMPLATE" "$p"
    printf '%s' "$p"
}

# yaml_scalar FILE INDENT PARENT_REGEX KEY -> value of the first KEY at INDENT spaces after
# PARENT_REGEX matches. Used to read back what the helpers wrote.
yaml_after() { # file parent_regex indent key
    awk -v parent="$2" -v ind="$3" -v key="$4" '
        $0 ~ parent { f = 1; next }
        f && $0 ~ ("^ {" ind "}" key ":") {
            line = $0
            sub(/^[[:space:]]*[A-Za-z0-9_]+:[[:space:]]*/, "", line)
            sub(/[[:space:]]+#.*$/, "", line)   # drop an inline comment
            sub(/[[:space:]]+$/, "", line)      # drop trailing whitespace
            print line
            exit
        }
    ' "$1"
}

# ===========================================================================
# set_yaml_value
# ===========================================================================
it "set_yaml_value"

cfg="$(fresh_config)"
set_yaml_value "security.basicauth.enabled" "true" "$cfg"
assert_eq "basicauth.enabled set to true" "true" "$(yaml_after "$cfg" '^[[:space:]]{2}basicauth:' 4 enabled)"
# The sibling sub-blocks that ALSO have enabled: at indent 4 must be untouched.
assert_eq "allowsubnetbypass.enabled untouched" "false" "$(yaml_after "$cfg" '^[[:space:]]{2}allowsubnetbypass:' 4 enabled)"
assert_eq "googleAuth.enabled untouched" "false" "$(yaml_after "$cfg" '^[[:space:]]{2}googleAuth:' 4 enabled)"

# Quoted value with sed metacharacters must be written literally (no sed escaping needed).
set_yaml_value "security.basicauth.password" '"$2b$10$ab/CD.ef&gh|ij"' "$cfg"
assert_eq "password written literally with metachars" '"$2b$10$ab/CD.ef&gh|ij"' "$(yaml_after "$cfg" '^[[:space:]]{2}basicauth:' 4 password)"

# Deeply nested leaf (indent 6) that appears after sibling scalars and before a sub-block.
cfg="$(fresh_config)"
set_yaml_value "realtime.audio.export.type" "aac" "$cfg"
assert_eq "export.type set to aac" "aac" "$(yaml_after "$cfg" '^[[:space:]]{4}export:' 6 type)"
assert_eq "export.enabled untouched (true)" "true" "$(yaml_after "$cfg" '^[[:space:]]{4}export:' 6 enabled)"
# The equalizer list item '- type: HighPass' (indent 8, list element) must be untouched.
hp="$(awk '/^[[:space:]]{4}equalizer:/{f=1} f&&/^[[:space:]]{8}-[[:space:]]+type:/{print $3; exit}' "$cfg")"
assert_eq "equalizer filter '- type:' untouched" "HighPass" "$hp"

# Re-run safety: changing a value that is no longer the template default.
set_yaml_value "realtime.audio.export.type" "opus" "$cfg"
assert_eq "export.type re-changed aac->opus" "opus" "$(yaml_after "$cfg" '^[[:space:]]{4}export:' 6 type)"

# Missing leaf returns non-zero so callers can warn.
cfg="$(fresh_config)"
set_yaml_value "security.basicauth.nope" "x" "$cfg"; rc=$?
assert_nonzero "missing leaf returns non-zero" "$rc"

# 2-space direct child (same scope set_config_value handles).
set_yaml_value "webserver.port" '"9000"' "$cfg"
assert_eq "webserver.port set" '"9000"' "$(yaml_after "$cfg" '^webserver:' 2 port)"

# The app (Go yaml.v3) re-serializes config.yaml with 4-space indentation, not the
# template's 2-space. set_yaml_value must locate nested paths regardless of indent width,
# or reconfigure silently no-ops on a live install (audio format / web auth / locale).
# The soundlevel.export node is synthetic (the real config has no audio.soundlevel.export);
# it exists only to place a same-named key one level deeper than the real audio.export.type
# so the grandchild-false-match guard is exercised.
app_config() { # -> path to a 4-space app-style fixture
    local p="${WORK}/app.$RANDOM.$RANDOM.yaml"
    cat > "$p" <<'YAML'
realtime:
    audio:
        soundlevel:
            export:
                type: bad
        export:
            enabled: true
            type: wav
birdnet:
    locale: en
    threshold: "0.8"
security:
    basicauth:
        enabled: false
        password: ""
    allowsubnetbypass:
        enabled: false
YAML
    printf '%s' "$p"
}

it "set_yaml_value (4-space app config)"

acfg="$(app_config)"
set_yaml_value "realtime.audio.export.type" "flac" "$acfg"
assert_eq "4-space export.type set to flac" "flac" "$(yaml_after "$acfg" '^[[:space:]]{8}export:' 12 type)"
assert_eq "4-space export.enabled untouched" "true" "$(yaml_after "$acfg" '^[[:space:]]{8}export:' 12 enabled)"
# A same-named key nested deeper under a non-matching sibling must not be rewritten.
assert_eq "4-space grandchild soundlevel.export.type untouched" "bad" "$(yaml_after "$acfg" '^[[:space:]]{12}export:' 16 type)"

set_yaml_value "birdnet.locale" "fi" "$acfg"
assert_eq "4-space birdnet.locale set to fi" "fi" "$(yaml_after "$acfg" '^birdnet:' 4 locale)"

set_yaml_value "security.basicauth.enabled" "true" "$acfg"
assert_eq "4-space basicauth.enabled set to true" "true" "$(yaml_after "$acfg" '^[[:space:]]{4}basicauth:' 8 enabled)"
assert_eq "4-space allowsubnetbypass.enabled untouched" "false" "$(yaml_after "$acfg" '^[[:space:]]{4}allowsubnetbypass:' 8 enabled)"

# Re-run on the 4-space config changes an already-set value (not a no-op).
set_yaml_value "realtime.audio.export.type" "opus" "$acfg"
assert_eq "4-space export.type re-changed flac->opus" "opus" "$(yaml_after "$acfg" '^[[:space:]]{8}export:' 12 type)"

# ===========================================================================
# set_config_value
# ===========================================================================
it "set_config_value"

cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
set_config_value security host "birdnet.example.com"
assert_eq "security.host set" "birdnet.example.com" "$(yaml_after "$cfg" '^security:' 2 host)"
# Re-run: change the already-set value.
set_config_value security host "other.example.com"
assert_eq "security.host re-changed" "other.example.com" "$(yaml_after "$cfg" '^security:' 2 host)"
# A nested grandchild with a same name must NOT be reachable by set_config_value (it only
# anchors to a 2-space child), so basicauth.enabled stays false.
set_config_value security enabled "true"
assert_eq "set_config_value does not reach grandchild basicauth.enabled" "false" "$(yaml_after "$cfg" '^[[:space:]]{2}basicauth:' 4 enabled)"

# 4-space (app-serialized) config: a direct child of a top-level block sits at indent 4, not 2.
# The old fixed 2-space sed anchor silently no-opped here (breaking TLS / web-port reconfigure
# on a live config); delegating to the indent-agnostic set_yaml_value fixes it. Fails on old code.
acfg2="$(app_config)"
CONFIG_FILE="$acfg2"
set_config_value birdnet locale "fi"
assert_eq "4-space set_config_value reaches indent-4 child" "fi" "$(yaml_after "$acfg2" '^birdnet:' 4 locale)"
# A non-existent direct scalar child must no-op without reaching the indent-8 grandchild.
set_config_value security enabled "true"
assert_eq "4-space set_config_value does not reach grandchild" "false" "$(yaml_after "$acfg2" '^[[:space:]]{4}basicauth:' 8 enabled)"

# ===========================================================================
# set_first_audio_source
# ===========================================================================
it "set_first_audio_source"

# Pristine template: name-first then device.
cfg="$(fresh_config)"
set_first_audio_source "hw:1,0" "USB Mic" "$cfg"; rc=$?
assert_ok "pristine source edit succeeds" "$rc"
dev="$(awk '/^[[:space:]]{4}sources:/{f=1} f&&/^[[:space:]]+(-[[:space:]]+)?device:/{sub(/^[[:space:]]*(-[[:space:]]+)?device:[[:space:]]*/,"");print;exit}' "$cfg")"
nm="$(awk '/^[[:space:]]{4}sources:/{f=1} f&&/^[[:space:]]+-[[:space:]]+name:/{sub(/^[[:space:]]*-[[:space:]]+name:[[:space:]]*/,"");print;exit}' "$cfg")"
assert_eq "device set" '"hw:1,0"' "$dev"
assert_eq "name set" '"USB Mic"' "$nm"

# ENVIRON (not awk -v) preserves a backslash in the device verbatim. With the old `awk -v`,
# `\t` in the value was processed into a literal tab; ENVIRON keeps it as two characters.
cfg="$(fresh_config)"
set_first_audio_source 'hw:Foo\tBar' "USB Mic" "$cfg"
dev="$(awk '/^[[:space:]]{4}sources:/{f=1} f&&/^[[:space:]]+(-[[:space:]]+)?device:/{sub(/^[[:space:]]*(-[[:space:]]+)?device:[[:space:]]*/,"");print;exit}' "$cfg")"
assert_eq "backslash in device preserved verbatim (ENVIRON, not awk -v)" '"hw:Foo\tBar"' "$dev"

# Reverse field order (device before name) must also work.
cat > "$cfg" <<'EOF'
realtime:
  audio:
    sources:
      - device: "sysdefault"
        name: "Sound Card 1"
        gain: 0
    soundlevel:
      enabled: false
EOF
set_first_audio_source "hw:2,0" "Other Mic" "$cfg"; rc=$?
assert_ok "reverse-order source edit succeeds" "$rc"
dev="$(awk '/^[[:space:]]{4}sources:/{f=1} f&&/^[[:space:]]+(-[[:space:]]+)?device:/{sub(/^[[:space:]]*(-[[:space:]]+)?device:[[:space:]]*/,"");print;exit}' "$cfg")"
assert_eq "device set (reverse order)" '"hw:2,0"' "$dev"

# Multi-source: only the FIRST item is edited.
cat > "$cfg" <<'EOF'
realtime:
  audio:
    sources:
      - name: "Sound Card 1"
        device: "sysdefault"
        gain: 0
      - name: "Second"
        device: "hw:9,0"
        gain: 0
    soundlevel:
      enabled: false
EOF
set_first_audio_source "hw:1,0" "First" "$cfg"
second_dev="$(awk '/- name: "Second"/{f=1} f&&/device:/{print;exit}' "$cfg")"
assert_eq "second source device untouched" '        device: "hw:9,0"' "$second_dev"

# No active source line (sound card commented out, RTSP-only) returns non-zero.
cat > "$cfg" <<'EOF'
realtime:
  audio:
    sources:
# - name: "Sound Card 1"
#   device: "sysdefault"
    soundlevel:
      enabled: false
EOF
set_first_audio_source "hw:1,0" "x" "$cfg"; rc=$?
assert_nonzero "no active source returns non-zero" "$rc"

# ===========================================================================
# load_existing_service_config
# ===========================================================================
it "load_existing_service_config"

unit="${WORK}/birdnet-go.service"
cat > "$unit" <<'EOF'
[Service]
ExecStart=/usr/bin/docker run --rm \
    --name birdnet-go \
    -p 127.0.0.1:9000:8080 \
    -p 443:443 \
    -p 8090:8090 \
    --env TZ="Europe/Helsinki" \
    -v /home/pi/birdnet-go-app/config:/config \
    ghcr.io/tphakala/birdnet-go:nightly
EOF
WEB_PORT=""; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ=""
load_existing_service_config "$unit"
assert_eq "restored web port" "9000" "$WEB_PORT"
assert_eq "restored web bind addr" "127.0.0.1" "$WEB_PORT_BIND_ADDR"
assert_eq "restored TLS binding" "true" "$BIND_TLS_PORTS"
assert_eq "restored metrics binding" "true" "$BIND_METRICS_PORT"
assert_eq "restored timezone" "Europe/Helsinki" "$CONFIGURED_TZ"

# A unit with only the web port (no 80/443, no 8090) leaves TLS/metrics off.
cat > "$unit" <<'EOF'
[Service]
ExecStart=/usr/bin/docker run --rm \
    -p 8080:8080 \
    --env TZ="UTC" \
    ghcr.io/tphakala/birdnet-go:nightly
EOF
WEB_PORT=""; BIND_TLS_PORTS="false"; BIND_METRICS_PORT="false"; CONFIGURED_TZ=""
load_existing_service_config "$unit"
assert_eq "web-only: port restored" "8080" "$WEB_PORT"
assert_eq "web-only: TLS stays off" "false" "$BIND_TLS_PORTS"
assert_eq "web-only: metrics stays off" "false" "$BIND_METRICS_PORT"

# ===========================================================================
# generate_systemd_service_content
# ===========================================================================
it "generate_systemd_service_content"

WEB_PORT="8080"; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="true"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ="UTC"
unit_content="$(generate_systemd_service_content)"
autotls_cap_count="$(printf '%s\n' "$unit_content" | grep -c -- '--cap-add NET_BIND_SERVICE' || true)"
assert_eq "AutoTLS unit grants low-port bind capability" "1" "$autotls_cap_count"

BIND_TLS_PORTS="false"
unit_content="$(generate_systemd_service_content)"
plain_cap_count="$(printf '%s\n' "$unit_content" | grep -c -- '--cap-add NET_BIND_SERVICE' || true)"
assert_eq "plain HTTP unit does not grant low-port bind capability" "0" "$plain_cap_count"

# The container entrypoint drops from root to BIRDNET_UID with gosu before
# launching birdnet-go. Docker's --cap-add keeps NET_BIND_SERVICE in the
# container bounding set, but the binary also needs a file capability so the
# non-root process can bind ports 80/443 after the uid switch.
it "Dockerfile grants birdnet-go low-port bind file capability"
dockerfile_content="$(cat "${REPO_ROOT}/Dockerfile")"
libcap_count="$(printf '%s\n' "$dockerfile_content" | grep -c -- 'libcap2-bin' || true)"
setcap_count="$(printf '%s\n' "$dockerfile_content" | grep -c -- 'setcap cap_net_bind_service=+ep /usr/bin/birdnet-go' || true)"
assert_eq "Docker image installs setcap tool" "1" "$libcap_count"
assert_eq "Docker image grants birdnet-go CAP_NET_BIND_SERVICE" "1" "$setcap_count"

# ===========================================================================
# apply_tls_settings (full slate; mode switch must clear stale host)
# ===========================================================================
it "apply_tls_settings"

cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
apply_tls_settings autotls "birdnet.example.com" ""
assert_eq "autotls: host set" "birdnet.example.com" "$(yaml_after "$cfg" '^security:' 2 host)"
assert_eq "autotls: autoTls true" "true" "$(yaml_after "$cfg" '^security:' 2 autoTls)"
# Switch to direct: the stale host must be cleared (the ledger regression).
apply_tls_settings direct "" ""
assert_eq "direct: host cleared" "" "$(yaml_after "$cfg" '^security:' 2 host)"
assert_eq "direct: autoTls false" "false" "$(yaml_after "$cfg" '^security:' 2 autoTls)"

# ===========================================================================
# configure_rtsp_in_config (populate empty default; guard a populated list)
# ===========================================================================
it "configure_rtsp_in_config"

cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
configure_rtsp_in_config "rtsp://user:pass@cam.local:554/stream1" "Front Door"
got_url="$(awk '/^[[:space:]]{4}streams:/{f=1} f&&/url:/{sub(/^[[:space:]]*url:[[:space:]]*/,"");print;exit}' "$cfg")"
assert_eq "fresh: stream url written" '"rtsp://user:pass@cam.local:554/stream1"' "$got_url"
sc_commented="$(grep -c '^# .*device: "sysdefault"' "$cfg")"
assert_eq "fresh: default sound card source commented out" "1" "$sc_commented"

# Re-run against the now-populated config: guard must leave it unchanged (no second stream,
# no double-commenting).
before="$(sha256sum "$cfg" | cut -d' ' -f1)"
configure_rtsp_in_config "rtsp://other/stream2" "Backyard"; rc=$?
after="$(sha256sum "$cfg" | cut -d' ' -f1)"
assert_ok "re-run returns success" "$rc"
assert_eq "re-run leaves populated config byte-identical" "$before" "$after"

# Stronger guard test: the byte-identical case above is coincidentally idempotent because the
# first populate already commented the source. Build the realistic "RTSP added via the web UI"
# shape, streams populated but the sound-card source still ACTIVE (uncommented). Without the
# guard, the source-commenting seds would comment the live source; the guard must skip. This
# case goes RED if the guard is removed.
cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
sed -i 's|^    streams: \[\].*|    streams:\n      - name: "Existing"\n        url: "rtsp://existing/s"\n        enabled: true\n        type: rtsp\n        transport: tcp|' "$cfg"
before="$(sha256sum "$cfg" | cut -d' ' -f1)"
configure_rtsp_in_config "rtsp://new/stream" "New"; rc=$?
after="$(sha256sum "$cfg" | cut -d' ' -f1)"
assert_ok "guard: re-run on populated config returns success" "$rc"
assert_eq "guard: populated config with ACTIVE source left byte-identical" "$before" "$after"
active="$(grep -c '^      - name: "Sound Card 1"' "$cfg")"
assert_eq "guard: sound-card source still active (uncommented)" "1" "$active"

# ===========================================================================
# configure_audio_format (silent re-run safety)
# ===========================================================================
it "configure_audio_format"

cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
SILENT_MODE="true"; BIRDNET_AUDIO_FORMAT="flac"
configure_audio_format
assert_eq "silent: format set to flac" "flac" "$(yaml_after "$cfg" '^[[:space:]]{4}export:' 6 type)"
BIRDNET_AUDIO_FORMAT="opus"
configure_audio_format
assert_eq "silent re-run: format changed flac->opus" "opus" "$(yaml_after "$cfg" '^[[:space:]]{4}export:' 6 type)"
SILENT_MODE=""

# ===========================================================================
# rewrite_migrated_config_paths
# ===========================================================================
it "rewrite_migrated_config_paths"

cfg="${WORK}/migrate.yaml"
cat > "$cfg" <<'EOF'
realtime:
  audio:
    export:
      path: /root/birdnet-go-app/data/clips
monitoring:
  disk:
    paths: /root/.config/birdnet-go
unrelated: /root/some-other-dir
EOF
rewrite_migrated_config_paths "$cfg" "/root" "/home/pi"
assert_eq "birdnet-go-app path rewritten" "/home/pi/birdnet-go-app/data/clips" "$(awk '/path:/{sub(/^[[:space:]]*path:[[:space:]]*/,"");print;exit}' "$cfg")"
assert_eq ".config path rewritten" "/home/pi/.config/birdnet-go" "$(awk '/paths:/{sub(/^[[:space:]]*paths:[[:space:]]*/,"");print;exit}' "$cfg")"
assert_eq "unrelated /root path untouched" "/root/some-other-dir" "$(awk '/unrelated:/{sub(/^[[:space:]]*unrelated:[[:space:]]*/,"");print;exit}' "$cfg")"

# Same old/new home is a no-op.
cp "$CONFIG_TEMPLATE" "${WORK}/noop.yaml"
before="$(sha256sum "${WORK}/noop.yaml" | cut -d' ' -f1)"
rewrite_migrated_config_paths "${WORK}/noop.yaml" "/home/pi" "/home/pi"
after="$(sha256sum "${WORK}/noop.yaml" | cut -d' ' -f1)"
assert_eq "same home is a no-op" "$before" "$after"

# ===========================================================================
# configure_locale (silent; must not corrupt the eBird locale)
# ===========================================================================
it "configure_locale"

cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
SILENT_MODE="true"; BIRDNET_LOCALE="fi"
configure_locale
assert_eq "silent: birdnet.locale set to fi" "fi" "$(yaml_after "$cfg" '^birdnet:' 2 locale)"
# The eBird locale (realtime.ebird.locale: "en") must NOT be corrupted to fi"en" by the
# birdnet-locale edit (the pre-existing double-locale bug).
ebird_locale="$(yaml_after "$cfg" '^[[:space:]]{2}ebird:' 4 locale)"
assert_eq "silent: eBird locale untouched" '"en"' "$ebird_locale"
SILENT_MODE=""

# ===========================================================================
# configure_auth (interactive EOF guard: a closed stdin must not disable auth)
# ===========================================================================
it "configure_auth"

# Pre-enable auth, then drive the interactive path with stdin closed. The gating
# "Enable password protection? (y/n)" read hits EOF; before the guard this fell through
# to the disable branch and silently set basicauth.enabled false. The guard must instead
# return non-zero and leave the existing setting untouched.
cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
SILENT_MODE="false"
set_yaml_value "security.basicauth.enabled" "true" "$cfg"
configure_auth </dev/null
assert_nonzero "EOF on enable prompt returns non-zero (no change)" "$?"
assert_eq "EOF must not disable existing auth" "true" "$(yaml_after "$cfg" '^[[:space:]]{2}basicauth:' 4 enabled)"
SILENT_MODE=""

# ===========================================================================
# ensure_internal_port_8080 (normalize a custom webserver.port back to 8080)
# ===========================================================================
it "ensure_internal_port_8080"

cfg="$(fresh_config)"
CONFIG_FILE="$cfg"
set_config_value webserver port '"9000"'
ensure_internal_port_8080
assert_eq "custom internal port normalized to 8080" '"8080"' "$(yaml_after "$cfg" '^webserver:' 2 port)"
ensure_internal_port_8080
assert_eq "already-8080 stays 8080 (idempotent)" '"8080"' "$(yaml_after "$cfg" '^webserver:' 2 port)"

# ===========================================================================
# Result
# ===========================================================================
echo "------------------------------------------------------------"
echo "install.sh helper tests: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
