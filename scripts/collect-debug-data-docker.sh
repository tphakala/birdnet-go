#!/bin/bash
# BirdNET-Go Debug Data Collection Script for Docker Deployments
# This script collects debug data from BirdNET-Go running in a Docker container

set -euo pipefail

# Configuration
CONTAINER_NAME="${BIRDNET_CONTAINER:-birdnet-go}"
BIRDNET_PORT="${BIRDNET_PORT:-8080}"
OUTPUT_DIR="debug-data-docker-$(date +%Y%m%d-%H%M%S)"
PROFILE_DURATION="${PROFILE_DURATION:-30}"
# Seconds to wait for a TCP connection when probing whether the server is up,
# and an overall ceiling for the same probe. Both are needed: see http_status.
PROBE_CONNECT_TIMEOUT="${PROBE_CONNECT_TIMEOUT:-15}"
PROBE_MAX_TIME="${PROBE_MAX_TIME:-120}"
# Docker image name from install.sh
BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:nightly"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Function to check and install Go if needed
check_go_installation() {
    if command -v go &> /dev/null; then
        GO_VERSION=$(go version | awk '{print $3}')
        print_status "Go is installed: ${GO_VERSION}"
        return 0
    fi
    
    print_warning "Go is not installed. Go is optional but recommended for analyzing profiling data."
    echo ""
    
    # Offer automatic installation
    read -p "Would you like to install Go 1.24.4 automatically? (y/N): " -n 1 -r
    echo ""
    
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_status "Installing Go 1.24.4..."
        
        # Detect architecture
        ARCH=$(dpkg --print-architecture)
        GO_ARCH=""
        case $ARCH in
            amd64) GO_ARCH="amd64" ;;
            arm64) GO_ARCH="arm64" ;;
            armhf) GO_ARCH="armv6l" ;;
            *) 
                print_error "Unsupported architecture: $ARCH"
                return 1
                ;;
        esac
        
        # Download and install Go
        GO_VERSION="1.24.4"
        GO_TAR="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
        GO_URL="https://go.dev/dl/${GO_TAR}"
        
        print_status "Downloading Go from ${GO_URL}..."
        if wget -q --show-progress "${GO_URL}" -O "/tmp/${GO_TAR}"; then
            print_status "Installing Go to /usr/local..."
            sudo rm -rf /usr/local/go
            sudo tar -C /usr/local -xzf "/tmp/${GO_TAR}"
            rm "/tmp/${GO_TAR}"
            
            # Add to PATH if not already there
            if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
                echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
            fi
            
            # Export for current session
            export PATH=$PATH:/usr/local/go/bin
            
            print_status "✓ Go ${GO_VERSION} installed successfully!"
            print_status "PATH updated. For new terminals, run: source ~/.bashrc"
            return 0
        else
            print_error "Failed to download Go"
            return 1
        fi
    else
        echo ""
        echo "Manual installation instructions:"
        echo ""
        echo "Option 1: Install from official repository (recommended):"
        echo "  wget https://go.dev/dl/go1.24.4.linux-\$(dpkg --print-architecture).tar.gz"
        echo "  sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.4.linux-\$(dpkg --print-architecture).tar.gz"
        echo "  echo 'export PATH=\$PATH:/usr/local/go/bin' >> ~/.bashrc"
        echo "  source ~/.bashrc"
        echo ""
        echo "Option 2: Install from apt (older version):"
        echo "  sudo apt update && sudo apt install -y golang-go"
        echo ""
        echo "Option 3: Use Docker for analysis (no installation needed):"
        echo "  Analysis script will provide Docker commands"
        echo ""
        return 1
    fi
}

# Function to print colored output
# The message is printed with %s, never %b, so backslashes in it are emitted
# literally. Only the colour codes are interpreted. This matters because one
# caller prints an awk program for the user to copy: `echo -e` decodes \0nnn,
# which turned the program's \047 into a bare ' that closed its own quoting and
# made the printed command unpasteable.
print_status() {
    printf '%b[%s]%b %s\n' "${GREEN}" "$(date +'%H:%M:%S')" "${NC}" "$1"
}

print_warning() {
    printf '%b[%s] WARNING:%b %s\n' "${YELLOW}" "$(date +'%H:%M:%S')" "${NC}" "$1"
}

