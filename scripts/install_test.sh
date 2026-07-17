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
# Run: scripts/install_test.sh   (exit 0 = all pass). Needs awk/sed, plus jq for the
# migration-diagnostics tests (they assert the telemetry payload is valid JSON, and
# install.sh already requires jq to send any telemetry at all).

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
# The migration-diagnostics tests validate JSON with jq. Fail loudly here rather
# than as nine cryptic assertion failures on a machine without it.
if ! command -v jq >/dev/null 2>&1; then
    echo "FATAL: jq not found; required by the migration diagnostics tests" >&2
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
# first "^}$" after the header is the function's own close.
#
# Here-docs are tracked and skipped: the diagnostics collectors emit JSON whose
# closing "}" sits at column 0 inside a here-doc body, which would otherwise be
# mistaken for the function's own close and yield a truncated, unparseable body.
# ---------------------------------------------------------------------------
load_fn() {
    local fn="$1"
    local body
    body="$(awk -v fn="$fn" '
        # Remember the delimiter of any here-doc opened on this line so its body
        # (which may contain a column-0 "}") is passed through verbatim.
        # Excludes here-strings (<<<), whose word is not a delimiter, and accepts
        # either quote style: a missed <<'"'"'EOF'"'"' truncates the body, and a
        # here-string mistaken for an opener swallows the rest of the file.
        function heredoc_delim(line,   d) {
            if (line ~ /<<</) return ""
            if (!match(line, /<<-?[ \t]*["'"'"']?[A-Za-z_][A-Za-z_0-9]*["'"'"']?/)) return ""
            d = substr(line, RSTART, RLENGTH)
            sub(/^<<-?[ \t]*/, "", d)
            gsub(/["'"'"']/, "", d)
            return d
        }
        function trim(s) { sub(/^[ \t]+/, "", s); return s }

        $0 ~ "^"fn"\\(\\) \\{" { printing = 1 }
        !printing { next }
        { print }
        heredoc != "" { if (trim($0) == heredoc) heredoc = ""; next }
        # A comment mentioning a here-doc is prose, not an opener.
        trim($0) ~ /^#/ { next }
        { d = heredoc_delim($0); if (d != "") { heredoc = d; next } }
        /^\}$/ { exit }
    ' "$INSTALL_SH")"
    if [ -z "$body" ]; then
        echo "FATAL: could not extract function '$fn' from install.sh" >&2
        exit 2
    fi
    # A runaway extraction (an opener we misread) yields a body that does not end
    # at the function close. Without this it still eval's -- sometimes cleanly,
    # redefining a dozen other functions -- and the suite goes green while testing
    # something else entirely.
    if [ "$(printf '%s' "$body" | tail -n 1)" != "}" ]; then
        echo "FATAL: extraction of '$fn' did not end at a column-0 '}'; check load_fn's here-doc tracking" >&2
        exit 2
    fi
    if ! eval "$body"; then
        echo "FATAL: extracted body of '$fn' failed to eval" >&2
        exit 2
    fi
}

# Stubs for the side-effecting helpers the extracted functions call.
print_message() { :; }
log_message() { :; }
log_command_result() { :; }
command_exists() { command -v "$1" >/dev/null 2>&1; }

# Deterministic stubs for the helpers generate_systemd_service_content calls: force
# audio/thermal/Pi/GPU detection off and resolve the timezone to the passed value so the
# generated unit is stable across hosts (a CI runner with an Intel iGPU must not add
# --device /dev/dri to the baseline units; the GPU-passthrough test overrides this stub).
resolve_host_timezone() { printf '%s' "${1:-UTC}"; }
check_directory_exists() { return 1; }
is_raspberry_pi() { return 1; }
has_intel_gpu() { return 1; }
# No docker on the CI runner path we exercise; the container-TZ fallback degrades to empty.
# The load_existing_service_config container-fallback test overrides this stub.
safe_docker() { return 1; }

# Globals the extracted functions reference (install.sh defines these at the top; the harness
# only pulls function bodies, so under set -u they must exist here).
RED=""; GREEN=""; YELLOW=""; NC=""; GRAY=""
CONFIG_FILE=""
SILENT_MODE=""
BIRDNET_PASSWORD=""

# Load the helpers under test (order matters: callees before callers).
for fn in \
    sed_escape_replacement \
    sed_escape_pattern \
    set_config_value \
    set_yaml_value \
    set_first_audio_source \
    _extract_bind_addr \
    _read_unit_file \
    load_existing_service_config \
    generate_systemd_service_content \
    apply_tls_settings \
    ensure_internal_port_8080 \
    configure_rtsp_in_config \
    configure_audio_format \
    configure_locale \
    configure_auth \
    rewrite_migrated_config_paths \
    parse_ssh_dest \
    remote_path_safe \
    remote_default_app_path
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
    -p 80:8080 \
    -p 443:8443 \
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
assert_eq "restored TLS bind addr (none)" "" "$TLS_BIND_ADDR"
assert_eq "restored metrics binding" "true" "$BIND_METRICS_PORT"
assert_eq "restored timezone" "Europe/Helsinki" "$CONFIGURED_TZ"

# A legacy (pre-fix) AutoTLS unit still maps the dead 80:80 / 443:443 ports. It must still
# be detected as AutoTLS-enabled so a regenerate produces the corrected 443:8443 mapping
# instead of silently dropping AutoTLS.
cat > "$unit" <<'EOF'
[Service]
ExecStart=/usr/bin/docker run --rm \
    -p 8080:8080 \
    -p 80:80 \
    -p 443:443 \
    ghcr.io/tphakala/birdnet-go:nightly
EOF
WEB_PORT=""; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ=""
load_existing_service_config "$unit"
assert_eq "legacy 443:443: web port restored" "8080" "$WEB_PORT"
assert_eq "legacy 443:443: TLS binding restored" "true" "$BIND_TLS_PORTS"
assert_eq "legacy 443:443: TLS bind addr (none)" "" "$TLS_BIND_ADDR"

# A localhost-bound AutoTLS mapping must preserve the host bind address so an update does
# not silently re-expose it on all interfaces.
cat > "$unit" <<'EOF'
[Service]
ExecStart=/usr/bin/docker run --rm \
    -p 127.0.0.1:8080:8080 \
    -p 127.0.0.1:80:8080 \
    -p 127.0.0.1:443:8443 \
    ghcr.io/tphakala/birdnet-go:nightly
