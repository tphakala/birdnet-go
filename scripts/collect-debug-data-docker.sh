#!/bin/bash
# BirdNET-Go Debug Data Collection Script for Docker Deployments
# This script collects debug data from BirdNET-Go running in a Docker container

set -euo pipefail

# Configuration
CONTAINER_NAME="${BIRDNET_CONTAINER:-birdnet-go}"
BIRDNET_PORT="${BIRDNET_PORT:-8080}"
OUTPUT_DIR="debug-data-docker-$(date +%Y%m%d-%H%M%S)"
PROFILE_DURATION="${PROFILE_DURATION:-30}"
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
print_status() {
    echo -e "${GREEN}[$(date +'%H:%M:%S')]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[$(date +'%H:%M:%S')] WARNING:${NC} $1"
}

print_error() {
    echo -e "${RED}[$(date +'%H:%M:%S')] ERROR:${NC} $1"
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

# Function to check if BirdNET-Go is accessible
check_connectivity() {
    print_status "Checking connectivity to BirdNET-Go..."
    if ! curl -s -f "${BASE_URL}" > /dev/null 2>&1; then
        print_error "Cannot connect to BirdNET-Go at ${BASE_URL}"
        print_error "Please ensure BirdNET-Go is running in the container"
        exit 1
    fi
    print_status "✓ Connected to BirdNET-Go"
}

# Function to check if debug mode is enabled
check_debug_mode() {
    print_status "Checking if debug mode is enabled..."
    if ! curl -s -f "${BASE_URL}/debug/pprof/" > /dev/null 2>&1; then
        print_error "Debug mode is not enabled or pprof endpoints are not accessible"
        print_error "Please set 'debug: true' in your config.yaml and restart the container"
        print_warning "You may need to restart the container with debug mode enabled:"
        print_warning "  1. Update config.yaml with 'debug: true'"
        print_warning "  2. docker restart ${CONTAINER_NAME}"
        exit 1
    fi
    print_status "✓ Debug mode is enabled"
}

# Function to collect a profile
collect_profile() {
    local profile_type=$1
    local output_file=$2
    local url_params=${3:-""}
    
    print_status "Collecting ${profile_type} profile..."
    if curl -s -f -o "${output_file}" "${BASE_URL}/debug/pprof/${profile_type}${url_params}"; then
        print_status "✓ Collected ${profile_type} profile → ${output_file}"
    else
        print_warning "Failed to collect ${profile_type} profile"
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
echo "1. Analyzing heap memory usage..."
echo "   Top memory consumers:"
go tool pprof -top -unit=mb heap.pprof | head -20
echo ""

# Analyze goroutines
echo "2. Analyzing goroutines..."
echo "   Goroutine count by function:"
go tool pprof -text goroutine.pprof | head -20
echo ""

# Analyze CPU profile if it exists
if [ -f "cpu.pprof" ]; then
    echo "3. Analyzing CPU usage..."
    echo "   Top CPU consumers:"
    go tool pprof -top cpu.pprof | head -20
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
    
    # Check if debug mode is actually enabled in config
    if [ -f "${OUTPUT_DIR}/config.yaml" ]; then
        if ! grep -q "^debug: true" "${OUTPUT_DIR}/config.yaml" 2>/dev/null; then
            print_warning ""
            print_warning "⚠️  IMPORTANT: Debug mode may not be properly enabled!"
            print_warning "Found 'debug: false' in config.yaml"
            print_warning "To enable debug mode:"
            print_warning "1. Edit your config.yaml and set 'debug: true'"
            print_warning "2. Restart the container: sudo systemctl restart birdnet-go"
            print_warning "3. Run this script again"
        fi
    fi
    
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