print_error() {
    printf '%b[%s] ERROR:%b %s\n' "${RED}" "$(date +'%H:%M:%S')" "${NC}" "$1"
}

# Function to check if container is running
check_container() {
    print_status "Checking if container '${CONTAINER_NAME}' is running..."
    
    # Check both by name and by image
    if ! docker ps --format "table {{.Names}}" | grep -q "^${CONTAINER_NAME}$"; then
        # Also check if any container is running with the BirdNET-Go image
        if docker ps --format "table {{.Names}}\t{{.Image}}" | grep -q "${BIRDNET_GO_IMAGE}"; then
            print_warning "Container named '${CONTAINER_NAME}' not found, but found BirdNET-Go container:"
            docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" | grep "${BIRDNET_GO_IMAGE}"
            echo ""
            echo "Please specify the correct container name:"
            echo "  BIRDNET_CONTAINER=<actual-container-name> $0"
        else
            print_error "No BirdNET-Go container is running"
            print_error "Available containers:"
            docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"
        fi
        echo ""
        echo "To start BirdNET-Go:"
        echo "  sudo systemctl start birdnet-go"
        exit 1
    fi
    
    print_status "✓ Container '${CONTAINER_NAME}' is running"
}

# Function to get container's internal IP or use localhost with mapped port
get_container_url() {
    # First, try to get the mapped port
    local mapped_port=$(docker port "${CONTAINER_NAME}" ${BIRDNET_PORT} 2>/dev/null | cut -d: -f2)
    
    if [ -n "${mapped_port}" ]; then
        # Port is mapped to host
        BASE_URL="http://localhost:${mapped_port}"
        print_status "Using mapped port: localhost:${mapped_port}"
    else
        # Try to get container's IP address
        local container_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${CONTAINER_NAME}" 2>/dev/null | head -n1)
        
        if [ -n "${container_ip}" ]; then
            BASE_URL="http://${container_ip}:${BIRDNET_PORT}"
            print_status "Using container IP: ${container_ip}:${BIRDNET_PORT}"
        else
            print_error "Could not determine how to connect to container"
            print_error "Make sure port ${BIRDNET_PORT} is exposed or container is on accessible network"
            exit 1
        fi
    fi
}

# http_status prints the HTTP status code of a GET, or 000 when no response
# arrived at all (connection refused, DNS failure, timeout).
#
# Deliberately not `curl -f`, which collapses every 4xx and 5xx into exit code
# 22 and one indistinguishable message. The status code is the whole diagnosis
# here: 404 means profiling is off, 403 means the token is wrong, 401 means an
# auth provider is configured instead, and 410 means the caller is talking to
# the old telemetry listener. See the troubleshooting section of
# doc/PROFILING.md, which tells users to triage on exactly these codes.
#
# The suppression is `|| true`, NOT `|| status=""`. %{http_code} already prints
# 000 for every case where no response arrived, so it is the three-valued answer
# on its own; discarding curl's stdout on a non-zero exit only destroys a real
# code. curl exits 18 for a partial transfer the server answered 200 to, and
# reporting that as 000 would send the reader to "check the host and port" for a
# server that replied.
#
# The two timeouts compose and both are needed; they are not alternatives.
# --connect-timeout bounds a black-holed host (SYN dropped by a firewall), which
# otherwise hangs for curl's 300-second default. --max-time bounds a server that
# ACCEPTS the connection and then never answers, which --connect-timeout does not
# cover at all: a deadlocked handler, a SIGSTOPped process, a proxy with a hung
# upstream. Measured against an accept-then-stall listener, --connect-timeout
# alone never returns. That pathology is one this collector exists to diagnose,
# so hanging on it is the worst possible response.
#
# --max-time is deliberately generous. These two probes fetch cheap endpoints,
# so 120s is far beyond any healthy response, and keeping it large is what stops
# a loaded Raspberry Pi slow to first byte from being misreported as 000, "no
# HTTP response at all". Neither probe fetches a profile; collect_profile does,
# and it sets no total cap because a CPU profile legitimately runs for
# PROFILE_DURATION seconds.
http_status() {
    local status
    status=$(curl -s --connect-timeout "${PROBE_CONNECT_TIMEOUT}" --max-time "${PROBE_MAX_TIME}" \
        -o /dev/null -w '%{http_code}' "$1" 2>/dev/null) || true
    printf '%s' "${status:-000}"
}