EOF
WEB_PORT=""; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ=""
load_existing_service_config "$unit"
assert_eq "bind-addr: TLS binding restored" "true" "$BIND_TLS_PORTS"
assert_eq "bind-addr: TLS bind addr preserved" "127.0.0.1" "$TLS_BIND_ADDR"

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
# generate_systemd_service_content: AutoTLS maps host 80/443 to container 8080/8443
# (never dead 443:443), adds no NET_BIND_SERVICE, and round-trips cleanly through the
# parser (the new 80:8080 line must not be mistaken for the web-port mapping).
# ===========================================================================
it "generate_systemd_service_content AutoTLS ports"

CONFIG_DIR="/home/pi/birdnet-go-app/config"
DATA_DIR="/home/pi/birdnet-go-app/data"
BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:nightly"
WEB_PORT="9000"; WEB_PORT_BIND_ADDR=""
BIND_TLS_PORTS="true"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""
CONFIGURED_TZ="UTC"

autotls_unit="${WORK}/autotls.service"
generate_systemd_service_content > "$autotls_unit"
assert_eq "AutoTLS unit maps host 80 -> container 8080" "1" "$(grep -c -- '-p 80:8080' "$autotls_unit")"
assert_eq "AutoTLS unit maps host 443 -> container 8443" "1" "$(grep -c -- '-p 443:8443' "$autotls_unit")"
assert_eq "AutoTLS unit has no dead 443:443 mapping" "0" "$(grep -c -- '443:443' "$autotls_unit")"
assert_eq "AutoTLS unit still publishes the web port" "1" "$(grep -c -- '-p 9000:8080' "$autotls_unit")"
assert_eq "AutoTLS unit adds no NET_BIND_SERVICE" "0" "$(grep -c 'NET_BIND_SERVICE' "$autotls_unit")"

WEB_PORT=""; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ=""
load_existing_service_config "$autotls_unit"
assert_eq "round-trip: web port restored (not 80)" "9000" "$WEB_PORT"
assert_eq "round-trip: TLS binding restored" "true" "$BIND_TLS_PORTS"
assert_eq "round-trip: TLS bind addr empty" "" "$TLS_BIND_ADDR"

# ===========================================================================
# generate_systemd_service_content: Intel iGPU passthrough is gated on has_intel_gpu.
# With detection stubbed off the unit must omit --device /dev/dri; when an Intel render
# node is present it must map /dev/dri into the container so the OpenVINO GPU plugin
# (bundled in the amd64 image) can reach it. Mirrors the /dev/snd audio gating.
# ===========================================================================
it "generate_systemd_service_content GPU passthrough"

CONFIG_DIR="/home/pi/birdnet-go-app/config"
DATA_DIR="/home/pi/birdnet-go-app/data"
BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:nightly"
WEB_PORT="9000"; WEB_PORT_BIND_ADDR=""
BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""
CONFIGURED_TZ="UTC"

# Default stub (has_intel_gpu -> 1): no Intel render node, so no device mapping.
nogpu_unit="${WORK}/nogpu.service"
generate_systemd_service_content > "$nogpu_unit"
assert_eq "no Intel GPU: unit omits --device /dev/dri" "0" "$(grep -c -- '--device /dev/dri' "$nogpu_unit")"

# Intel render node present (has_intel_gpu -> 0): exactly one /dev/dri mapping is added,
# and generation is otherwise intact (web port still published).
has_intel_gpu() { return 0; }
gpu_unit="${WORK}/gpu.service"
generate_systemd_service_content > "$gpu_unit"
assert_eq "Intel GPU: unit maps /dev/dri exactly once" "1" "$(grep -c -- '--device /dev/dri' "$gpu_unit")"
assert_eq "Intel GPU: web port still published" "1" "$(grep -c -- '-p 9000:8080' "$gpu_unit")"
has_intel_gpu() { return 1; }   # restore the deterministic default for subsequent tests

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
# parse_ssh_dest (validate the migration ssh destination)
# ===========================================================================
it "parse_ssh_dest"
ssh_dest_out="$(parse_ssh_dest 'pi@raspi4.local')"; ssh_dest_rc=$?
assert_eq "accepts user@host" "pi@raspi4.local" "$ssh_dest_out"
assert_ok "user@host returns success rc" "$ssh_dest_rc"
assert_eq "accepts bare alias" "oldpi" "$(parse_ssh_dest 'oldpi')"
assert_eq "accepts underscore alias" "old_pi" "$(parse_ssh_dest 'old_pi')"
parse_ssh_dest "" >/dev/null 2>&1; assert_nonzero "rejects empty" "$?"
parse_ssh_dest "a b" >/dev/null 2>&1; assert_nonzero "rejects whitespace" "$?"
# metachar WITHOUT whitespace: isolates the charset guard from the whitespace guard
parse_ssh_dest 'a;b' >/dev/null 2>&1; assert_nonzero "rejects semicolon (no whitespace)" "$?"
parse_ssh_dest 'host|nc' >/dev/null 2>&1; assert_nonzero "rejects pipe" "$?"
parse_ssh_dest 'host$(id)' >/dev/null 2>&1; assert_nonzero "rejects command substitution chars" "$?"
# colon is rejected: the transfer appends :$path itself, so the dest must not carry one
parse_ssh_dest 'host:22' >/dev/null 2>&1; assert_nonzero "rejects colon" "$?"
# leading dash must not be accepted (would be read as an ssh/rsync flag)
parse_ssh_dest '-oProxyCommand=x' >/dev/null 2>&1; assert_nonzero "rejects leading-dash flag-like dest" "$?"

it "remote_path_safe"
remote_path_safe "/home/pi/birdnet-go-app"; assert_ok "accepts a normal path" "$?"
remote_path_safe ""; assert_nonzero "rejects empty" "$?"
remote_path_safe "/data'; rm -rf ~"; assert_nonzero "rejects embedded single quote" "$?"

# ===========================================================================
# remote_default_app_path (default remote birdnet-go-app location)
# ===========================================================================
it "remote_default_app_path"
assert_eq "default app path from home" "/home/pi/birdnet-go-app" "$(remote_default_app_path /home/pi)"
assert_eq "root home" "/root/birdnet-go-app" "$(remote_default_app_path /root)"

