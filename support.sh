#!/bin/bash

# BirdNET-Go Support Data Collection Script
# This script collects diagnostic information to help troubleshoot BirdNET-Go issues
# It masks sensitive information such as passwords, tokens, and IP addresses

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored messages
print_message() {
    if [ "$3" = "nonewline" ]; then
        echo -en "${2}${1}${NC}"
    else
        echo -e "${2}${1}${NC}"
    fi
}

# Print banner
cat << "EOF"
 ____  _         _ _   _ _____ _____    ____      
| __ )(_)_ __ __| | \ | | ____|_   _|  / ___| ___ 
|  _ \| | '__/ _` |  \| |  _|   | |   | |  _ / _ \
| |_) | | | | (_| | |\  | |___  | |   | |_| | (_) |
|____/|_|_|  \__,_|_| \_|_____| |_|    \____|\___/ 

Support Data Collection Tool

EOF

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    print_message "⚠️  This script needs to be run as root to collect all necessary information." "$YELLOW"
    print_message "Please run with sudo: sudo bash $0" "$YELLOW"
    exit 1
fi

# Timestamp for file naming
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
OUTPUT_DIR="/tmp/birdnet-go-support-$TIMESTAMP"
OUTPUT_FILE="/tmp/birdnet-go-support-$TIMESTAMP.tar.gz"

# Create output directory
mkdir -p "$OUTPUT_DIR"
print_message "📁 Created temporary directory for diagnostic data: $OUTPUT_DIR" "$GREEN"

# Function to mask sensitive information in a file
mask_sensitive_data() {
    local input_file="$1"
    local output_file="$2"
    
    # Make a copy of the input file
    cp "$input_file" "$output_file"
    
    # Mask passwords, tokens, keys, etc.
    sed -i 's/\(password: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(Password: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(apikey: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(APIKey: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(token: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(Token: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(secret: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(Secret: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(ClientSecret: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(clientsecret: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    sed -i 's/\(sessionsecret: \)"\([^"]*\)"/\1"[REDACTED]"/g' "$output_file"
    
    # Mask IP addresses
    sed -i 's/\([0-9]\{1,3\}\.\)[0-9]\{1,3\}\(\.[0-9]\{1,3\}\)\(\.[0-9]\{1,3\}\)/\1xxx\2\3/g' "$output_file"
}

# Function to collect system information
collect_system_info() {
    print_message "🖥️  Collecting system information..." "$BLUE"
    
    # Create system info directory
    mkdir -p "$OUTPUT_DIR/system"
    
    # Basic system info
    uname -a > "$OUTPUT_DIR/system/uname.txt"
    cat /etc/os-release > "$OUTPUT_DIR/system/os-release.txt"
    lscpu > "$OUTPUT_DIR/system/cpu-info.txt"
    free -h > "$OUTPUT_DIR/system/memory.txt"
    df -h > "$OUTPUT_DIR/system/disk-space.txt"
    
    # Check if lshw is installed
    if command -v lshw &> /dev/null; then
        lshw -short > "$OUTPUT_DIR/system/hardware.txt" 2>/dev/null
    else
        echo "lshw not installed" > "$OUTPUT_DIR/system/hardware.txt"
    fi
    
    # Network info (masked)
    ip a | sed 's/inet [0-9]\{1,3\}\.[0-9]\{1,3\}\.[0-9]\{1,3\}\.[0-9]\{1,3\}/inet xxx.xxx.xxx.xxx/g' > "$OUTPUT_DIR/system/network.txt"
    
    # Kernel modules related to sound
    lsmod | grep -E 'snd|sound' > "$OUTPUT_DIR/system/sound-modules.txt"
    
    # Environment variables (masked)
    env | grep -v -E 'PASSWORD|TOKEN|KEY|SECRET' > "$OUTPUT_DIR/system/environment.txt"
}