# Function to check if BirdNET-Go is accessible
check_connectivity() {
    print_status "Checking connectivity to BirdNET-Go..."
    # Any HTTP response proves the server is there. An instance with
    # authentication enabled answers the root path with 302 or 401, which is a
    # working server rather than a connectivity failure.
    if [ "$(http_status "${BASE_URL}")" = "000" ]; then
        print_error "Cannot connect to BirdNET-Go at ${BASE_URL}"
        print_error "Please ensure BirdNET-Go is running in the container"
        exit 1
    fi
    print_status "✓ Connected to BirdNET-Go"
}

# Profiling token, required when BirdNET-Go has no authentication provider
# configured. Set BIRDNET_PROFILING_TOKEN to the diagnostics.profiling.token
# value from config.yaml. When authentication IS configured, the endpoints sit
# behind it instead and this stays empty.
# Note: these collectors support unauthenticated and token-gated instances only;
# they cannot log in to an instance protected by basic auth or OAuth.
PROFILING_TOKEN="${BIRDNET_PROFILING_TOKEN:-}"

# PROFILING_TOKEN_AWK extracts diagnostics.profiling.token from a config file.
#
# SIX copies of this program exist and must stay byte-identical. Counting
# occurrences, not files, because doc/PROFILING.md alone carries three:
#   scripts/collect-debug-data.sh          1
#   scripts/collect-debug-data-docker.sh   1
#   doc/PROFILING.md                       3  (plain read, docker exec, export)
#   doc/DEBUG-COLLECTION.md                1
# doc/DEBUG-COLLECTION.md carries a SEVENTH near-copy that shares this whole
# prologue and swaps the final rule to print `enabled:`; it must track any
# change to the section-scoping rules below.
#
# Every clause earns its place, so do not simplify it back to a grep:
#   - the BOM strip lets '^diagnostics:' match a hand-edited config that was
#     saved with one; without it the section is never entered and the command
#     prints nothing, which the docs then misdiagnose as "no token minted";
#   - the section is bounded at the next top-level key, because other sections
#     have token: keys of their own and an unbounded search prints a
#     notification provider's secret instead;
#   - it is scoped to the profiling: subsection and LEAVES that subsection at
#     the next key indented no deeper than profiling: itself. Bounding only at
#     the next top-level key is not enough: a sibling subsection of diagnostics
#     placed AFTER profiling: is still indented, so it never closed the section
#     and its token: won instead. Latent today rather than live, and the
#     distinction is worth stating: DiagnosticsConfig has only Profiling, and
#     notification: is a TOP-level key that the top-level bound already handled.
#     It becomes a disclosure path the moment a diagnostics subsection carrying
#     a secret is added, which is not a change anyone would think to re-audit
#     this awk for;
#   - it trims only the ENDS of the value, and strips one surrounding pair of
#     quotes of either kind. An earlier version gsub'd all whitespace, which
#     silently mangled a hand-set token containing a space, contradicting
#     urlencode below, which is explicitly built to carry one;
#   - the quote strip uses \047 rather than a literal ' so the printed command
#     survives being pasted into a single-quoted shell string. That only holds
#     because print_error uses printf %s; echo -e would decode \047 back into a
#     bare quote and break the very thing this avoids.
# Verified against gawk, mawk, busybox awk and nawk over twelve config shapes.
#
# Known limitation, deliberately not coded around: a '#' inside a quoted token
# is treated as the start of a trailing comment. Generated tokens are base64url
# so this is unreachable for them, and handling it properly needs a real parser.
PROFILING_TOKEN_AWK='NR==1{sub(/^\357\273\277/,"")} /^[^[:space:]#]/{d=($0~/^diagnostics:/);p=0;next} d&&p&&/[^[:space:]]/{i=match($0,/[^[:space:]]/);if(i<=pi&&substr($0,i,1)!="#")p=0} d&&!p&&/^[[:space:]]*profiling:/{p=1;pi=match($0,/[^[:space:]]/);next} d&&p&&/^[[:space:]]*token:/{sub(/\r$/,"");sub(/^[[:space:]]*token:[[:space:]]*/,"");sub(/[[:space:]]+#.*$/,"");sub(/[[:space:]]+$/,"");if($0~/^".*"$/||$0~/^\047.*\047$/)$0=substr($0,2,length($0)-2);print;exit}'