# ===========================================================================
# resolve_host_timezone / _valid_iana_tz  (GitHub #3950)
# timedatectl must win over a stale /etc/timezone; each source is validated
# independently so an invalid early source no longer defeats a valid later one.
# These load the REAL functions, replacing the deterministic stub used by the
# generation tests above, so this section MUST stay after them.
# ===========================================================================
it "resolve_host_timezone"
load_fn _valid_iana_tz
load_fn resolve_host_timezone

# Hermetic zoneinfo fixture: these tests never depend on the runner's tzdata and can
# validate Etc/UTC deterministically. BNG_TZ_ZONEINFO_DIR points _valid_iana_tz at it and
# stays exported for the container-fallback and silent-mode blocks below (unset at the end).
ZI="${WORK}/zoneinfo"
mkdir -p "$ZI/America" "$ZI/Europe" "$ZI/Asia" "$ZI/Etc"
touch "$ZI/America/Chicago" "$ZI/Europe/Helsinki" "$ZI/Europe/Berlin" \
      "$ZI/Asia/Tokyo" "$ZI/Etc/UTC"
ZI="$(cd "$ZI" && pwd -P)"   # canonical, so the /etc/localtime symlink test prefix-strips cleanly
export BNG_TZ_ZONEINFO_DIR="$ZI"

tzdir="${WORK}/tz"; mkdir -p "$tzdir"
printf 'Etc/UTC\n' > "${tzdir}/etc_stale"
printf 'Europe/Helsinki\n' > "${tzdir}/etc_valid"
ln -sf "$ZI/Asia/Tokyo" "${tzdir}/localtime_tokyo"

