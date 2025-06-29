#!/bin/bash
# BirdNET-Go Debug Data Collection Script
# This script collects comprehensive profiling and debug information
# for performance analysis

set -euo pipefail

# Configuration
BIRDNET_HOST="${BIRDNET_HOST:-localhost}"
BIRDNET_PORT="${BIRDNET_PORT:-8080}"
BASE_URL="http://${BIRDNET_HOST}:${BIRDNET_PORT}"
OUTPUT_DIR="debug-data-$(date +%Y%m%d-%H%M%S)"
PROFILE_DURATION="${PROFILE_DURATION:-30}"  # CPU profile duration in seconds

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
    
    print_warning "Go is not installed. Go is required for analyzing profiling data."
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
        echo "After installation, run this script again."
        echo ""
        echo "Note: You can still collect debug data now, but you'll need Go to analyze it."
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

# Function to check if BirdNET-Go is accessible
check_connectivity() {
    print_status "Checking connectivity to BirdNET-Go at ${BASE_URL}..."
    if ! curl -s -f "${BASE_URL}" > /dev/null 2>&1; then
        print_error "Cannot connect to BirdNET-Go at ${BASE_URL}"
        print_error "Please ensure BirdNET-Go is running and accessible"
        exit 1
    fi
    print_status "✓ Connected to BirdNET-Go"
}

# Function to check if debug mode is enabled
check_debug_mode() {
    print_status "Checking if debug mode is enabled..."
    if ! curl -s -f "${BASE_URL}/debug/pprof/" > /dev/null 2>&1; then
        print_error "Debug mode is not enabled or pprof endpoints are not accessible"
        print_error "Please set 'debug: true' in your config.yaml and restart BirdNET-Go"
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

# Function to collect system information
collect_system_info() {
    local output_file=$1
    
    print_status "Collecting system information..."
    {
        echo "=== System Information ==="
        echo "Date: $(date)"
        echo "Hostname: $(hostname)"
        echo ""
        
        echo "=== OS Information ==="
        uname -a
        echo ""
        
        if [ -f /etc/os-release ]; then
            echo "=== Distribution ==="
            cat /etc/os-release
            echo ""
        fi
        
        echo "=== CPU Information ==="
        if command -v lscpu &> /dev/null; then
            lscpu
        else
            cat /proc/cpuinfo | grep -E "processor|model name|cpu cores" | head -20
        fi
        echo ""
        
        echo "=== Memory Information ==="
        free -h
        echo ""
        
        echo "=== Disk Usage ==="
        df -h
        echo ""
        
        echo "=== Process Information ==="
        if pgrep -x "birdnet-go" > /dev/null; then
            ps aux | grep -E "PID|birdnet-go" | grep -v grep
        else
            echo "BirdNET-Go process not found with 'birdnet-go' name"
        fi
        echo ""
        
        echo "=== Network Connections ==="
        if command -v ss &> /dev/null; then
            ss -tlnp 2>/dev/null | grep -E "State|:${BIRDNET_PORT}" || true
        else
            netstat -tlnp 2>/dev/null | grep -E "Proto|:${BIRDNET_PORT}" || true
        fi
        echo ""
        
    } > "${output_file}"
    print_status "✓ Collected system information → ${output_file}"
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
    local script_file="${OUTPUT_DIR}/analyze.sh"
    
    cat > "${script_file}" << 'EOF'
#!/bin/bash
# BirdNET-Go Debug Data Analysis Script
# Run this script to analyze the collected debug data

set -euo pipefail

echo "=== BirdNET-Go Debug Data Analysis ==="
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
    echo "  docker run --rm -v \$PWD:/data -w /data golang:1.24 bash analyze.sh"
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

# Analyze mutex contention
if [ -f "mutex.pprof" ]; then
    echo "4. Analyzing mutex contention..."
    go tool pprof -top mutex.pprof | head -10
    echo ""
fi

# Analyze memory growth over time
if [ -d "time-series" ]; then
    echo "5. Analyzing memory growth..."
    echo "   Comparing heap profiles:"
    if [ -f "time-series/heap-1.pprof" ] && [ -f "time-series/heap-3.pprof" ]; then
        echo "   Memory growth between first and last sample:"
        go tool pprof -top -unit=mb -base=time-series/heap-1.pprof time-series/heap-3.pprof | head -10
    fi
    echo ""
fi

echo "=== Analysis Complete ==="
echo ""
echo "For interactive analysis, run:"
echo "  go tool pprof heap.pprof"
echo "  go tool pprof cpu.pprof"
echo "  go tool pprof -http=:8081 heap.pprof  # Opens web UI"
echo ""
echo "To generate flame graphs:"
echo "  go tool pprof -http=:8081 cpu.pprof"
echo ""
EOF
    
    chmod +x "${script_file}"
    print_status "✓ Generated analysis script → ${script_file}"
}

# Main execution
main() {
    print_status "Starting BirdNET-Go debug data collection..."
    print_status "Output directory: ${OUTPUT_DIR}"
    
    # Create output directory
    mkdir -p "${OUTPUT_DIR}"
    
    # Check Go installation
    check_go_installation
    
    # Check connectivity and debug mode
    check_connectivity
    check_debug_mode
    
    # Collect system information
    collect_system_info "${OUTPUT_DIR}/system-info.txt"
    
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
    ARCHIVE_NAME="birdnet-go-debug-$(date +%Y%m%d-%H%M%S).tar.gz"
    tar -czf "${ARCHIVE_NAME}" "${OUTPUT_DIR}"
    
    # Final summary
    echo ""
    print_status "=========================================="
    print_status "Debug data collection complete!"
    print_status "=========================================="
    echo ""
    echo "Files collected in: ${OUTPUT_DIR}/"
    echo "Archive created: ${ARCHIVE_NAME}"
    echo ""
    echo "To analyze the data locally:"
    echo "  cd ${OUTPUT_DIR}"
    echo "  ./analyze.sh"
    echo ""
    echo "To share for analysis:"
    echo "  Upload ${ARCHIVE_NAME} to a file sharing service"
    echo ""
    echo "For real-time analysis:"
    echo "  go tool pprof -http=:8081 ${OUTPUT_DIR}/heap.pprof"
    echo ""
}

# Run main function
main