# urlencode percent-encodes a string for safe use in a URL query value.
# The generated token is base64url and needs no encoding, but a hand-configured
# one may contain &, =, + or spaces, which would silently corrupt the request.
urlencode() {
    # LC_ALL=C forces byte-oriented iteration. Under a UTF-8 locale bash walks
    # CHARACTERS, so a token containing e.g. 'e-acute' would be emitted as its
    # single code point (%E9) rather than its UTF-8 bytes (%C3%A9), and the
    # server would receive a different token than the one configured.
    local LC_ALL=C
    local raw=$1
    local i char out=""
    for (( i = 0; i < ${#raw}; i++ )); do
        char=${raw:i:1}
        case "$char" in
            [a-zA-Z0-9.~_-]) out+="$char" ;;
            *) out+=$(printf '%%%02X' "'$char") ;;
        esac
    done
    printf '%s' "$out"
}

# auth_query returns the query-string fragment carrying the profiling token,
# with the correct separator for a URL that may already have parameters.
auth_query() {
    local separator=${1:-?}
    if [ -n "${PROFILING_TOKEN}" ]; then
        printf '%stoken=%s' "${separator}" "$(urlencode "${PROFILING_TOKEN}")"
    fi
}

# Function to check that the profiling endpoints are reachable
check_debug_mode() {
    print_status "Checking if profiling is enabled..."
    local status
    status=$(http_status "${BASE_URL}/debug/pprof/$(auth_query)")
    if [ "${status}" = "200" ]; then
        print_status "✓ Profiling is enabled"
        return 0
    fi

    print_error "Profiling endpoints are not accessible (HTTP ${status})"
    case "${status}" in
        404)
            print_error "  Profiling is disabled. Note that 'debug: true' does not enable it;"
            print_error "  it only controls logging verbosity."
            print_warning "  1. Set 'diagnostics.profiling.enabled: true' in config.yaml"
            print_warning "  2. docker restart ${CONTAINER_NAME}"
            ;;
        403)
            print_error "  The token is missing or wrong. Export the generated one:"
            print_error "  export BIRDNET_PROFILING_TOKEN=\"\$(docker exec ${CONTAINER_NAME} awk '${PROFILING_TOKEN_AWK}' /config/config.yaml)\""
            ;;
        401 | 302)
            print_error "  An authentication provider is configured, so the token is never"
            print_error "  consulted and a session is required instead. This collector cannot"
            print_error "  log in; see the login-then-fetch recipe in doc/PROFILING.md."
            ;;
        410)
            print_error "  This is the telemetry listener (default port 8090). The endpoints"
            print_error "  moved to the web server port (default 8080); set BIRDNET_PORT to it."
            ;;
        000)
            print_error "  No HTTP response at all. Check that the container port is reachable."
            ;;
        *)
            print_error "  Unexpected status. See the troubleshooting section of doc/PROFILING.md."
            ;;
    esac
    exit 1
}

# Function to collect a profile
collect_profile() {
    local profile_type=$1
    local output_file=$2
    local url_params=${3:-""}
    local separator="?"
    if [ -n "${url_params}" ]; then
        separator="&"
    fi

    print_status "Collecting ${profile_type} profile..."
    # Unlike http_status, this needs BOTH the status and the exit code. A
    # truncated transfer reports 200 with an incomplete body (curl exit 18), and
    # trusting the status alone would keep a half-written .pprof that
    # go tool pprof rejects later, which is the outcome the rm below exists to
    # prevent.
    #
    # --connect-timeout but NOT --max-time. The two bound different phases: a
    # CPU profile legitimately runs for PROFILE_DURATION seconds, so a total cap
    # would truncate it, but nothing about that makes the CONNECT phase
    # unbounded-by-design. Without it a black-holed host stalls every profile
    # fetch for curl's 300-second default.
    local status rc=0
    status=$(curl -s --connect-timeout "${PROBE_CONNECT_TIMEOUT}" -o "${output_file}" -w '%{http_code}' \
        "${BASE_URL}/debug/pprof/${profile_type}${url_params}$(auth_query "${separator}")" 2>/dev/null) || rc=$?
    if [ "${rc}" -eq 0 ] && [ "${status:-000}" = "200" ]; then
        print_status "✓ Collected ${profile_type} profile → ${output_file}"
    else
        # The body of a failed request is an error page or a partial profile,
        # and a file that go tool pprof rejects later is worse than no file.
        rm -f "${output_file}"
        if [ "${status:-000}" = "200" ]; then
            print_warning "Failed to collect ${profile_type} profile (server answered 200 but the transfer did not complete; curl exit ${rc})"
        else
            print_warning "Failed to collect ${profile_type} profile (HTTP ${status:-000})"
        fi
    fi
}