# 1. Regression repro: stale /etc/timezone=Etc/UTC must NOT override a correct
#    timedatectl=America/Chicago when no candidate is supplied. Pre-fix this returned
#    Etc/UTC (the container-TZ reset). BNG_TZ_LOCALTIME points at a nonexistent path.
timedatectl() { echo "America/Chicago"; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/etc_stale" BNG_TZ_LOCALTIME="${tzdir}/none" resolve_host_timezone "")
assert_eq "timedatectl beats stale /etc/timezone" "America/Chicago" "$got"

# 2. Debian 13 / Forgejo #877 guard: no /etc/timezone, timedatectl still resolves.
timedatectl() { echo "Europe/Helsinki"; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/absent" BNG_TZ_LOCALTIME="${tzdir}/none" resolve_host_timezone "")
assert_eq "no /etc/timezone: timedatectl resolves (Debian 13)" "Europe/Helsinki" "$got"

# 3. Candidate preservation: an explicit prior zone wins over host detection.
timedatectl() { echo "America/Chicago"; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/etc_stale" BNG_TZ_LOCALTIME="${tzdir}/none" resolve_host_timezone "Europe/Helsinki")
assert_eq "candidate preserved over detection" "Europe/Helsinki" "$got"

# 4. timedatectl 'n/a' rejected by validation; /etc/localtime symlink resolves.
timedatectl() { echo "n/a"; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/absent" BNG_TZ_LOCALTIME="${tzdir}/localtime_tokyo" resolve_host_timezone "")
assert_eq "timedatectl n/a: falls through to /etc/localtime symlink" "Asia/Tokyo" "$got"

# 5. timedatectl absent: /etc/timezone used as last resort.
command_exists() { case "$1" in timedatectl) return 1 ;; *) command -v "$1" >/dev/null 2>&1 ;; esac; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/etc_valid" BNG_TZ_LOCALTIME="${tzdir}/none" resolve_host_timezone "")
assert_eq "no timedatectl: /etc/timezone last resort" "Europe/Helsinki" "$got"
command_exists() { command -v "$1" >/dev/null 2>&1; }   # restore

# 6. Path-traversal candidate rejected; empty when nothing else valid.
timedatectl() { echo ""; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/absent" BNG_TZ_LOCALTIME="${tzdir}/none" resolve_host_timezone "../../etc/passwd")
assert_eq "traversal candidate rejected, nothing else -> empty" "" "$got"

# 7. Invalid candidate falls through to a valid timedatectl.
timedatectl() { echo "Europe/Berlin"; }
got=$(BNG_TZ_ETC_TIMEZONE="${tzdir}/absent" BNG_TZ_LOCALTIME="${tzdir}/none" resolve_host_timezone "Not/AZone")
assert_eq "invalid candidate falls through to detection" "Europe/Berlin" "$got"

unset -f timedatectl

# ===========================================================================
# load_existing_service_config: running-container TZ fallback (GitHub #3950)
# ===========================================================================
it "load_existing_service_config container-TZ fallback"
unit_notz="${WORK}/notz.service"
cat > "$unit_notz" <<'UNIT'
[Service]
ExecStart=/usr/bin/docker run --rm --name birdnet-go \
    -p 8080:8080 \
    --env BIRDNET_UID=1000 \
    -v /x:/config
UNIT
# Unit carries no --env TZ=; the running container reports it via safe_docker inspect.
safe_docker() { printf 'PATH=/usr/bin\nTZ=Europe/Oslo\nLANG=C\n'; }
WEB_PORT=""; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ=""
load_existing_service_config "$unit_notz"
assert_eq "container TZ fallback used when unit has no TZ" "Europe/Oslo" "$CONFIGURED_TZ"

# A unit that DOES carry TZ wins over the container fallback.
unit_withtz="${WORK}/withtz.service"
cat > "$unit_withtz" <<'UNIT'
[Service]
ExecStart=/usr/bin/docker run --rm --name birdnet-go \
    -p 8080:8080 \
    --env TZ="America/Chicago" \
    -v /x:/config
UNIT
safe_docker() { printf 'TZ=Europe/Oslo\n'; }
WEB_PORT=""; WEB_PORT_BIND_ADDR=""; BIND_TLS_PORTS="false"; TLS_BIND_ADDR=""
BIND_METRICS_PORT="false"; METRICS_BIND_ADDR=""; CONFIGURED_TZ=""
load_existing_service_config "$unit_withtz"
assert_eq "unit TZ wins over container fallback" "America/Chicago" "$CONFIGURED_TZ"
safe_docker() { return 1; }   # restore default stub

# ===========================================================================
# configure_timezone silent mode: preserve a restored zone; UTC only when nothing
# resolves (GitHub #3950 - the old inline chain clobbered a restored zone).
# ===========================================================================
it "configure_timezone silent mode"
load_fn configure_timezone
SILENT_MODE="true"

# (a) a zone already restored this run is preserved (candidate wins in the resolver).
timedatectl() { echo "America/Chicago"; }
CONFIGURED_TZ="Europe/Helsinki"
BNG_TZ_ETC_TIMEZONE="${WORK}/no_such_tz" BNG_TZ_LOCALTIME="${WORK}/no_such_localtime" configure_timezone
assert_eq "silent: restored zone preserved" "Europe/Helsinki" "$CONFIGURED_TZ"

# (b) nothing detectable -> UTC.
command_exists() { case "$1" in timedatectl) return 1 ;; *) command -v "$1" >/dev/null 2>&1 ;; esac; }
CONFIGURED_TZ=""
BNG_TZ_ETC_TIMEZONE="${WORK}/no_such_tz" BNG_TZ_LOCALTIME="${WORK}/no_such_localtime" configure_timezone
assert_eq "silent: no detection -> UTC" "UTC" "$CONFIGURED_TZ"
command_exists() { command -v "$1" >/dev/null 2>&1; }
unset -f timedatectl
SILENT_MODE=""
unset BNG_TZ_ZONEINFO_DIR

# ===========================================================================
# Cross-host migration failure reporting
#
# A migration abort used to emit one opaque telemetry event ("Cross-host
# migration failed", empty diagnostics) for ~26 distinct failure paths, which
# made the resulting Sentry issue unactionable. These tests pin the three
# properties that fix depends on: the failing step is recorded, the payload is
# valid JSON (an invalid one is silently replaced by a fallback, losing every
# diagnostic), and the payload carries no user identity.
# ===========================================================================
it "migration failure reporting"

# Derived from install.sh rather than duplicated: a test asserting truncation
# against its own copy of the limit would keep passing if the real one changed.
MAX_ERROR_LENGTH="$(awk -F= '/^MAX_ERROR_LENGTH=/{print $2; exit}' "$INSTALL_SH")"
[ -n "$MAX_ERROR_LENGTH" ] || { echo "FATAL: could not read MAX_ERROR_LENGTH from install.sh" >&2; exit 2; }
for fn in json_escape migrate_fail collect_migration_diagnostics migrate_report_failure \
          remote_path_safe remote_default_app_path migrate_rerun_hint \
          resolve_remote_app check_remote_disk; do
    load_fn "$fn"
done

reset_migration_state() {
    MIGRATE_FAIL_STEP=""; MIGRATE_FAIL_KIND=""; MIGRATE_FAIL_DETAIL=""
    MIGRATE_DEST=""; MIGRATE_DEST_SOURCE="unset"; MIGRATE_REMOTE_HOME=""; MIGRATE_REMOTE_APP=""
    MIGRATE_REMOTE_APP_DEFAULT="unknown"; MIGRATE_TRANSFER_METHOD="none"
    MIGRATE_BACKUP=""; MIGRATE_STOPPED_REMOTE="0"
    MIGRATE_BACKUP_TAKEN="no"; MIGRATE_STOPPED_REMOTE_EVER="no"
    MIGRATE_REMOTE_STATE="unknown"
    MIGRATE_REMOTE_SIZE_KB=""; MIGRATE_HOME_AVAIL_KB=""
    MIGRATE_MODE="true"   # read by migrate_rerun_hint from the abort messages
    SILENT_MODE="false"
}
reset_migration_state

# --- json_escape ----------------------------------------------------------
# Every escaped value is interpolated into a JSON string literal; one stray
# quote or backslash invalidates the whole payload.
assert_eq "json_escape: quotes escaped" '\"x\"' "$(json_escape '"x"')"
# The '\\' below is a single-quoted pair of literal backslashes: JSON's escaped
# form of one backslash, which is exactly what is being asserted. shellcheck
# reads it as a botched attempt to escape a single quote.
# shellcheck disable=SC1003
assert_eq "json_escape: backslash escaped once, not doubled" '\\' "$(json_escape '\')"
assert_eq "json_escape: backslash-quote pair" '\\\"' "$(json_escape '\"')"
assert_eq "json_escape: newlines folded to spaces" 'a b' "$(json_escape 'a
b')"
assert_eq "json_escape: empty stays empty" '' "$(json_escape '')"
# Truncation happens before escaping, so a cut can never land mid-escape and
# leave a dangling backslash. The leading 'x' is load-bearing: a pure backslash
# run truncated at an even MAX_ERROR_LENGTH stays valid under BOTH orderings, so
# without it this assertion passes even when the order is reversed and proves
# nothing. The odd offset is what forces a cut mid-escape.
# '\\' is how a literal backslash is written for tr, not a quote-escape attempt.
# shellcheck disable=SC1003
long_backslashes="x$(printf '%*s' 600 '' | tr ' ' '\\')"
escaped_long="$(json_escape "$long_backslashes")"
printf '{"v":"%s"}' "$escaped_long" | jq empty 2>/dev/null
assert_ok "json_escape: truncated backslash run stays valid JSON" $?

# Under a UTF-8 locale the cut counts characters, so it cannot leave half a
# multi-byte character behind. jq tolerates a severed one (it substitutes U+FFFD)
# so this would never fail loudly; it would just ship a mangled byte.
#
# Two things here are load-bearing. The padding is exact, so the character
# STRADDLES the cut: with it anywhere else it is never severed and the assertion
# passes under byte truncation too, proving nothing. And the locale is set
# explicitly, because bash's ${x:0:n} is locale-dependent: inheriting the
# runner's locale would make this pass on a UTF-8 CI box by luck and fail for
# anyone running the suite under LC_ALL=C.
multibyte_astride="$(printf '%*s' $((MAX_ERROR_LENGTH - 1)) '' | tr ' ' 'x')$(printf '\xc3\xa9')trailing"
assert_eq "json_escape: a multi-byte character astride the cut survives intact (UTF-8 locale)" \
    "c3a9" "$(LC_ALL=C.UTF-8 json_escape "$multibyte_astride" | tail -c 2 | od -An -tx1 | tr -d ' \n')"
# The invariant that holds in EVERY locale is the one that actually matters: the
# payload stays valid JSON either way. Under LC_ALL=C the cut is byte-wise, just
# as it was with head -c, and must still not emit a dangling escape.
printf '{"v":"%s"}' "$(LC_ALL=C json_escape "$multibyte_astride")" | jq empty 2>/dev/null
assert_ok "json_escape: stays valid JSON even where the cut is byte-wise (C locale)" $?

# --- migrate_fail ---------------------------------------------------------
reset_migration_state
migrate_fail ssh_connect error "ssh_exit=255"
assert_nonzero "migrate_fail returns non-zero so callers can 'migrate_fail x; return 1'" $?
assert_eq "migrate_fail records step"   "ssh_connect"  "$MIGRATE_FAIL_STEP"
assert_eq "migrate_fail records kind"   "error"        "$MIGRATE_FAIL_KIND"
assert_eq "migrate_fail records detail" "ssh_exit=255" "$MIGRATE_FAIL_DETAIL"
migrate_fail dest_occupied_declined cancelled
assert_eq "migrate_fail defaults detail to empty (no stale detail)" "" "$MIGRATE_FAIL_DETAIL"
migrate_fail some_step
assert_eq "migrate_fail defaults kind to error" "error" "$MIGRATE_FAIL_KIND"

# --- collect_migration_diagnostics ---------------------------------------
reset_migration_state
ssh() { echo "OpenSSH_9.9p1 Debian-3, OpenSSL 3.5.4" >&2; }
migrate_fail rsync_failed error "rsync_exit=23"
diag="$(collect_migration_diagnostics)"
printf '%s' "$diag" | jq empty 2>/dev/null
assert_ok "diagnostics: emits valid JSON" $?
assert_eq "diagnostics: reports the failing step" "rsync_failed" "$(printf '%s' "$diag" | jq -r .fail_step)"
assert_eq "diagnostics: reports the detail"       "rsync_exit=23" "$(printf '%s' "$diag" | jq -r .detail)"

# A hostile detail must not be able to break the payload: invalid JSON is
# swapped for a fallback by validate_diagnostic_json, silently dropping every
# diagnostic on the event this whole change exists to capture.
reset_migration_state
migrate_fail rsync_failed error 'he said "boom" \ then
newline'
printf '%s' "$(collect_migration_diagnostics)" | jq empty 2>/dev/null
assert_ok "diagnostics: quotes/backslashes/newlines in detail keep JSON valid" $?

# Privacy: the destination is a user@host and the app paths embed the account
# name. Neither may ever reach telemetry.
#
# The key set is an ALLOW-LIST, deliberately. A deny-list that greps for the
# planted values only catches a leak of those exact values: adding a field
# holding $HOME or $USER would leak just as much identity and sail through
# (a sibling collector already ships "user": "$USER"). Asserting the exact key
# set means ANY new field fails here until it is reviewed and declared.
MIGRATION_DIAG_KEYS="backup_taken
dest_source
detail
fail_kind
fail_step
home_avail_kb
remote_app_is_default_path
remote_app_resolved
remote_size_kb
remote_state
rsync_local
silent_mode
sqlite3_local
ssh_version
stopped_remote_this_run
transfer_method"

reset_migration_state
MIGRATE_DEST="pi@secret-host.example.com"
MIGRATE_REMOTE_HOME="/home/secretuser"
MIGRATE_REMOTE_APP="/home/secretuser/birdnet-go-app"
migrate_fail ssh_connect error "ssh_exit=255"
diag="$(collect_migration_diagnostics)"
case "$diag" in
    *secret-host*|*secretuser*|*"pi@"*) leaked="yes" ;;
    *) leaked="no" ;;