# Function to collect Docker information
collect_docker_info() {
    print_message "🐳 Collecting Docker information..." "$BLUE"
    
    # Create docker info directory
    mkdir -p "$OUTPUT_DIR/docker"
    
    # Check if Docker is installed
    if ! command -v docker &> /dev/null; then
        echo "Docker is not installed" > "$OUTPUT_DIR/docker/not-installed.txt"
        print_message "❌ Docker not found" "$RED"
        return
    fi
    
    # Docker version
    docker version > "$OUTPUT_DIR/docker/version.txt" 2>&1
    docker info > "$OUTPUT_DIR/docker/info.txt" 2>&1
    
    # Docker images
    docker images | grep -E 'birdnet-go|ghcr.io/tphakala/birdnet-go' > "$OUTPUT_DIR/docker/images.txt" 2>&1
    
    # Docker containers
    docker ps -a > "$OUTPUT_DIR/docker/containers-all.txt" 2>&1
    docker ps -a | grep -E 'birdnet-go|ghcr.io/tphakala/birdnet-go' > "$OUTPUT_DIR/docker/containers-birdnet.txt" 2>&1
    
    # Get container ID
    CONTAINER_ID=$(docker ps -a | grep -E 'birdnet-go|ghcr.io/tphakala/birdnet-go' | awk '{print $1}' | head -n 1)
    
    if [ -n "$CONTAINER_ID" ]; then
        # Container logs
        docker logs "$CONTAINER_ID" 2>&1 | grep -v -E 'password|token|key|secret' > "$OUTPUT_DIR/docker/container-logs.txt"
        
        # Container details
        docker inspect "$CONTAINER_ID" > "$OUTPUT_DIR/docker/container-inspect.txt" 2>&1
        
        # Get container creation command
        docker inspect --format='{{.Config.Cmd}}' "$CONTAINER_ID" > "$OUTPUT_DIR/docker/container-cmd.txt" 2>&1
        docker inspect --format='{{.HostConfig.Devices}}' "$CONTAINER_ID" > "$OUTPUT_DIR/docker/container-devices.txt" 2>&1
    else
        echo "No BirdNET-Go container found" > "$OUTPUT_DIR/docker/no-container.txt"
        print_message "⚠️  No BirdNET-Go container found" "$YELLOW"
    fi
}

# Function to collect BirdNET-Go configuration
collect_birdnet_config() {
    print_message "📄 Collecting BirdNET-Go configuration..." "$BLUE"
    
    # Create config directory
    mkdir -p "$OUTPUT_DIR/config"
    
    # Common config paths
    CONFIG_PATHS=(
        "/home/*/birdnet-go-app/config/config.yaml"
        "/root/.config/birdnet-go/config.yaml"
        "/config/config.yaml"  # Docker volume common path
    )
    
    # Find config files
    CONFIG_FOUND=false
    for CONFIG_PATH in "${CONFIG_PATHS[@]}"; do
        for CONFIG_FILE in $CONFIG_PATH; do
            if [ -f "$CONFIG_FILE" ]; then
                mask_sensitive_data "$CONFIG_FILE" "$OUTPUT_DIR/config/config.yaml"
                echo "Found config at: $CONFIG_FILE" >> "$OUTPUT_DIR/config/path.txt"
                CONFIG_FOUND=true
            fi
        done
    done
    
    # Try to find any birdnet-go config files if not found in common locations
    if [ "$CONFIG_FOUND" = false ]; then
        find / -name "config.yaml" -path "*birdnet-go*" -o -path "*/.config/birdnet-go/*" 2>/dev/null | while read -r file; do
            mask_sensitive_data "$file" "$OUTPUT_DIR/config/$(basename "$(dirname "$file")")_config.yaml"
            echo "Found config at: $file" >> "$OUTPUT_DIR/config/path.txt"
            CONFIG_FOUND=true
        done
    fi
    
    if [ "$CONFIG_FOUND" = false ]; then
        echo "No BirdNET-Go configuration files found" > "$OUTPUT_DIR/config/not-found.txt"
        print_message "⚠️  No BirdNET-Go configuration files found" "$YELLOW"
    fi
}

# Function to collect systemd service information
collect_systemd_info() {
    print_message "🔄 Collecting systemd service information..." "$BLUE"
    
    # Create systemd directory
    mkdir -p "$OUTPUT_DIR/systemd"
    
    # Check for BirdNET-Go service
    if systemctl list-unit-files | grep -q birdnet-go.service; then
        # Service status
        systemctl status birdnet-go.service > "$OUTPUT_DIR/systemd/status.txt" 2>&1
        
        # Service logs
        journalctl -u birdnet-go.service --no-pager -n 500 > "$OUTPUT_DIR/systemd/service-logs.txt" 2>&1
        
        # Service configuration
        cp /etc/systemd/system/birdnet-go.service "$OUTPUT_DIR/systemd/service-file.txt" 2>/dev/null
    else
        echo "BirdNET-Go systemd service not found" > "$OUTPUT_DIR/systemd/not-installed.txt"
        print_message "⚠️  BirdNET-Go systemd service not found" "$YELLOW"
    fi
    
    # Check Docker service
    if systemctl list-unit-files | grep -q docker.service; then
        systemctl status docker.service > "$OUTPUT_DIR/systemd/docker-status.txt" 2>&1
        journalctl -u docker.service --no-pager -n 100 > "$OUTPUT_DIR/systemd/docker-logs.txt" 2>&1
    fi
}