# Function to collect container information
collect_container_info() {
    local output_file=$1
    
    print_status "Collecting container information..."
    {
        echo "=== Container Information ==="
        echo "Date: $(date)"
        echo "Container Name: ${CONTAINER_NAME}"
        echo ""
        
        echo "=== Container Details ==="
        docker inspect "${CONTAINER_NAME}" | jq -r '.[0] | {
            State: .State.Status,
            Started: .State.StartedAt,
            Image: .Config.Image,
            Memory: .HostConfig.Memory,
            CPUs: .HostConfig.CpuQuota,
            RestartPolicy: .HostConfig.RestartPolicy,
            Mounts: .Mounts,
            Env: (.Config.Env | map(select(test("BIRDNET_|PUID|PGID|TZ"))))
        }' 2>/dev/null || docker inspect "${CONTAINER_NAME}" --format '{{json .}}' | head -20
        echo ""
        
        echo "=== Volume Mounts ==="
        docker inspect "${CONTAINER_NAME}" --format '{{range .Mounts}}{{.Type}} {{.Source}} -> {{.Destination}}{{println}}{{end}}'
        echo ""
        
        echo "=== Environment Variables (filtered) ==="
        docker exec "${CONTAINER_NAME}" env | grep -E "^(BIRDNET_|PUID|PGID|TZ|HOME|USER)" | sort || echo "Could not get environment variables"
        echo ""
        
        echo "=== Container Stats ==="
        docker stats "${CONTAINER_NAME}" --no-stream --format "table {{.Container}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}\t{{.BlockIO}}"
        echo ""
        
        echo "=== Container Processes ==="
        docker top "${CONTAINER_NAME}"
        echo ""
        
        echo "=== Audio Device Check ==="
        # Check if audio device is mounted
        if docker exec "${CONTAINER_NAME}" test -d /dev/snd 2>/dev/null; then
            echo "Audio device mounted: YES"
            docker exec "${CONTAINER_NAME}" ls -la /dev/snd/ 2>/dev/null || echo "Could not list audio devices"
        else
            echo "Audio device mounted: NO"
        fi
        echo ""
        
        echo "=== Recent Container Logs (last 100 lines) ==="
        docker logs "${CONTAINER_NAME}" --tail 100 2>&1 | tail -100
        echo ""
        
        echo "=== BirdNET-Go Process Info ==="
        docker exec "${CONTAINER_NAME}" ps aux | grep -E "PID|birdnet" | grep -v grep || echo "Could not get process info"
        echo ""
        
        echo "=== Host System Information ==="
        echo "OS: $(uname -a)"
        echo "Docker Version: $(docker version --format '{{.Server.Version}}')"
        echo ""
        
        echo "=== Host Resources ==="
        free -h 2>/dev/null || echo "free command not available"
        echo ""
        df -h 2>/dev/null | grep -E "Filesystem|/$|/var/lib/docker" || echo "df command not available"
        echo ""
        
    } > "${output_file}"
    print_status "✓ Collected container information → ${output_file}"
}

# Function to collect multiple samples over time
collect_time_series() {
    print_status "Collecting time-series profiles (this will take a few minutes)..."
    
    local samples_dir="${OUTPUT_DIR}/time-series"
    mkdir -p "${samples_dir}"
    
    # Collect 3 heap samples with 30-second intervals
    for i in 1 2 3; do
        print_status "Collecting heap sample ${i}/3..."
        collect_profile "heap" "${samples_dir}/heap-${i}.pprof"
        if [ $i -lt 3 ]; then
            print_status "Waiting 30 seconds before next sample..."
            sleep 30
        fi
    done
    
    print_status "✓ Time-series collection complete"
}