esac
assert_eq "diagnostics: no hostname/username/path leaks into telemetry" "no" "$leaked"
assert_eq "diagnostics: field set is exactly the reviewed allow-list" \
    "$MIGRATION_DIAG_KEYS" "$(printf '%s' "$diag" | jq -r 'keys[]' | sort)"
assert_eq "diagnostics: reports resolution as a boolean instead" "yes" "$(printf '%s' "$diag" | jq -r .remote_app_resolved)"

# "unparseable" distinguishes "the disk really is full" from "we could not read
# the size" (a login banner on the SSH output, or du failing).
reset_migration_state
MIGRATE_REMOTE_SIZE_KB="Welcome to Debian"
MIGRATE_HOME_AVAIL_KB="123456"
diag="$(collect_migration_diagnostics)"
assert_eq "diagnostics: non-numeric remote size reported as unparseable" \
    "unparseable" "$(printf '%s' "$diag" | jq -r .remote_size_kb)"
assert_eq "diagnostics: numeric local size passed through" \
    "123456" "$(printf '%s' "$diag" | jq -r .home_avail_kb)"
reset_migration_state
assert_eq "diagnostics: size unknown before the disk check runs" \
    "unknown" "$(collect_migration_diagnostics | jq -r .remote_size_kb)"

# --- migrate_report_failure ----------------------------------------------
# A user declining a prompt is a choice, not a fault. Reporting it at error
# level inflates the error rate and buries the events worth acting on.
# Mirrors the real signature: event_type message level context diagnostics.
# $5 is captured because the whole point of the reporter is that it carries a
# diagnostics payload; without asserting it, deleting the argument entirely goes
# unnoticed and the events go back to being empty.
TELEMETRY_LEVEL=""; TELEMETRY_MSG=""; TELEMETRY_CTX=""; TELEMETRY_DIAG=""
send_telemetry_event() { TELEMETRY_LEVEL="$3"; TELEMETRY_MSG="$2"; TELEMETRY_CTX="$4"; TELEMETRY_DIAG="${5:-}"; }

reset_migration_state
migrate_fail rsync_failed error "rsync_exit=23"
migrate_report_failure
assert_eq "report: real failure is error level" "error" "$TELEMETRY_LEVEL"
assert_eq "report: message names the step so Sentry groups per step" \
    "Cross-host migration failed: rsync_failed" "$TELEMETRY_MSG"
assert_eq "report: context carries the step" \
    "step=remote_migrate,result=failure,error=rsync_failed" "$TELEMETRY_CTX"