# Function to collect audio device information
collect_audio_info() {
    print_message "🎤 Collecting audio device information..." "$BLUE"
    
    # Create audio directory
    mkdir -p "$OUTPUT_DIR/audio"
    
    # Check if arecord is installed
    if command -v arecord &> /dev/null; then
        # Force English locale for consistent output
        LC_ALL=C arecord -l > "$OUTPUT_DIR/audio/devices.txt" 2>&1
        LC_ALL=C arecord -L > "$OUTPUT_DIR/audio/device-list.txt" 2>&1
    else
        echo "arecord not installed" > "$OUTPUT_DIR/audio/not-installed.txt"
    fi
    
    # Get audio groups and users
    getent group audio > "$OUTPUT_DIR/audio/audio-group.txt" 2>&1
    
    # ALSA config
    if [ -f /etc/asound.conf ]; then
        cp /etc/asound.conf "$OUTPUT_DIR/audio/asound.conf" 2>/dev/null
    fi
    
    # PulseAudio/PipeWire info if available
    if command -v pactl &> /dev/null; then
        pactl info > "$OUTPUT_DIR/audio/pulseaudio-info.txt" 2>&1
        pactl list sources > "$OUTPUT_DIR/audio/pulseaudio-sources.txt" 2>&1
    fi
    
    # Check access to audio devices
    ls -la /dev/snd/ > "$OUTPUT_DIR/audio/snd-devices.txt" 2>&1
}

# Function to collect install script information
collect_install_info() {
    print_message "📦 Collecting installation information..." "$BLUE"
    
    # Create install directory
    mkdir -p "$OUTPUT_DIR/install"
    
    # Look for install.sh in common locations
    INSTALL_PATHS=(
        "/home/*/install.sh"
        "/root/install.sh"
        "/tmp/install.sh"
    )
    
    INSTALL_FOUND=false
    for INSTALL_PATH in "${INSTALL_PATHS[@]}"; do
        for INSTALL_FILE in $INSTALL_PATH; do
            if [ -f "$INSTALL_FILE" ]; then
                cp "$INSTALL_FILE" "$OUTPUT_DIR/install/install.sh" 2>/dev/null
                echo "Found install.sh at: $INSTALL_FILE" >> "$OUTPUT_DIR/install/path.txt"
                INSTALL_FOUND=true
            fi
        done
    done
    
    # Check when install.sh was run
    if [ -f /var/log/auth.log ]; then
        grep -E "sudo.*install.sh|bash.*install.sh" /var/log/auth.log > "$OUTPUT_DIR/install/auth-log-entries.txt" 2>/dev/null
    fi
    
    if [ "$INSTALL_FOUND" = false ]; then
        echo "No install.sh file found" > "$OUTPUT_DIR/install/not-found.txt"
        print_message "⚠️  No install.sh file found" "$YELLOW"
    fi
}

# Function to bundle everything into a tarball
create_support_bundle() {
    print_message "📦 Creating support bundle..." "$BLUE"
    
    # Add a README file
    cat > "$OUTPUT_DIR/README.txt" << EOF
BirdNET-Go Support Data
=======================
Collected on: $(date)
Hostname: $(hostname)
System: $(cat /etc/os-release | grep PRETTY_NAME | cut -d'"' -f2)

This archive contains diagnostic information for troubleshooting BirdNET-Go issues.
Sensitive information such as passwords, tokens, and IP addresses has been masked.

Contents:
- system/: System information (OS, CPU, memory, etc.)
- docker/: Docker configuration and logs
- config/: BirdNET-Go configuration files
- systemd/: Systemd service information
- audio/: Audio device information
- install/: Installation information

Please share this file with the BirdNET-Go developers.
EOF
    
    # Create tarball
    tar -czf "$OUTPUT_FILE" -C "$(dirname "$OUTPUT_DIR")" "$(basename "$OUTPUT_DIR")"
    
    # Set permissions so normal user can access it
    chmod 644 "$OUTPUT_FILE"
    
    # Clean up
    rm -rf "$OUTPUT_DIR"
    
    print_message "✅ Support bundle created: $OUTPUT_FILE" "$GREEN"
    print_message "📋 Please share this file with the BirdNET-Go developers." "$GREEN"
}

# Main execution
print_message "🚀 Starting data collection process..." "$GREEN"

collect_system_info
collect_docker_info
collect_birdnet_config
collect_systemd_info
collect_audio_info
collect_install_info
create_support_bundle

print_message "\n✅ Data collection complete!" "$GREEN"
print_message "📦 Support bundle: $OUTPUT_FILE" "$BLUE"
print_message "🙏 Thank you for helping improve BirdNET-Go!" "$GREEN" 