# Function to generate analysis commands
generate_analysis_script() {
    local script_file="${OUTPUT_DIR}/analyze-docker.sh"
    
    cat > "${script_file}" << 'EOF'
#!/bin/bash
# BirdNET-Go Docker Debug Data Analysis Script

set -euo pipefail

echo "=== BirdNET-Go Docker Debug Data Analysis ==="
echo ""

# Check if go tool is available
if ! command -v go &> /dev/null; then
    echo "ERROR: 'go' command not found. Please install Go to analyze profiles."
    echo ""
    echo "To install Go on apt-based Linux (Ubuntu/Debian/Raspberry Pi OS):"
    echo ""
    echo "Option 1: Install from official repository (recommended):"
    echo "  wget https://go.dev/dl/go1.24.4.linux-\$(dpkg --print-architecture).tar.gz"
    echo "  sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.4.linux-\$(dpkg --print-architecture).tar.gz"
    echo "  echo 'export PATH=\$PATH:/usr/local/go/bin' >> ~/.bashrc"
    echo "  source ~/.bashrc"
    echo ""
    echo "Option 2: Install from apt (older version):"
    echo "  sudo apt update && sudo apt install -y golang-go"
    echo ""
    echo "Option 3: Use Docker with Go installed:"
    echo "  docker run --rm -v \$PWD:/data -w /data golang:1.24 bash analyze-docker.sh"
    exit 1
fi

# Analyze heap memory
# Every step below tolerates both a missing and an unreadable profile. The
# collector deletes a profile whose fetch returned an error, so absence is the
# common case, but a kill mid-transfer still leaves a truncated file because that
# cleanup only runs after curl returns. Either one aborts this whole script at
# the step that hits it, under `set -euo pipefail`, skipping every later step.
echo "1. Analyzing heap memory usage..."
if [ -f "heap.pprof" ]; then
    echo "   Top memory consumers:"
    go tool pprof -top -unit=mb heap.pprof | head -20 || echo "   heap.pprof could not be analyzed"
else
    echo "   heap.pprof was not collected, skipping"
fi
echo ""

# Analyze goroutines
echo "2. Analyzing goroutines..."
if [ -f "goroutine.pprof" ]; then
    echo "   Goroutine count by function:"
    go tool pprof -text goroutine.pprof | head -20 || echo "   goroutine.pprof could not be analyzed"
else
    echo "   goroutine.pprof was not collected, skipping"
fi
echo ""

# Analyze CPU profile if it exists
if [ -f "cpu.pprof" ]; then
    echo "3. Analyzing CPU usage..."
    echo "   Top CPU consumers:"
    go tool pprof -top cpu.pprof | head -20 || echo "   cpu.pprof could not be analyzed"
    echo ""
fi

# Check container stats from log
echo "4. Container Resource Usage:"
grep -A5 "Container Stats" container-info.txt || echo "Stats not found"
echo ""

echo "=== Analysis Complete ==="
echo ""
echo "For interactive analysis:"
echo "  go tool pprof -http=:8081 heap.pprof"
echo ""
echo "Or use Docker if Go is not installed locally:"
echo "  docker run --rm -v \$PWD:/data -w /data -p 8081:8081 golang:1.21 go tool pprof -http=:8081 heap.pprof"
EOF
    
    chmod +x "${script_file}"
    print_status "✓ Generated analysis script → ${script_file}"
}

# Function to copy config from container
copy_config_from_container() {
    print_status "Attempting to copy config.yaml from container..."
    
    # From Dockerfile and entrypoint.sh, config is at /config/config.yaml
    # The entrypoint creates a symlink from user home to /config
    local config_path="/config/config.yaml"
    
    if docker exec "${CONTAINER_NAME}" test -f "${config_path}" 2>/dev/null; then
        if docker cp "${CONTAINER_NAME}:${config_path}" "${OUTPUT_DIR}/config.yaml" 2>/dev/null; then
            print_status "✓ Copied config.yaml from ${config_path}"
            # Sanitize sensitive information
            if [ -f "${OUTPUT_DIR}/config.yaml" ]; then
                sed -i 's/password:.*/password: [REDACTED]/g' "${OUTPUT_DIR}/config.yaml" 2>/dev/null || true
                sed -i 's/apikey:.*/apikey: [REDACTED]/g' "${OUTPUT_DIR}/config.yaml" 2>/dev/null || true
                sed -i 's/secret:.*/secret: [REDACTED]/g' "${OUTPUT_DIR}/config.yaml" 2>/dev/null || true
                sed -i 's/token:.*/token: [REDACTED]/g' "${OUTPUT_DIR}/config.yaml" 2>/dev/null || true
                sed -i 's/encryption_key:.*/encryption_key: [REDACTED]/g' "${OUTPUT_DIR}/config.yaml" 2>/dev/null || true
            fi
            return 0
        fi
    fi
    
    print_warning "Could not find config.yaml in container at ${config_path}"
    print_warning "Config directory may be mounted differently"
    return 1
}