printf '%s' "$TELEMETRY_DIAG" | jq empty 2>/dev/null
assert_ok "report: passes a valid JSON diagnostics payload as the 5th arg" $?
assert_eq "report: the payload carries the failing step" \
    "rsync_failed" "$(printf '%s' "$TELEMETRY_DIAG" | jq -r .fail_step)"

reset_migration_state
migrate_fail dest_occupied_declined cancelled
migrate_report_failure
assert_eq "report: user cancellation is info level, not error" "info" "$TELEMETRY_LEVEL"
assert_eq "report: cancellation message says cancelled" \
    "Cross-host migration cancelled: dest_occupied_declined" "$TELEMETRY_MSG"

reset_migration_state
migrate_report_failure
assert_eq "report: unrecorded failure degrades to 'unknown', never empty" \
    "Cross-host migration failed: unknown" "$TELEMETRY_MSG"
unset -f send_telemetry_event

# --- login-banner tolerance ----------------------------------------------
# A remote .bashrc that echoes before Debian's non-interactive guard prepends a
# banner to every ssh command's output. The banner is written by the remote
# shell outside the command pipeline, so only a LOCAL tail -n 1 drops it.
reset_migration_state
MIGRATE_DEST="host"
migrate_ssh() {
    case "$1" in
        *printf*) printf 'Welcome to Debian GNU/Linux 13\nLast login: today\n%s' "/home/pi" ;;
        *"test -f"*) case "$1" in *"/home/pi/birdnet-go-app/config/config.yaml"*) return 0 ;; *) return 1 ;; esac ;;
        *) return 1 ;;
    esac
}
resolve_remote_app
assert_ok "banner: remote install still resolves through a login banner" $?
assert_eq "banner: remote home is the path, not the banner text" "/home/pi" "$MIGRATE_REMOTE_HOME"
assert_eq "banner: app path derived cleanly" "/home/pi/birdnet-go-app" "$MIGRATE_REMOTE_APP"
assert_eq "banner: default path recorded for telemetry" "yes" "$MIGRATE_REMOTE_APP_DEFAULT"

# du's size must survive the same banner, or the disk check aborts in silent
# mode claiming it "could not verify free disk space".
reset_migration_state
MIGRATE_REMOTE_APP="/home/pi/birdnet-go-app"
SILENT_MODE="true"
migrate_ssh() { printf 'Welcome to Debian GNU/Linux 13\n%s\n' "1000"; }
df() { printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/x 100 1 999999 1%% /\n'; }
check_remote_disk
assert_ok "banner: disk check reads the size through a banner" $?
assert_eq "banner: remote size captured numerically for telemetry" "1000" "$MIGRATE_REMOTE_SIZE_KB"
unset -f migrate_ssh df ssh
reset_migration_state

# --- a noisy remote shell is refused BEFORE anything is moved or stopped ------
# rsync and tar stream over the same shell, so a host that greets us cannot be
# migrated at all. Failing here costs nothing; failing at the transfer costs the
# user their data being moved aside and their source service stopped.
load_fn check_remote_shell_clean
reset_migration_state
MIGRATE_DEST="host"
migrate_ssh() { printf 'Welcome to Debian GNU/Linux 13\n'; }
check_remote_shell_clean
assert_nonzero "noisy shell: a greeting remote is refused" $?
assert_eq "noisy shell: records its own slug" "remote_shell_noisy" "$MIGRATE_FAIL_STEP"
assert_eq "noisy shell: is an error, not a cancellation" "error" "$MIGRATE_FAIL_KIND"

# A .bashrc printing only a blank line is the hard case: command substitution
# strips trailing newlines, so it reads as silence unless the probe defends
# against it -- yet a lone newline corrupts rsync exactly like a banner does.
for blank in 'printf "\n"' 'printf "\n\n"'; do
    reset_migration_state
    MIGRATE_DEST="host"
    eval "migrate_ssh() { $blank; }"
    check_remote_shell_clean
    assert_nonzero "noisy shell: newline-only noise ($blank) is refused" $?
    assert_eq "noisy shell: newline-only noise records the slug ($blank)" \
        "remote_shell_noisy" "$MIGRATE_FAIL_STEP"
done

# stderr does not ride the payload stream, so a stderr banner must NOT be
# treated as noise: refusing those hosts would block migrations that work.
reset_migration_state
MIGRATE_DEST="host"
migrate_ssh() { echo "banner on stderr" >&2; }
check_remote_shell_clean
assert_ok "noisy shell: a stderr-only banner is NOT treated as noise" $?
assert_eq "noisy shell: stderr banner records no failure" "" "$MIGRATE_FAIL_STEP"

# A dead connection produces no stdout, which is indistinguishable from a clean
# shell unless the probe carries its own exit status back. Reporting "clean"
# here would blame whatever step failed next for an SSH drop.
reset_migration_state
MIGRATE_DEST="host"
migrate_ssh() { return 255; }
check_remote_shell_clean
assert_nonzero "noisy shell: a dropped connection is not mistaken for silence" $?
assert_eq "noisy shell: a dropped probe records its own slug" \
    "remote_probe_failed" "$MIGRATE_FAIL_STEP"
assert_eq "noisy shell: a dropped probe records ssh's exit code" \
    "ssh_exit=255" "$MIGRATE_FAIL_DETAIL"

migrate_ssh() { :; }
reset_migration_state
MIGRATE_DEST="host"
check_remote_shell_clean
assert_ok "noisy shell: a quiet remote passes" $?
assert_eq "noisy shell: quiet remote records no failure" "" "$MIGRATE_FAIL_STEP"
unset -f migrate_ssh

# --- production abort paths record the slug the reporter will publish ---------
# Exercising migrate_fail directly only proves the helper stores what it is
# handed. The contract that matters is the slug each real abort path chooses:
# rename them all and a mechanism-only suite stays green while every Sentry
# issue is misfiled. These drive the real functions.
load_fn check_remote_stopped

reset_migration_state
SILENT_MODE="true"
MIGRATE_REMOTE_APP="/home/pi/birdnet-go-app"
migrate_ssh() { printf '%s\n' "5000"; }                     # remote wants 5000 KB
df() { printf 'h\n/dev/x 100 1 10 1%% /\n'; }               # only 10 KB free
check_remote_disk
assert_nonzero "abort: insufficient disk returns non-zero" $?
assert_eq "abort: insufficient disk records disk_insufficient" "disk_insufficient" "$MIGRATE_FAIL_STEP"
assert_eq "abort: insufficient disk is an error" "error" "$MIGRATE_FAIL_KIND"

reset_migration_state
SILENT_MODE="true"
MIGRATE_REMOTE_APP="/home/pi/birdnet-go-app"
migrate_ssh() { printf 'not-a-number\n'; }                  # du unreadable
df() { printf 'h\n/dev/x 100 1 999999 1%% /\n'; }
check_remote_disk
assert_nonzero "abort: unverifiable disk aborts in silent mode" $?
assert_eq "abort: unverifiable disk records disk_unverified_silent" \
    "disk_unverified_silent" "$MIGRATE_FAIL_STEP"

reset_migration_state
MIGRATE_DEST="host"
SILENT_MODE="true"
migrate_ssh() { case "$1" in *printf*) printf '\n' ;; *) return 1 ;; esac; }
resolve_remote_app
assert_nonzero "abort: unusable remote home returns non-zero" $?
assert_eq "abort: unusable remote home records remote_home_unresolved" \
    "remote_home_unresolved" "$MIGRATE_FAIL_STEP"

reset_migration_state
MIGRATE_DEST="host"
SILENT_MODE="true"
migrate_ssh() { case "$1" in *printf*) printf '%s' "/home/pi" ;; *) return 1 ;; esac; }
resolve_remote_app
assert_nonzero "abort: no remote install returns non-zero" $?
assert_eq "abort: no remote install records remote_app_not_found" \
    "remote_app_not_found" "$MIGRATE_FAIL_STEP"

# check_remote_stopped is a PROBE: returning 1 is a normal branch ("maybe
# running"), not a failure, so it must never record a slug. But it must say WHY,
# or the caller reports "service was running" when the truth is we never got an
# answer.
reset_migration_state
migrate_ssh() { printf 'active\n'; }
check_remote_stopped
assert_nonzero "probe: an active service is not confirmed stopped" $?
assert_eq "probe: records state=running" "running" "$MIGRATE_REMOTE_STATE"
assert_eq "probe: records NO slug (its return 1 is a branch, not an abort)" "" "$MIGRATE_FAIL_STEP"

reset_migration_state
migrate_ssh() { printf '\n'; }                              # systemctl gave no answer
check_remote_stopped
assert_nonzero "probe: no answer is not confirmed stopped" $?
assert_eq "probe: an unanswered probe is 'unknown', not 'running'" "unknown" "$MIGRATE_REMOTE_STATE"

reset_migration_state
migrate_ssh() { return 255; }                               # ssh dropped
check_remote_stopped
assert_nonzero "probe: dropped ssh is not confirmed stopped" $?
assert_eq "probe: dropped ssh is 'unknown', not 'running'" "unknown" "$MIGRATE_REMOTE_STATE"

reset_migration_state
migrate_ssh() { case "$1" in *systemctl*) printf 'inactive\n' ;; *) printf '\n' ;; esac; }
check_remote_stopped
assert_ok "probe: inactive service with no container is confirmed stopped" $?
assert_eq "probe: records state=stopped" "stopped" "$MIGRATE_REMOTE_STATE"

# The docker half of the same fail-closed contract. systemd says "not running",
# but a live container means the database is live: confirming "stopped" here
# would let the transfer copy a database mid-write.
reset_migration_state
migrate_ssh() { case "$1" in *systemctl*) printf 'inactive\n' ;; *) printf 'birdnet-go\n' ;; esac; }
check_remote_stopped
assert_nonzero "probe: a running container is NOT confirmed stopped" $?
assert_eq "probe: a running container is state=running" "running" "$MIGRATE_REMOTE_STATE"

reset_migration_state
migrate_ssh() { case "$1" in *systemctl*) printf 'inactive\n' ;; *) return 255 ;; esac; }
check_remote_stopped
assert_nonzero "probe: a dropped ssh on the docker check is not confirmed stopped" $?
assert_eq "probe: dropped ssh on the docker check is 'unknown'" "unknown" "$MIGRATE_REMOTE_STATE"

# `docker ps` exits non-zero only when it could not answer (daemon down, user
# not in the docker group). Swallowing that with `|| true` on the remote turns
# "could not check" into empty output and then into "confirmed stopped", and the
# transfer copies a live database. The remote command must carry the failure.
assert_eq "probe: the docker check does not swallow its own failure with '|| true'" "0" \
    "$(grep -c 'docker ps --filter name=birdnet-go --format "{{.Names}}" 2>/dev/null || true' "$INSTALL_SH")"
# ...while the systemctl probe MUST keep its `|| true`: is-active exits non-zero
# to answer "inactive", which is a normal answer, not a failure.
assert_eq "probe: the systemctl check keeps its '|| true' (non-zero is an answer)" "1" \
    "$(grep -c 'systemctl is-active birdnet-go.service 2>/dev/null || true' "$INSTALL_SH")"

# grep -Fx matches the WHOLE line: a container merely named like ours (or shell
# noise containing the name) must not count as our container running.
reset_migration_state
migrate_ssh() { case "$1" in *systemctl*) printf 'inactive\n' ;; *) printf 'birdnet-go-sidecar\n' ;; esac; }
check_remote_stopped
assert_ok "probe: a similarly-named container does not count as ours" $?

unset -f migrate_ssh df

# --- ssh's real exit code reaches the report ---------------------------------
# `if ! ssh ...; then rc=$?` records the NEGATED status, i.e. 0, for every
# failure. That reads as a successful connection in the payload: a plausible
# lie is worse than no field at all, so pin the real code.
load_fn open_ssh_master
reset_migration_state
MIGRATE_DEST="host"
MIGRATE_SSH_DIR=""; MIGRATE_SSH_SOCKET=""
mktemp() { mkdir -p "${WORK}/sshmaster"; printf '%s' "${WORK}/sshmaster"; }
ssh() { return 255; }
open_ssh_master
assert_nonzero "ssh master: an unreachable host returns non-zero" $?
assert_eq "ssh master: records ssh_connect" "ssh_connect" "$MIGRATE_FAIL_STEP"
assert_eq "ssh master: records ssh's REAL exit code, not the negated status" \
    "ssh_exit=255" "$MIGRATE_FAIL_DETAIL"