# Main execution
main() {
    print_status "Starting BirdNET-Go Docker debug data collection..."
    print_status "Output directory: ${OUTPUT_DIR}"
    
    # Create output directory
    mkdir -p "${OUTPUT_DIR}"
    
    # Check Go installation
    check_go_installation
    
    # Check container
    check_container
    
    # Get container URL
    get_container_url
    
    # Check connectivity and debug mode
    check_connectivity
    check_debug_mode
    
    # Collect container information
    collect_container_info "${OUTPUT_DIR}/container-info.txt"
    
    # Try to copy config
    copy_config_from_container || true
    
    # Collect instant profiles
    print_status "Collecting instant profiles..."
    collect_profile "heap" "${OUTPUT_DIR}/heap.pprof"
    collect_profile "goroutine" "${OUTPUT_DIR}/goroutine.pprof"
    collect_profile "allocs" "${OUTPUT_DIR}/allocs.pprof"
    collect_profile "threadcreate" "${OUTPUT_DIR}/threadcreate.pprof"
    collect_profile "mutex" "${OUTPUT_DIR}/mutex.pprof"
    collect_profile "block" "${OUTPUT_DIR}/block.pprof"
    
    # Collect CPU profile (takes time)
    print_status "Collecting CPU profile (${PROFILE_DURATION} seconds)..."
    print_warning "This will take ${PROFILE_DURATION} seconds. Please ensure BirdNET-Go is under typical load..."
    collect_profile "profile" "${OUTPUT_DIR}/cpu.pprof" "?seconds=${PROFILE_DURATION}"
    
    # Collect execution trace (5 seconds)
    print_status "Collecting execution trace (5 seconds)..."
    collect_profile "trace" "${OUTPUT_DIR}/trace.out" "?seconds=5"
    
    # Collect time-series heap samples
    collect_time_series
    
    # Generate analysis script
    generate_analysis_script
    
    # Create archive
    print_status "Creating archive..."
    ARCHIVE_NAME="birdnet-go-docker-debug-$(date +%Y%m%d-%H%M%S).tar.gz"
    tar -czf "${ARCHIVE_NAME}" "${OUTPUT_DIR}"
    
    # Deliberately no second "is profiling enabled" check here.
    #
    # There used to be one, keyed on `grep -A3 '^diagnostics:' | grep -q
    # "enabled: true"`. It had the two defects the awk above exists to avoid: a
    # fixed 3-line window that only reached `enabled:` by accident of key order,
    # and an unanchored match that any `enabled: true` in the window satisfied.
    # It was also unreachable except as a false alarm, because check_debug_mode
    # exits unless the endpoints answered 200, so by this point profiling IS on
    # and the block could only ever contradict a successful collection. Its
    # remediation was wrong for this deployment too, telling a Docker user to
    # `systemctl restart birdnet-go`.
    
    # Final summary
    echo ""
    print_status "=========================================="
    print_status "Docker debug data collection complete!"
    print_status "=========================================="
    echo ""
    echo "Container: ${CONTAINER_NAME}"
    echo "Image: ${BIRDNET_GO_IMAGE}"
    echo "Files collected in: ${OUTPUT_DIR}/"
    echo "Archive created: ${ARCHIVE_NAME}"
    echo ""
    echo "To analyze the data:"
    echo "  cd ${OUTPUT_DIR}"
    echo "  ./analyze-docker.sh"
    echo ""
    echo "If you don't have Go installed, use Docker:"
    echo "  cd ${OUTPUT_DIR}"
    echo "  docker run --rm -v \$PWD:/data -w /data golang:1.24 bash analyze-docker.sh"
    echo ""
    echo "For interactive web UI (requires Go or Docker):"
    echo "  go tool pprof -http=:8081 ${OUTPUT_DIR}/heap.pprof"
    echo ""
    
    # Tip for systemd logs
    echo "To view systemd service logs:"
    echo "  sudo journalctl -u birdnet-go -n 100"
    echo ""
}

# Run main function
main