reset_migration_state
MIGRATE_DEST="host"
MIGRATE_SSH_DIR=""; MIGRATE_SSH_SOCKET=""
ssh() { return 0; }
open_ssh_master
assert_ok "ssh master: a reachable host succeeds" $?
assert_eq "ssh master: success records no failure" "" "$MIGRATE_FAIL_STEP"

reset_migration_state
MIGRATE_DEST="host"
MIGRATE_SSH_DIR=""; MIGRATE_SSH_SOCKET=""
mktemp() { return 1; }
open_ssh_master
assert_nonzero "ssh master: unusable temp dir returns non-zero" $?
assert_eq "ssh master: records tmpdir_failed" "tmpdir_failed" "$MIGRATE_FAIL_STEP"
unset -f mktemp ssh

# --- "running" and "could not tell" must not become the same Sentry issue ----
# The silent-mode abort picks its slug from the probe's recorded state. Merging
# these back together re-creates exactly the conflation this change exists to
# remove: one issue that says "stop the service" to people whose service was
# never running.
reset_migration_state
MIGRATE_REMOTE_STATE="running"
if [ "$MIGRATE_REMOTE_STATE" = "running" ]; then migrate_fail remote_running_silent; else migrate_fail remote_state_unknown_silent; fi
assert_eq "silent abort: a confirmed-running source reports remote_running_silent" \
    "remote_running_silent" "$MIGRATE_FAIL_STEP"
reset_migration_state
MIGRATE_REMOTE_STATE="unknown"
if [ "$MIGRATE_REMOTE_STATE" = "running" ]; then migrate_fail remote_running_silent; else migrate_fail remote_state_unknown_silent; fi
assert_eq "silent abort: an unanswered probe reports remote_state_unknown_silent" \
    "remote_state_unknown_silent" "$MIGRATE_FAIL_STEP"

# Both slugs must actually exist at the real call site, or the branch above is
# testing a copy of the logic rather than the shipped logic.
assert_eq "silent abort: both slugs are wired into install.sh" "2" \
    "$(grep -cE 'migrate_fail (remote_running_silent|remote_state_unknown_silent)$' "$INSTALL_SH")"

# --- the record globals are actually written, at the right moment -------------
# migrate_rollback clears MIGRATE_BACKUP and restarts the source BEFORE the
# failure is reported, so the reporter reads report-only records instead. Pin
# both halves: that the records are WRITTEN where the fact becomes true, and
# that the reporter reads them rather than the live state.
assert_eq "records: backup_taken is set after the mv succeeds, not before" "1" \
    "$(grep -cE '^        MIGRATE_BACKUP_TAKEN="yes"$' "$INSTALL_SH")"
assert_eq "records: stopped_remote_ever is set where the source is stopped" "1" \
    "$(grep -cE '^                MIGRATE_STOPPED_REMOTE_EVER="yes"$' "$INSTALL_SH")"

# The rollback restart flag must be armed BEFORE the stop is issued. Armed only
# after a confirmed stop, a dropped verification round trip leaves the flag at
# "0" and rollback walks away with the user's source service still down.
arm_line="$(grep -n '^            MIGRATE_STOPPED_REMOTE="1"$' "$INSTALL_SH" | head -1 | cut -d: -f1)"
issue_line="$(grep -n "ControlPath=\"\$MIGRATE_SSH_SOCKET\".*systemctl stop birdnet-go.service" "$INSTALL_SH" | head -1 | cut -d: -f1)"
arm_rc=0
{ [ -n "$arm_line" ] && [ -n "$issue_line" ] && [ "$arm_line" -lt "$issue_line" ]; } || arm_rc=1
assert_ok "records: the rollback restart flag is armed BEFORE the stop is issued" "$arm_rc"

reset_migration_state
MIGRATE_BACKUP_TAKEN="yes"; MIGRATE_STOPPED_REMOTE_EVER="yes"
MIGRATE_BACKUP=""; MIGRATE_STOPPED_REMOTE="0"              # what rollback leaves behind
ssh() { echo "OpenSSH_9.9p1" >&2; }
migrate_fail rsync_failed error "rsync_exit=23"
diag="$(collect_migration_diagnostics)"
assert_eq "records: backup_taken survives the rollback that cleared it" \
    "yes" "$(printf '%s' "$diag" | jq -r .backup_taken)"
assert_eq "records: stopped_remote survives the rollback that restarted it" \
    "yes" "$(printf '%s' "$diag" | jq -r .stopped_remote_this_run)"
unset -f ssh
reset_migration_state

# --- the noisy-shell probe is actually wired in ------------------------------
# The function is unit-tested above; this pins that migrate_from_remote_host
# calls it, and calls it BEFORE the steps that move data and stop the service.
probe_line="$(grep -n 'check_remote_shell_clean || return 1' "$INSTALL_SH" | cut -d: -f1)"
backup_line="$(grep -n 'MIGRATE_BACKUP="\${dest_dir}' "$INSTALL_SH" | cut -d: -f1)"
# Anchor on the migration's OWN stop (the one issued over the ssh master), not
# the unrelated local `systemctl stop` sites elsewhere in the installer.
stop_line="$(grep -n "ControlPath=\"\$MIGRATE_SSH_SOCKET\".*systemctl stop birdnet-go.service" "$INSTALL_SH" | head -1 | cut -d: -f1)"
assert_eq "wiring: the noisy-shell probe is called from the migration flow" \
    "1" "$(grep -c 'check_remote_shell_clean || return 1' "$INSTALL_SH")"
[ -n "$probe_line" ] && [ -n "$backup_line" ] && [ "$probe_line" -lt "$backup_line" ]
assert_ok "wiring: the probe runs BEFORE the user's data is moved aside" $?
[ -n "$probe_line" ] && [ -n "$stop_line" ] && [ "$probe_line" -lt "$stop_line" ]
assert_ok "wiring: the probe runs BEFORE the source service is stopped" $?

# ===========================================================================
# Result
# ===========================================================================
echo "------------------------------------------------------------"
echo "install.sh helper tests: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
