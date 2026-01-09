#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
GRAY='\033[0;90m'
NC='\033[0m' # No Color


# ASCII Art Banner
cat << "EOF"
 ____  _         _ _   _ _____ _____    ____      
| __ )(_)_ __ __| | \ | | ____|_   _|  / ___| ___ 
|  _ \| | '__/ _` |  \| |  _|   | |   | |  _ / _ \
| |_) | | | | (_| | |\  | |___  | |   | |_| | (_) |
|____/|_|_|  \__,_|_| \_|_____|_|    \____\___/ 

🐳 → 📦 PODMAN EDITION
EOF


# Default version (will be set by parse_arguments function)
BIRDNET_GO_VERSION="podman-nightly"
BIRDNET_GO_IMAGE=""

# Logging configuration
LOG_DIR="$HOME/birdnet-go-app/data/logs"
# Generate timestamped log file name: podman-install-YYYYMMDD-HHMMSS.log
LOG_TIMESTAMP=$(date '+%Y%m%d-%H%M%S')
LOG_FILE="$LOG_DIR/podman-install-${LOG_TIMESTAMP}.log"

# Logging system will be initialized after function definitions

# Version management configuration
MAX_CONFIG_BACKUPS=10
VERSION_HISTORY_FILE="$LOG_DIR/podman_version_history.log"
CONFIG_BACKUP_PREFIX="config-backup-"

# Set secure umask for file creation
umask 077

# Cleanup trap for temporary files
cleanup_temp_files() {
    rm -f /tmp/version_history_*.tmp 2>/dev/null
    rm -f "$LOG_DIR/.last_backup_time" 2>/dev/null
    rm -f "$VERSION_HISTORY_FILE.lock" 2>/dev/null
}
trap cleanup_temp_files EXIT INT TERM

# Function to validate version history entry format
validate_version_history_entry() {
    local line="$1"
    # Format: timestamp|image_hash|config_backup|image_tag|context
    # Example: 20240826-134817|sha256:abc123...|config-backup-20240826-134817.yaml|ghcr.io/tphakala/birdnet-go:podman-nightly|pre-update
    if [[ "$line" =~ ^[0-9]{8}-[0-9]{6}\|[^|]+\|[^|]*\|[^|]+\|[^|]+$ ]]; then
        return 0
    else
        log_message "WARN" "Invalid version history entry format: $line"
        return 1
    fi
}

# Atomic append to version history file with locking
append_version_history() {
    local entry="$1"
    
    if [ -z "$entry" ]; then
        log_message "ERROR" "Cannot append empty entry to version history"
        return 1
    fi
    
    # Validate entry format before writing
    if ! validate_version_history_entry "$entry"; then
        log_message "ERROR" "Invalid version history entry format, refusing to append: $entry"
        return 2
    fi
    
    # Ensure version history file exists with secure permissions
    if [ ! -f "$VERSION_HISTORY_FILE" ]; then
        touch "$VERSION_HISTORY_FILE"
        chmod 600 "$VERSION_HISTORY_FILE" 2>/dev/null
        log_message "INFO" "Created version history file with secure permissions"
    fi
    
    # Use flock for atomic append operation
    (
        flock -x 200
        echo "$entry" >> "$VERSION_HISTORY_FILE"
    ) 200>"$VERSION_HISTORY_FILE.lock"
    
    local result=$?
    if [ $result -eq 0 ]; then
        log_message "INFO" "Version history entry appended atomically"
    else
        log_message "ERROR" "Failed to append to version history (exit code: $result)"
    fi
    return $result
}

# Function to setup logging directory
setup_logging() {
    # Create logs directory if it doesn't exist
    if [ ! -d "$LOG_DIR" ]; then
        mkdir -p "$LOG_DIR" 2>/dev/null || {
            # If user directory creation fails, try to create it with proper permissions
            mkdir -p "$(dirname "$LOG_DIR")" 2>/dev/null
            mkdir -p "$LOG_DIR" 2>/dev/null
        }
    fi
    
    # Test if we can write to the timestamped log file
    if [ -d "$LOG_DIR" ] && touch "$LOG_FILE" 2>/dev/null; then
        # Log file is accessible, initialize with session start
        log_message "INFO" "=== BirdNET-Go Podman Installation/Update Session Started ==="
        log_message "INFO" "Log file: $(basename "$LOG_FILE")"
        log_message "INFO" "Script version: $(grep -o 'script_version.*[0-9]\+\.[0-9]\+\.[0-9]\+' "$0" | head -1 || echo 'podman-1.0.0')"
        log_message "INFO" "User: $USER (UID: $(id -u)), Working directory: $(pwd)"
        log_message "INFO" "System: $(uname -a)"
        
        # Log initial system state
        log_system_resources "initial"
        # Network state logging will be done during network check
        
        return 0
    else
        # Cannot write to log file, disable logging
        LOG_FILE=""
        return 1
    fi
}

# Redact credentials and obvious secrets from log lines
sanitize_for_logs() {
    # Redact URL basic-auth creds: scheme://user:pass@host -> scheme://***:***@host
    # Also redact common secret patterns like password: value
    sed -E 's#(://)[^/@:]+(:[^/@]*)?@#\1***:***@#g' \
    | sed -E 's#(password|passwd|pwd|token|secret|api[_-]?key)["'\'']?\s*[:=]\s*[^"'\''\s]+#\1: ***#Ig'
}

# Function to log messages with timestamps
log_message() {
    local level="$1"
    local message="$2"
    
    # Only log if LOG_FILE is set and accessible
    if [ -n "$LOG_FILE" ] && [ -w "$LOG_FILE" ]; then
        # Create timestamp in UTC ISO 8601 format with RFC3339 compliance
        local timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
        # Sanitize the message before logging
        local sanitized_message
        sanitized_message=$(echo "$message" | sanitize_for_logs)
        # Append to log file
        echo "[$timestamp] [$level] $sanitized_message" >> "$LOG_FILE"
    fi
}

# Function to log command execution results
log_command_result() {
    local command="$1"
    local exit_code="$2"
    local context="$3"
    
    if [ "$exit_code" -eq 0 ]; then
        log_message "INFO" "Command succeeded: $command${context:+ ($context)}"
    else
        log_message "ERROR" "Command failed (exit $exit_code): $command${context:+ ($context)}"
    fi
}

# Enhanced print_message function that also logs
print_message() {
    # Check if $3 exists, otherwise set to empty string
    local nonewline=${3:-""}
    local message="$1"
    local color="$2"
    
    if [ "$nonewline" = "nonewline" ]; then
        echo -en "${color}${message}${NC}"
    else
        echo -e "${color}${message}${NC}"
    fi
    
    # Strip ANSI and sanitize before logging
    local log_line
    log_line="$(echo "$message" | sed 's/\x1b\[[0-9;]*m//g' | sanitize_for_logs)"
    
    # Log the message with appropriate level
    if [[ "$message" == *"❌"* ]] || [[ "$message" == *"ERROR"* ]] || [[ "$message" == *"Failed"* ]] || [[ "$message" == *"failed"* ]]; then
        log_message "ERROR" "$log_line"
    elif [[ "$message" == *"⚠️"* ]] || [[ "$message" == *"WARNING"* ]] || [[ "$message" == *"Warning"* ]]; then
        log_message "WARN" "$log_line"
    elif [[ "$message" == *"✅"* ]] || [[ "$message" == *"Success"* ]]; then
        log_message "INFO" "$log_line"
    else
        log_message "INFO" "$log_line"
    fi
}

# Function to log system resources (disk, memory)
log_system_resources() {
    local context="${1:-general}"
    
    log_message "INFO" "=== System Resources Check ($context) ==="
    
    # Disk space for key directories
    local config_dir_space=""
    local data_dir_space=""
    local podman_space=""
    local tmp_space=""
    
    if [ -d "$CONFIG_DIR" ] || [ -d "$(dirname "$CONFIG_DIR")" ]; then
        config_dir_space=$(df -h "$(dirname "$CONFIG_DIR")" 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
        log_message "INFO" "Config directory disk space: $config_dir_space"
    fi
    
    if [ -d "$DATA_DIR" ] || [ -d "$(dirname "$DATA_DIR")" ]; then
        data_dir_space=$(df -h "$(dirname "$DATA_DIR")" 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
        log_message "INFO" "Data directory disk space: $data_dir_space"
    fi
    
    # Check Podman storage directory instead of Docker
    if [ -d "$HOME/.local/share/containers" ]; then
        podman_space=$(df -h "$HOME/.local/share/containers" 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
        log_message "INFO" "Podman storage disk space: $podman_space"
    fi
    
    tmp_space=$(df -h /tmp 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
    log_message "INFO" "Temp directory disk space: $tmp_space"
    
    # Memory information
    if [ -f /proc/meminfo ]; then
        local mem_total=$(grep MemTotal /proc/meminfo | awk '{printf "%.1f GB", $2/1024/1024}')
        local mem_available=$(grep MemAvailable /proc/meminfo | awk '{printf "%.1f GB", $2/1024/1024}' 2>/dev/null || echo "unknown")
        log_message "INFO" "Memory: Total $mem_total, Available $mem_available"
    fi
    
    # Load average
    if [ -f /proc/loadavg ]; then
        local load_avg=$(cat /proc/loadavg | cut -d' ' -f1-3)
        log_message "INFO" "Load average: $load_avg"
    fi
}

# Function to get IP address
get_ip_address() {
    # Get primary IP address, excluding podman and localhost interfaces
    local ip=""
    
    # Method 1: Try using ip command with POSIX-compatible regex
    if command_exists ip; then
        ip=$(ip -4 addr show scope global \
          | grep -vE 'podman|cni|docker|br-|veth' \
          | grep -oE 'inet ([0-9]+\.){3}[0-9]+' \
          | awk '{print $2}' \
          | head -n1)
    fi
    
    # Method 2: Try hostname command for fallback if ip command didn't work
    if [ -z "$ip" ] && command_exists hostname; then
        ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    
    # Method 3: Try ifconfig as last resort
    if [ -z "$ip" ] && command_exists ifconfig; then
        ip=$(ifconfig | grep -Eo 'inet (addr:)?([0-9]*\.){3}[0-9]*' | grep -v '127.0.0.1' | head -n1 | awk '{print $2}' | sed 's/addr://')
    fi
    
    # Return the IP address or empty string
    echo "$ip"
}

# Function to check if mDNS is available
check_mdns() {
    # First check if avahi-daemon is installed
    if ! command_exists avahi-daemon && ! command_exists systemctl; then
        return 1
    fi

    # Then check if it's running
    if command_exists systemctl && systemctl is-active --quiet avahi-daemon; then
        hostname -f | grep -q ".local"
        return $?
    fi
    return 1
}

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to check if curl is available and install it if needed
ensure_curl() {
    if ! command_exists curl; then
        print_message "📦 curl not found. Installing curl..." "$YELLOW"
        if sudo apt -qq update && sudo apt install -qq -y curl; then
            print_message "✅ curl installed successfully" "$GREEN"
        else
            print_message "❌ Failed to install curl" "$RED"
            print_message "Please install curl manually and try again" "$YELLOW"
            exit 1
        fi
    fi
}

# Function to check network connectivity
check_network() {
    print_message "🌐 Checking network connectivity..." "$YELLOW"
    local success=true

    # First do a basic ping test to check general connectivity
    if ! ping -c 1 8.8.8.8 >/dev/null 2>&1; then
        send_telemetry_event "error" "Network connectivity failed" "error" "step=network_check,error=ping_failed"
        print_message "❌ No network connectivity (ping test failed)" "$RED"
        print_message "Please check your internet connection and try again" "$YELLOW"
        exit 1
    fi

    # Now ensure curl is available for further tests
    ensure_curl
     
    # HTTP/HTTPS Check
    print_message "\n📡 Testing HTTP/HTTPS connectivity..." "$YELLOW"
    local urls=(
        "https://github.com"
        "https://raw.githubusercontent.com"
        "https://ghcr.io"
    )
    
    for url in "${urls[@]}"; do
        local http_code
        http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "$url")
        if [ "$http_code" -ge 200 ] && [ "$http_code" -lt 400 ]; then
            print_message "✅ HTTPS connection successful to $url (HTTP $http_code)" "$GREEN"
        else
            print_message "❌ HTTPS connection failed to $url (HTTP $http_code)" "$RED"
            success=false
        fi
    done

    # Container Registry Check
    print_message "\n📡 Testing GitHub registry connectivity..." "$YELLOW"
    if curl -s "https://ghcr.io/v2/" >/dev/null 2>&1; then
        print_message "✅ GitHub registry is accessible" "$GREEN"
    else
        print_message "❌ Cannot access container registry" "$RED"
        success=false
    fi

    if [ "$success" = false ]; then
        print_message "\n❌ Network connectivity check failed" "$RED"
        print_message "Please check:" "$YELLOW"
        print_message "  • Internet connection" "$YELLOW"
        print_message "  • DNS settings (/etc/resolv.conf)" "$YELLOW"
        print_message "  • Firewall rules" "$YELLOW"
        print_message "  • Proxy settings (if applicable)" "$YELLOW"
        return 1
    fi

    print_message "\n✅ Network connectivity check passed\n" "$GREEN"
    return 0
}

# Function to check port availability
check_port_availability() {
    local port="$1"
    
    # Check if port is numeric and within valid range
    if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
        log_message "ERROR" "Invalid port number: $port"
        return 1
    fi
    
    # Use ss if available (preferred)
    if command_exists ss; then
        ! ss -tuln | awk '{print $5}' | grep -E ":${port}$" >/dev/null 2>&1
        return $?
    fi
    
    # Fallback to netstat
    if command_exists netstat; then
        ! netstat -tuln 2>/dev/null | awk '{print $4}' | grep -E ":${port}$" >/dev/null
        return $?
    fi
    
    # Ultimate fallback - try to bind to the port
    if command_exists nc; then
        ! timeout 1 nc -l -p "$port" </dev/null >/dev/null 2>&1
        return $?
    fi
    
    # If no tools available, assume port is available
    return 0
}

# Function to get process information for a port
get_port_process_info() {
    local port="$1"
    local process_info=""
    
    # Try ss first (most reliable)
    if command_exists ss; then
        # Try to get process name with elevated permissions if available
        local proc_name=""
        if command_exists sudo; then
            proc_name=$(sudo -n ss -tlnp 2>/dev/null | awk -v port=":$port" '$4 ~ port"$" {gsub(/.*users:\(\("/, "", $7); gsub(/",.*/, "", $7); print $7}' | head -1)
        fi
        
        if [ -n "$proc_name" ]; then
            process_info="$proc_name"
        fi
    fi
    
    # If ss didn't work, try lsof
    if [ -z "$process_info" ] && command_exists lsof; then
        # Try to get process name with elevated permissions if available
        local proc_name=""
        if command_exists sudo; then
            proc_name=$(sudo -n lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $1}' | head -1)
        fi
        
        if [ -n "$proc_name" ]; then
            process_info="$proc_name"
        fi
    fi
    
    # If still no info, try netstat as last resort
    if [ -z "$process_info" ] && command_exists netstat; then
        # Try to get process name with elevated permissions if available
        proc_name=""
        if command_exists sudo; then
            # Single awk command that matches local address ending with :<port> and extracts program name
            proc_name=$(sudo -n netstat -tlnp 2>/dev/null | awk -v port=":$port" '$4 ~ port"$" {split($7, a, "/"); print a[2]}' | head -1)
        fi
        
        if [ -n "$proc_name" ]; then
            process_info="$proc_name"
        else
            # Check if port is in use without process info
            if netstat -tln 2>/dev/null | awk -v port=":$port" '$4 ~ port"$" {exit 0} END {exit 1}'; then
                process_info="(permission denied to get process name)"
            fi
        fi
    fi
    
    # Return the process info or "unknown process"
    if [ -n "$process_info" ]; then
        echo "$process_info"
    else
        echo "unknown process"
    fi
}

# Function to check system prerequisites for Podman
check_prerequisites() {
    print_message "🔧 Checking system prerequisites for Podman..." "$YELLOW"
    log_message "INFO" "Starting system prerequisites check for Podman"

    # Check CPU architecture and generation
    case "$(uname -m)" in
        "x86_64")
            log_message "INFO" "Detected x86_64 architecture, checking for AVX2 support"
            # Check CPU flags for AVX2 (Haswell and newer)
            if ! grep -q "avx2" /proc/cpuinfo; then
                log_message "ERROR" "CPU requirements not met: AVX2 support required for x86_64"
                send_telemetry_event "error" "CPU requirements not met" "error" "step=check_prerequisites,error=no_avx2"
                print_message "❌ Your Intel CPU is too old. BirdNET-Go requires Intel Haswell (2013) or newer CPU with AVX2 support" "$RED"
                exit 1
            else
                log_message "INFO" "CPU architecture check passed: x86_64 with AVX2 support"
                print_message "✅ Intel CPU architecture and generation check passed" "$GREEN"
            fi
            ;;
        "aarch64"|"arm64")
            log_message "INFO" "Detected ARM 64-bit architecture"
            print_message "✅ ARM 64-bit architecture detected, continuing with installation" "$GREEN"
            ;;
        "armv7l"|"armv6l"|"arm")
            log_message "ERROR" "Unsupported architecture: 32-bit ARM detected"
            send_telemetry_event "error" "Architecture requirements not met" "error" "step=check_prerequisites,error=32bit_arm"
            print_message "❌ 32-bit ARM architecture detected. BirdNET-Go requires 64-bit ARM processor and OS" "$RED"
            exit 1
            ;;
        *)
            log_message "ERROR" "Unsupported CPU architecture: $(uname -m)"
            send_telemetry_event "error" "Unsupported CPU architecture" "error" "step=check_prerequisites,error=unsupported_arch,arch=$(uname -m)"
            print_message "❌ Unsupported CPU architecture: $(uname -m)" "$RED"
            exit 1
            ;;
    esac

    # shellcheck source=/etc/os-release
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        log_message "INFO" "Detected OS: $ID $VERSION_ID ($NAME)"
    else
        log_message "ERROR" "Cannot determine OS version - /etc/os-release not found"
        print_message "❌ Cannot determine OS version" "$RED"
        exit 1
    fi

    # Check for supported distributions
    case "$ID" in
        debian)
            # Check version first, then handle Raspberry Pi OS based on version
            if [ -n "$VERSION_ID" ] && [ "$VERSION_ID" -lt 13 ]; then
                # Version is too old - check if it's RPi OS for specific messaging
                if [[ $(uname -r) =~ rpt-rpi ]] || grep -q "Raspberry Pi" /proc/device-tree/model 2>/dev/null; then
                    log_message "ERROR" "Raspberry Pi OS based on old Debian $VERSION_ID detected"
                    print_message "❌ Raspberry Pi OS based on old Debian $VERSION_ID detected" "$RED"
                    print_message "" "$NC"
                    print_message "📋 Podman requirements:" "$YELLOW"
                    print_message "  • Debian 13 (Trixie) or newer required" "$NC"
                    print_message "  • Your RPi OS is based on old Debian $VERSION_ID" "$NC"
                    print_message "  • Old versions have incompatible libc6 and dependencies" "$NC"
                    print_message "" "$NC"
                    print_message "💡 Options:" "$GREEN"
                    print_message "  1. Upgrade to Raspberry Pi OS based on Debian 13+" "$NC"
                    print_message "  2. Use the Docker-based install.sh instead" "$NC"
                    print_message "  3. Install pure Debian 13+ on Raspberry Pi (advanced)" "$NC"
                    print_message "" "$NC"
                else
                    log_message "ERROR" "Debian version $VERSION_ID too old for Podman, minimum version 13 (Trixie) required"
                    print_message "❌ Debian $VERSION_ID too old for Podman support" "$RED"
                    print_message "" "$NC"
                    print_message "📋 Podman requirements:" "$YELLOW"
                    print_message "  • Debian 13 (Trixie) or newer required" "$NC"
                    print_message "  • Includes Podman 5.4+ with proper dependencies" "$NC"
                    print_message "  • Avoids libc6 and dependency conflicts" "$NC"
                    print_message "" "$NC"
                    print_message "💡 Options for Debian $VERSION_ID:" "$GREEN"
                    print_message "  1. Upgrade to Debian 13: apt update && apt full-upgrade" "$NC"
                    print_message "  2. Use the Docker-based install.sh instead" "$NC"
                    print_message "" "$NC"
                fi
                print_message "📖 Debian 13 released: August 9th, 2025" "$GRAY"
                exit 1
            else
                # Version 13+ detected - check if it's RPi OS for specific messaging
                if [[ $(uname -r) =~ rpt-rpi ]] || grep -q "Raspberry Pi" /proc/device-tree/model 2>/dev/null; then
                    log_message "INFO" "OS compatibility check passed: Raspberry Pi OS based on Debian $VERSION_ID (Podman supported)"
                    print_message "✅ Raspberry Pi OS based on Debian $VERSION_ID found - Podman support available! 🎉" "$GREEN"
                    print_message "🎯 This is the first officially supported Raspberry Pi OS with Podman!" "$YELLOW"
                else
                    log_message "INFO" "OS compatibility check passed: Debian $VERSION_ID (Podman supported)"
                    print_message "✅ Debian $VERSION_ID found - Podman support available" "$GREEN"
                fi
            fi
            ;;
        raspbian)
            log_message "ERROR" "32-bit Raspberry Pi OS detected"
            print_message "❌ 32-bit Raspberry Pi OS detected" "$RED"
            print_message "" "$NC"
            print_message "🔍 Issues with current system:" "$YELLOW"
            print_message "  • 32-bit architecture not supported by BirdNET-Go" "$NC"
            print_message "  • Raspberry Pi OS not yet available for Debian 13" "$NC"
            print_message "" "$NC"
            print_message "💡 Required:" "$GREEN"
            print_message "  • 64-bit Raspberry Pi OS based on Debian 13+" "$NC"
            print_message "  • Or pure Debian 13+ on Raspberry Pi" "$NC"
            print_message "" "$NC"
            exit 1
            ;;
        ubuntu)
            # Ubuntu 25.04+ has Podman 5.4.1 with full Quadlet support
            ubuntu_version=$(echo "$VERSION_ID" | awk -F. '{print $1$2}')
            if [ "$ubuntu_version" -lt 2504 ]; then
                log_message "ERROR" "Ubuntu version $VERSION_ID too old for reliable Podman support"
                print_message "❌ Ubuntu $VERSION_ID - Podman requires 25.04+ with Podman 5.4.1" "$RED"
                print_message "" "$NC"
                print_message "📋 Podman requirements:" "$YELLOW"
                print_message "  • Ubuntu 25.04 or newer required" "$NC"
                print_message "  • Includes Podman 5.4.1 with full Quadlet support" "$NC"
                print_message "  • Older Ubuntu versions have outdated Podman packages" "$NC"
                print_message "" "$NC"
                print_message "💡 Options for Ubuntu $VERSION_ID:" "$GREEN"
                print_message "  1. Upgrade to Ubuntu 25.04+: do-release-upgrade" "$NC"
                print_message "  2. Use the Docker-based install.sh instead" "$NC"
                print_message "" "$NC"
                exit 1
            else
                log_message "INFO" "OS compatibility check passed: Ubuntu $VERSION_ID (Podman 5.4.1+ available)"
                print_message "✅ Ubuntu $VERSION_ID found - includes Podman 5.4.1 with full Quadlet support" "$GREEN"
            fi
            ;;
        *)
            log_message "ERROR" "Unsupported Linux distribution: $ID"
            print_message "❌ Unsupported Linux distribution: $ID" "$RED"
            print_message "" "$NC"
            print_message "📋 Supported distributions for Podman:" "$YELLOW"
            print_message "  • Debian 13 (Trixie) or newer" "$NC"
            print_message "  • Ubuntu 25.04 or newer (includes Podman 5.4.1)" "$NC"
            print_message "  • Raspberry Pi OS based on Debian 13+" "$NC"
            print_message "" "$NC"
            print_message "💡 Alternative:" "$GREEN"
            print_message "  • Use the Docker-based install.sh for broader compatibility" "$NC"
            print_message "" "$NC"
            exit 1
            ;;
    esac

    # Function to add user to required groups (no docker group for Podman)
    add_user_to_groups() {
        print_message "🔧 Adding user $USER to required groups..." "$YELLOW"
        local groups_added=false

        # Audio group for sound device access
        if ! groups "$USER" | grep &>/dev/null "\baudio\b"; then
            if sudo usermod -aG audio "$USER"; then
                print_message "✅ Added user $USER to audio group" "$GREEN"
                groups_added=true
            else
                print_message "❌ Failed to add user $USER to audio group" "$RED"
                exit 1
            fi
        fi

        # Add user to adm group for journalctl access
        if ! groups "$USER" | grep &>/dev/null "\badm\b"; then
            if sudo usermod -aG adm "$USER"; then
                print_message "✅ Added user $USER to adm group" "$GREEN"
                groups_added=true
            else
                print_message "❌ Failed to add user $USER to adm group" "$RED"
                exit 1
            fi
        fi

        # NOTE: No docker group needed for Podman rootless operation
        print_message "✅ Podman runs rootless - no container runtime group membership required" "$GREEN"

        if [ "$groups_added" = true ]; then
            print_message "Please log out and log back in for group changes to take effect, and rerun podman-install.sh to continue with install" "$YELLOW"
            exit 0
        fi
    }

    # Check and install Podman from native repositories  
    if ! command_exists podman; then
        log_message "INFO" "Podman not found, installing from native repositories"
        print_message "📦 Podman not found. Installing Podman..." "$YELLOW"
        
        # Install from native repositories (Debian 13+ and Ubuntu 24.04+ have modern Podman)
        print_message "📦 Installing Podman from native repositories..." "$YELLOW"
        
        case "$ID" in
            debian)
                # Debian 13+ has Podman 5.x in main repos - no testing repo needed!
                log_message "INFO" "Installing Podman from Debian 13+ native repositories"
                ;;
            ubuntu)
                # Ubuntu 24.04+ has modern Podman in universe - enable if needed
                log_message "INFO" "Installing Podman from Ubuntu 24.04+ native repositories"
                if ! grep -q "universe" /etc/apt/sources.list /etc/apt/sources.list.d/* 2>/dev/null; then
                    sudo add-apt-repository -y universe
                    log_command_result "enable ubuntu universe repository" $? "universe repository setup"
                fi
                ;;
        esac
        
        # Update package list
        sudo apt -qq update
        log_command_result "apt update" $? "Podman installation preparation"
        
        # Install Podman and related tools from native repos
        if sudo apt -qq install -y podman; then
            log_command_result "apt install podman" $? "Podman package installation"
            print_message "✅ Podman installed successfully" "$GREEN"
            
            # Try to install podman-compose if available (optional)
            if sudo apt -qq install -y podman-compose 2>/dev/null; then
                log_message "INFO" "podman-compose installed successfully"
                print_message "✅ podman-compose installed" "$GREEN"
            else
                log_message "INFO" "podman-compose not available in repositories (optional)"
                print_message "ℹ️ podman-compose not available (not required)" "$YELLOW"
            fi
        else
            log_message "ERROR" "Failed to install Podman from native repositories"
            print_message "❌ Failed to install Podman" "$RED"
            print_message "" "$NC"
            print_message "This may indicate:" "$YELLOW"
            print_message "  • System not fully updated to supported version" "$NC"
            print_message "  • Repository access issues" "$NC"
            print_message "  • Package dependency conflicts" "$NC"
            print_message "" "$NC"
            print_message "Try: sudo apt update && sudo apt full-upgrade" "$YELLOW"
            exit 1
        fi
        
        # Verify Podman version
        local podman_version
        podman_version=$(podman --version | cut -d' ' -f3)
        log_message "INFO" "Installed Podman version: $podman_version"
        print_message "✅ Podman version: $podman_version" "$GREEN"
        
        # Add current user to required groups
        add_user_to_groups
        
        log_message "INFO" "Podman installation completed"
        print_message "⚠️ Podman installed successfully. Please log out and log back in if group changes were made, then rerun podman-install.sh to continue with install" "$YELLOW"
        # Don't exit here if no group changes were made
        
    else
        log_message "INFO" "Podman already installed and available"
        print_message "✅ Podman found" "$GREEN"
        
        # Check Podman version
        local podman_version
        podman_version=$(podman --version | cut -d' ' -f3 2>/dev/null || echo "unknown")
        print_message "✅ Podman version: $podman_version" "$GREEN"
        
        # Check if user is in required groups
        add_user_to_groups

        # Check if Podman can be used by the user
        if ! podman info &>/dev/null; then
            log_message "ERROR" "Podman installed but not accessible by user $USER"
            print_message "❌ Podman cannot be accessed by user $USER. Please ensure you have the necessary permissions." "$RED"
            print_message "Try running: podman system migrate" "$YELLOW"
            exit 1
        else
            log_message "INFO" "Podman accessibility check passed for user $USER"
            print_message "✅ Podman is accessible by user $USER" "$GREEN"
        fi
    fi

    # Check port availability for rootless Podman (ports < 1024 need special handling)
    print_message "🔌 Checking required port availability..." "$YELLOW"
    local ports_to_check=("80" "443" "${WEB_PORT:-8080}" "8090")
    local unique_ports=()
    local failed_ports=()
    local port_processes=()
    local port
    local process_info
    
    # Use associative array for efficient deduplication
    local -A seen
    
    # Deduplicate ports array to avoid double-checking
    for port in "${ports_to_check[@]}"; do
        # Skip empty entries
        if [ -z "$port" ]; then
            continue
        fi
        
        # Only add if not seen before
        if [ -z "${seen[$port]:-}" ]; then
            seen[$port]=1
            unique_ports+=("$port")
        fi
    done
    
    # Check each port and handle rootless considerations
    for port in "${unique_ports[@]}"; do
        if ! check_port_availability "$port"; then
            failed_ports+=("$port")
            process_info=$(get_port_process_info "$port")
            port_processes+=("$process_info")
            print_message "❌ Port $port is already in use by: $process_info" "$RED"
        else
            # For rootless Podman, ports < 1024 need special consideration
            if [ "$port" -lt 1024 ]; then
                print_message "⚠️ Port $port is available but requires rootful Podman or port mapping (< 1024)" "$YELLOW"
                print_message "   Podman will handle privileged port access automatically" "$YELLOW"
            else
                print_message "✅ Port $port is available" "$GREEN"
            fi
        fi
    done
    
    # If any ports are in use, show detailed error and exit
    if [ ${#failed_ports[@]} -gt 0 ]; then
        print_message "\n❌ ERROR: Required ports are not available" "$RED"
        print_message "\nBirdNET-Go requires the following ports to be available:" "$YELLOW"
        print_message "  • Port 80   - HTTP web interface" "$YELLOW"
        print_message "  • Port 443  - HTTPS web interface (with SSL)" "$YELLOW"
        local web_port_display="${WEB_PORT:-8080}"
        if [ "$web_port_display" != "80" ] && [ "$web_port_display" != "443" ]; then
            print_message "  • Port $web_port_display - Primary web interface" "$YELLOW"
        fi
        print_message "  • Port 8090 - Prometheus metrics endpoint" "$YELLOW"
        
        print_message "\n📋 Ports currently in use:" "$RED"
        for i in "${!failed_ports[@]}"; do
            print_message "  • Port ${failed_ports[$i]} - Used by: ${port_processes[$i]}" "$RED"
        done
        
        print_message "\n💡 To resolve this issue, you can:" "$YELLOW"
        print_message "\n1. Stop the conflicting services:" "$YELLOW"
        
        # Provide specific instructions based on common services
        for i in "${!failed_ports[@]}"; do
            local failed_port="${failed_ports[$i]}"
            local process="${port_processes[$i]}"
            # Convert to lowercase for case-insensitive matching
            local process_lower
            process_lower=$(echo "$process" | tr '[:upper:]' '[:lower:]')
            
            if [[ "$process_lower" == *"apache"* ]] || [[ "$process_lower" == *"httpd"* ]]; then
                print_message "   sudo systemctl stop apache2  # For Apache on port $failed_port" "$NC"
            elif [[ "$process_lower" == *"nginx"* ]]; then
                print_message "   sudo systemctl stop nginx    # For Nginx on port $failed_port" "$NC"
            elif [[ "$process_lower" == *"lighttpd"* ]]; then
                print_message "   sudo systemctl stop lighttpd # For Lighttpd on port $failed_port" "$NC"
            elif [[ "$process_lower" == *"caddy"* ]]; then
                print_message "   sudo systemctl stop caddy    # For Caddy on port $failed_port" "$NC"
            elif [[ "$failed_port" == "80" ]] || [[ "$failed_port" == "443" ]]; then
                print_message "   sudo systemctl stop <service> # Replace <service> with the service using port $failed_port" "$NC"
            fi
        done
        
        print_message "\n2. Or use Podman with different port mappings:" "$YELLOW"
        print_message "   Modify the Quadlet configuration after installation to use different ports" "$NC"
        
        print_message "\n3. Or uninstall conflicting software if not needed:" "$YELLOW"
        print_message "   sudo apt remove <package-name>" "$NC"
        
        print_message "\n⚠️  Note: For ports < 1024, Podman can handle privileged access automatically" "$YELLOW"
        print_message "🔄 If you have Docker-based BirdNET-Go running, restart this script - it will detect and stop it automatically" "$YELLOW"
        
        send_telemetry_event "error" "Port availability check failed" "error" "step=check_prerequisites,failed_ports=${failed_ports[*]}"
        exit 1
    fi
    
    print_message "✅ All required ports are available" "$GREEN"

    # Check if quadlet/systemd integration is available
    if command_exists systemctl; then
        print_message "✅ Systemd available for Quadlet integration" "$GREEN"
        log_message "INFO" "Systemd available for Quadlet service management"
    else
        print_message "❌ Systemd not available - Quadlet integration not possible" "$RED"
        log_message "ERROR" "Systemd not available for Quadlet service management"
        exit 1
    fi

    log_message "INFO" "System prerequisites check completed successfully"
    print_message "🥳 System prerequisites checks passed" "$GREEN"
    print_message ""
}

# Function to check if systemd is the init system
check_systemd() {
    if [ "$(ps -p 1 -o comm=)" != "systemd" ]; then
        print_message "❌ This script requires systemd as the init system" "$RED"
        print_message "Your system appears to be using: $(ps -p 1 -o comm=)" "$YELLOW"
        exit 1
    else
        print_message "✅ Systemd detected as init system" "$GREEN"
    fi
}

# Function to show usage information
show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo "Install or update BirdNET-Go with Podman container runtime"
    echo ""
    echo "OPTIONS:"
    echo "  -v, --version VERSION    Specify container image version (tag)"
    echo "                          Default: podman-nightly"
    echo "                          Examples: podman-latest, podman-v1.2.3, podman-nightly"
    echo "  -h, --help              Show this help message"
    echo ""
    echo "EXAMPLES:"
    echo "  $0                           # Install using podman-nightly version (default)"
    echo "  $0 -v podman-latest         # Install using latest stable Podman-optimized version"
    echo "  $0 -v podman-v1.2.3         # Install specific Podman-optimized version tag"
    echo "  $0 --version podman-nightly # Explicitly use nightly Podman version"
    echo ""
    echo "SYSTEM REQUIREMENTS:"
    echo "  • Debian 13 (Trixie) or newer"
    echo "  • Ubuntu 25.04 or newer (includes Podman 5.4.1)"
    echo "  • Raspberry Pi OS based on Debian 13+"
    echo "  • 64-bit ARM or x86_64 architecture"
    echo "  • systemd init system"
    echo ""
    echo "NOTES:"
    echo "  • This script installs Podman-optimized container images"
    echo "  • Uses Quadlet for systemd integration instead of traditional services"
    echo "  • Runs containers in rootless mode by default"
    echo "  • Supports bridge networking without NAT overhead"
    echo "  • For older systems, use install.sh (Docker-based) instead"
    echo ""
}

# Function to parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -v|--version)
                if [ -n "$2" ] && [[ $2 != -* ]]; then
                    BIRDNET_GO_VERSION="$2"
                    shift 2
                else
                    echo "❌ Error: --version requires a value" >&2
                    echo ""
                    show_usage
                    exit 1
                fi
                ;;
            -h|--help)
                show_usage
                exit 0
                ;;
            -*)
                echo "❌ Error: Unknown option $1" >&2
                echo ""
                show_usage
                exit 1
                ;;
            *)
                echo "❌ Error: Unexpected argument $1" >&2
                echo ""
                show_usage
                exit 1
                ;;
        esac
    done
    
    # Validate that the version starts with podman- prefix
    if [[ ! "$BIRDNET_GO_VERSION" =~ ^podman- ]]; then
        print_message "⚠️ Adding 'podman-' prefix to version: $BIRDNET_GO_VERSION" "$YELLOW"
        BIRDNET_GO_VERSION="podman-$BIRDNET_GO_VERSION"
    fi
    
    # Set the container image URL after parsing arguments
    BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:${BIRDNET_GO_VERSION}"
    
    # Log the version being used
    echo "📦 Using BirdNET-Go Podman version: $BIRDNET_GO_VERSION"
}

# Function to safely execute podman commands
safe_podman() {
    if command_exists podman; then
        podman "$@" 2>/dev/null
        return $?
    fi
    return 1
}

# Parse command line arguments first
parse_arguments "$@"

# Initialize logging system
setup_logging

# Default paths
CONFIG_DIR="$HOME/birdnet-go-app/config"
DATA_DIR="$HOME/birdnet-go-app/data"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
WEB_PORT=8080  # Default web port
COCKPIT_PORT=9090  # Default Cockpit port
QUADLET_DIR="$HOME/.config/containers/systemd"
# For Podman, always include device mapping
AUDIO_ENV="--device /dev/snd:/dev/snd"
# Flag for fresh installation
FRESH_INSTALL="false"
# Configured timezone (will be set during configuration)
CONFIGURED_TZ=""

# Load telemetry configuration if it exists
# Note: Reusing same telemetry system as Docker version for consistency
TELEMETRY_ENABLED=false
TELEMETRY_INSTALL_ID=""
SENTRY_DSN="https://b9269b6c0f8fae154df65be5a97e0435@o4509553065525248.ingest.de.sentry.io/4509553112186960"

# Function to generate anonymous install ID
generate_install_id() {
    # Generate a UUID-like ID using /dev/urandom
    local id=$(dd if=/dev/urandom bs=16 count=1 2>/dev/null | od -x -An | tr -d ' \n' | cut -c1-32)
    # Format as UUID: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX
    echo "${id:0:8}-${id:8:4}-${id:12:4}-${id:16:4}-${id:20:12}"
}

# Function to load or create telemetry config
load_telemetry_config() {
    local telemetry_file="$CONFIG_DIR/.telemetry"
    
    if [ -f "$telemetry_file" ]; then
        # Load existing config
        TELEMETRY_ENABLED=$(grep "^enabled=" "$telemetry_file" 2>/dev/null | cut -d'=' -f2 || echo "false")
        TELEMETRY_INSTALL_ID=$(grep "^install_id=" "$telemetry_file" 2>/dev/null | cut -d'=' -f2 || echo "")
    fi
    
    # Generate install ID if missing
    if [ -z "$TELEMETRY_INSTALL_ID" ]; then
        TELEMETRY_INSTALL_ID=$(generate_install_id)
    fi
}

# Function to send telemetry event (reusing same telemetry system)
send_telemetry_event() {
    # Check if telemetry is enabled
    if [ "$TELEMETRY_ENABLED" != "true" ]; then
        return 0
    fi
    
    local event_type="$1"
    local message="$2"
    local level="${3:-info}"
    local context="${4:-}"
    
    # Add podman context to distinguish from Docker installs
    local full_context="runtime=podman,${context}"
    
    # Collect system info before background process
    local system_info
    system_info=$(collect_system_info)
    
    # Run in background to not block installation
    {
        
        # Build JSON payload
        local timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        local payload=$(cat <<EOF
{
    "timestamp": "$timestamp",
    "level": "$level",
    "message": "$message",
    "platform": "other",
    "environment": "production",
    "release": "podman-install-script@1.0.0",
    "tags": {
        "event_type": "$event_type",
        "script_version": "podman-1.0.0",
        "container_runtime": "podman"
    },
    "contexts": {
        "os": {
            "name": "$(echo "$system_info" | jq -r .os_name)",
            "version": "$(echo "$system_info" | jq -r .os_version)"
        },
        "device": {
            "arch": "$(echo "$system_info" | jq -r .cpu_arch)",
            "model": "$(echo "$system_info" | jq -r .pi_model)"
        }
    },
    "extra": {
        "podman_version": "$(echo "$system_info" | jq -r .podman_version)",
        "install_id": "$(echo "$system_info" | jq -r .install_id)",
        "context": "$full_context"
    }
}
EOF
)
        
        # Extract DSN components
        local sentry_key=$(echo "$SENTRY_DSN" | grep -oE 'https://[a-f0-9]+' | sed 's/https:\/\///')
        local sentry_project=$(echo "$SENTRY_DSN" | grep -oE '[0-9]+$')
        local sentry_host=$(echo "$SENTRY_DSN" | grep -oE '@[^/]+' | sed 's/@//')
        
        # Send to Sentry (timeout after 5 seconds, silent failure)
        curl -s -m 5 \
            -X POST \
            "https://${sentry_host}/api/${sentry_project}/store/" \
            -H "Content-Type: application/json" \
            -H "X-Sentry-Auth: Sentry sentry_key=${sentry_key}, sentry_version=7" \
            -d "$payload" \
            >/dev/null 2>&1 || true
    } &
    
    # Return immediately
    return 0
}

# Function to collect system info for telemetry (adapted for Podman)
collect_system_info() {
    local os_name="unknown"
    local os_version="unknown"
    local cpu_arch=$(uname -m)
    local podman_version="unknown"
    local pi_model="none"
    
    # Read OS information from /etc/os-release
    if [ -f /etc/os-release ]; then
        # Source the file to get the variables
        . /etc/os-release
        os_name="${ID:-unknown}"
        os_version="${VERSION_ID:-unknown}"
    fi
    
    # Get Podman version if available
    if command_exists podman; then
        podman_version=$(podman --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown")
    fi
    
    # Detect Raspberry Pi model or WSL
    if [ -f /proc/device-tree/model ]; then
        pi_model=$(cat /proc/device-tree/model 2>/dev/null | tr -d '\0' | sed 's/Raspberry Pi/RPi/g' || echo "none")
    elif grep -q microsoft /proc/version 2>/dev/null; then
        pi_model="wsl"
    fi
    
    # Output as JSON
    echo "{\"os_name\":\"$os_name\",\"os_version\":\"$os_version\",\"cpu_arch\":\"$cpu_arch\",\"podman_version\":\"$podman_version\",\"pi_model\":\"$pi_model\",\"install_id\":\"$TELEMETRY_INSTALL_ID\"}"
}

# =============================================================================
# DOCKER CONFLICT DETECTION AND TRANSITION FUNCTIONS
# =============================================================================

# Function to safely execute docker commands, suppressing errors if Docker isn't installed
safe_docker() {
    if command_exists docker; then
        docker "$@" 2>/dev/null
        return $?
    fi
    return 1
}

# Function to detect Docker-based BirdNET-Go installation
detect_docker_birdnet_installation() {
    local docker_service_exists=false
    local docker_image_exists=false
    local docker_container_exists=false
    local docker_container_running=false
    
    # Check for Docker systemd service
    if [ -f "/etc/systemd/system/birdnet-go.service" ] || [ -f "/lib/systemd/system/birdnet-go.service" ]; then
        docker_service_exists=true
    fi
    
    # Check if Docker is installed and accessible
    if command_exists docker && docker info &>/dev/null; then
        # Check for BirdNET-Go Docker images
        if safe_docker images --format "{{.Repository}}:{{.Tag}}" | grep -q "birdnet-go"; then
            docker_image_exists=true
        fi
        
        # Check for BirdNET-Go containers
        local container_count
        container_count=$(safe_docker ps -a | grep "birdnet-go" | wc -l || echo "0")
        # Clean up any whitespace/newlines
        container_count=$(echo "$container_count" | tr -d ' \n\r')
        
        if [ "$container_count" -gt 0 ] 2>/dev/null; then
            docker_container_exists=true
            
            # Check if any containers are running
            local running_count
            running_count=$(safe_docker ps | grep "birdnet-go" | wc -l || echo "0")
            # Clean up any whitespace/newlines
            running_count=$(echo "$running_count" | tr -d ' \n\r')
            if [ "$running_count" -gt 0 ] 2>/dev/null; then
                docker_container_running=true
            fi
        fi
    fi
    
    # Return status codes
    if [ "$docker_service_exists" = true ] || [ "$docker_image_exists" = true ] || [ "$docker_container_exists" = true ]; then
        return 0  # Docker installation detected
    else
        return 1  # No Docker installation found
    fi
}

# Function to get Docker installation details
get_docker_installation_details() {
    local details=""
    
    # Check service status
    if [ -f "/etc/systemd/system/birdnet-go.service" ]; then
        local service_status
        service_status=$(systemctl is-active birdnet-go.service 2>/dev/null || echo "unknown")
        details="${details}Service: active ($service_status). "
    fi
    
    # Check containers
    if command_exists docker && docker info &>/dev/null; then
        local running_containers
        running_containers=$(safe_docker ps | grep "birdnet-go" | wc -l || echo "0")
        running_containers=$(echo "$running_containers" | tr -d ' \n\r')
        local total_containers
        total_containers=$(safe_docker ps -a | grep "birdnet-go" | wc -l || echo "0")
        total_containers=$(echo "$total_containers" | tr -d ' \n\r')
        details="${details}Containers: $running_containers running, $total_containers total. "
        
        # Check images
        local images
        images=$(safe_docker images --format "{{.Repository}}:{{.Tag}}" | grep "birdnet-go" | wc -l || echo "0")
        images=$(echo "$images" | tr -d ' \n\r')
        details="${details}Images: $images. "
    fi
    
    echo "$details"
}

# Function to stop Docker-based BirdNET-Go services
stop_docker_services() {
    log_message "INFO" "Stopping Docker-based BirdNET-Go services"
    local stopped_something=false
    
    # Stop systemd service if it exists
    if systemctl is-active --quiet birdnet-go.service 2>/dev/null; then
        print_message "🛑 Stopping Docker-based BirdNET-Go service..." "$YELLOW"
        if sudo systemctl stop birdnet-go.service; then
            log_command_result "systemctl stop birdnet-go.service" $? "Docker service stop"
            print_message "✅ Docker service stopped successfully" "$GREEN"
            stopped_something=true
        else
            log_message "ERROR" "Failed to stop Docker service"
            print_message "❌ Failed to stop Docker service" "$RED"
            return 1
        fi
    fi
    
    # Stop running containers
    if command_exists docker && docker info &>/dev/null; then
        local running_containers
        running_containers=$(safe_docker ps --filter "name=birdnet-go" --format "{{.Names}}" | tr '\n' ' ')
        
        if [ -n "$running_containers" ]; then
            print_message "🛑 Stopping Docker containers: $running_containers" "$YELLOW"
            for container in $running_containers; do
                if docker stop "$container" >/dev/null 2>&1; then
                    log_message "INFO" "Stopped Docker container: $container"
                    print_message "✅ Stopped container: $container" "$GREEN"
                    stopped_something=true
                else
                    log_message "ERROR" "Failed to stop Docker container: $container"
                    print_message "❌ Failed to stop container: $container" "$RED"
                fi
            done
        fi
    fi
    
    if [ "$stopped_something" = true ]; then
        print_message "✅ Docker services stopped successfully" "$GREEN"
        return 0
    else
        print_message "ℹ️ No running Docker services found to stop" "$YELLOW"
        return 0
    fi
}

# Function to disable Docker services to prevent conflicts
disable_docker_services() {
    log_message "INFO" "Disabling Docker-based BirdNET-Go services"
    local disabled_something=false
    
    # Disable systemd service
    if systemctl is-enabled --quiet birdnet-go.service 2>/dev/null; then
        print_message "⏸️ Disabling Docker-based BirdNET-Go service to prevent conflicts..." "$YELLOW"
        if sudo systemctl disable birdnet-go.service; then
            log_command_result "systemctl disable birdnet-go.service" $? "Docker service disable"
            print_message "✅ Docker service disabled successfully" "$GREEN"
            disabled_something=true
        else
            log_message "ERROR" "Failed to disable Docker service"
            print_message "❌ Failed to disable Docker service" "$RED"
            return 1
        fi
    fi
    
    if [ "$disabled_something" = true ]; then
        print_message "✅ Docker services disabled to prevent conflicts" "$GREEN"
        return 0
    else
        print_message "ℹ️ No Docker services found to disable" "$YELLOW"
        return 0
    fi
}

# Function to preserve and validate existing data directories
preserve_docker_data() {
    log_message "INFO" "Checking Docker data preservation"
    
    # Check if Docker config/data directories exist
    local docker_config_exists=false
    local docker_data_exists=false
    
    if [ -d "$CONFIG_DIR" ]; then
        docker_config_exists=true
        print_message "✅ Found existing configuration directory: $CONFIG_DIR" "$GREEN"
        
        # Check if config file exists
        if [ -f "$CONFIG_FILE" ]; then
            print_message "✅ Found existing configuration file: $CONFIG_FILE" "$GREEN"
            log_message "INFO" "Existing config file will be preserved"
        fi
    fi
    
    if [ -d "$DATA_DIR" ]; then
        docker_data_exists=true
        print_message "✅ Found existing data directory: $DATA_DIR" "$GREEN"
        
        # Check data directory contents
        local data_size
        data_size=$(du -sh "$DATA_DIR" 2>/dev/null | cut -f1 || echo "unknown")
        print_message "  📊 Data size: $data_size" "$GRAY"
        log_message "INFO" "Existing data directory size: $data_size"
    fi
    
    # Create backup timestamp for safety
    if [ "$docker_config_exists" = true ] || [ "$docker_data_exists" = true ]; then
        local backup_timestamp
        backup_timestamp=$(date +"%Y%m%d-%H%M%S")
        
        print_message "💾 Creating safety backup timestamp: $backup_timestamp" "$YELLOW"
        
        # Create backup info file
        cat > "$CONFIG_DIR/.docker-to-podman-transition" << EOF
# Docker to Podman Transition Info
transition_timestamp=$backup_timestamp
transition_date=$(date)
transition_script=podman-install.sh
original_runtime=docker
new_runtime=podman
data_preserved=true
EOF
        
        log_message "INFO" "Created transition info file for safety"
        print_message "✅ Data directories will be preserved for Podman use" "$GREEN"
    fi
    
    return 0
}

# Function to handle Docker to Podman transition
handle_docker_transition() {
    log_message "INFO" "=== Starting Docker to Podman Transition ==="
    
    print_message "" "$NC"
    print_message "🔄 Docker to Podman Transition" "$YELLOW"
    print_message "==============================" "$GRAY"
    
    # Get current Docker installation details
    local docker_details
    docker_details=$(get_docker_installation_details)
    print_message "📋 Current Docker installation: $docker_details" "$GRAY"
    
    print_message "" "$NC"
    print_message "This will:" "$YELLOW"
    print_message "  ✅ Preserve all your existing configuration and data" "$GREEN"
    print_message "  🛑 Stop Docker-based BirdNET-Go services" "$YELLOW"
    print_message "  ⏸️  Disable Docker services to prevent conflicts" "$YELLOW"
    print_message "  🚀 Install BirdNET-Go with Podman + Quadlet" "$GREEN"
    print_message "  📁 Reuse existing data directories" "$GREEN"
    print_message "" "$NC"
    
    print_message "What would you like to do?" "$YELLOW"
    print_message "1) Automatic transition (recommended)" "$NC"
    print_message "2) Manual transition (stop services yourself)" "$NC"
    print_message "3) Exit and keep Docker installation" "$NC"
    print_message "" "$NC"
    
    read -p "Please select an option (1-3): " transition_choice
    
    case $transition_choice in
        1)
            log_message "INFO" "User selected automatic Docker transition"
            print_message "🤖 Performing automatic transition..." "$GREEN"
            
            # Stop Docker services
            if ! stop_docker_services; then
                print_message "❌ Failed to stop Docker services. Please stop them manually." "$RED"
                return 1
            fi
            
            # Disable Docker services
            if ! disable_docker_services; then
                print_message "❌ Failed to disable Docker services. Manual intervention needed." "$RED"
                return 1
            fi
            
            # Preserve data
            if ! preserve_docker_data; then
                print_message "❌ Failed to preserve Docker data." "$RED"
                return 1
            fi
            
            print_message "✅ Automatic transition completed successfully!" "$GREEN"
            print_message "🚀 Ready to proceed with Podman installation..." "$GREEN"
            send_telemetry_event "info" "Automatic Docker to Podman transition" "info" "method=automatic"
            return 0
            ;;
        2)
            log_message "INFO" "User selected manual Docker transition"
            print_message "👤 Manual transition selected" "$YELLOW"
            print_message "" "$NC"
            print_message "Please manually:" "$YELLOW"
            print_message "  1. Stop Docker service: sudo systemctl stop birdnet-go.service" "$NC"
            print_message "  2. Disable Docker service: sudo systemctl disable birdnet-go.service" "$NC"
            print_message "  3. Stop any running containers: docker stop <container_name>" "$NC"
            print_message "" "$NC"
            
            read -p "Press Enter when you have completed the manual steps..." -r
            
            # Still preserve data automatically
            if ! preserve_docker_data; then
                print_message "❌ Failed to preserve Docker data." "$RED"
                return 1
            fi
            
            print_message "✅ Manual transition completed!" "$GREEN"
            print_message "🚀 Ready to proceed with Podman installation..." "$GREEN"
            send_telemetry_event "info" "Manual Docker to Podman transition" "info" "method=manual"
            return 0
            ;;
        3|"")
            log_message "INFO" "User chose to exit and keep Docker installation"
            print_message "✋ Keeping Docker installation as requested" "$YELLOW"
            print_message "" "$NC"
            print_message "To use this script later, you can:" "$YELLOW"
            print_message "  • Stop Docker services manually" "$NC"
            print_message "  • Run this script again for automatic transition" "$NC"
            print_message "" "$NC"
            exit 0
            ;;
        *)
            log_message "ERROR" "Invalid transition selection: $transition_choice"
            print_message "❌ Invalid selection. Exiting." "$RED"
            exit 1
            ;;
    esac
}

# =============================================================================
# MAIN EXECUTION
# =============================================================================

print_message "Starting BirdNET-Go Podman installation script..." "$GREEN"
print_message "This script will install BirdNET-Go using Podman containers with Quadlet integration\n" "$YELLOW"

# Run initial system checks
check_systemd
check_network

# Check for Docker conflicts and handle transition BEFORE port checks
if detect_docker_birdnet_installation; then
    log_message "INFO" "Docker-based BirdNET-Go installation detected"
    print_message "" "$NC"
    print_message "⚠️ Docker-based BirdNET-Go installation detected!" "$YELLOW"
    
    docker_details=$(get_docker_installation_details)
    print_message "📋 Details: $docker_details" "$GRAY"
    
    # Handle the transition (stops Docker services that block ports)
    handle_docker_transition
else
    log_message "INFO" "No Docker-based BirdNET-Go installation detected"
    print_message "✅ No Docker conflicts detected - ready for clean Podman installation" "$GREEN"
fi

# Now run prerequisites check (including port availability) after Docker conflicts resolved
check_prerequisites

# Function to check if directory exists and create if needed
check_directory_exists() {
    local dir_path="$1"
    
    if [ ! -d "$dir_path" ]; then
        return 1
    else
        return 0
    fi
}

# Function to check and create directory
check_directory() {
    local dir_path="$1"
    local dir_name="$2"
    
    log_message "INFO" "Checking directory: $dir_path"
    
    if check_directory_exists "$dir_path"; then
        print_message "✅ $dir_name directory exists: $dir_path" "$GREEN"
        # Check permissions
        if [ -r "$dir_path" ] && [ -w "$dir_path" ]; then
            log_message "INFO" "Directory permissions check passed: $dir_path"
        else
            log_message "ERROR" "Insufficient permissions for directory: $dir_path"
            print_message "❌ Insufficient permissions for $dir_name directory" "$RED"
            exit 1
        fi
    else
        print_message "📁 Creating $dir_name directory: $dir_path" "$YELLOW"
        if mkdir -p "$dir_path" 2>/dev/null; then
            log_message "INFO" "Directory created successfully: $dir_path"
            print_message "✅ $dir_name directory created" "$GREEN"
        else
            log_message "ERROR" "Failed to create directory: $dir_path"
            print_message "❌ Failed to create $dir_name directory" "$RED"
            exit 1
        fi
    fi
}

# Function to pull Podman image
pull_podman_image() {
    log_message "INFO" "Starting Podman image pull: $BIRDNET_GO_IMAGE"
    print_message "\n📦 Pulling BirdNET-Go Podman image from GitHub Container Registry..." "$YELLOW"
    
    # Check if Podman can be used by the user
    if ! podman info &>/dev/null; then
        log_message "ERROR" "Podman not accessible by user $USER"
        print_message "❌ Podman cannot be accessed by user $USER. Please ensure you have the necessary permissions." "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- Podman not properly configured for rootless operation" "$YELLOW"
        print_message "- User session not reloaded after group changes" "$YELLOW"
        print_message "- Insufficient storage permissions in ~/.local/share/containers" "$YELLOW"
        print_message "Try running: podman system migrate" "$YELLOW"
        exit 1
    fi

    if podman pull "${BIRDNET_GO_IMAGE}"; then
        log_message "INFO" "Podman image pulled successfully: $BIRDNET_GO_IMAGE"
        print_message "✅ Podman image pulled successfully" "$GREEN"
    else
        log_message "ERROR" "Podman image pull failed: $BIRDNET_GO_IMAGE"
        send_telemetry_event "error" "Podman image pull failed" "error" "step=pull_podman_image,image=${BIRDNET_GO_IMAGE}"
        print_message "❌ Failed to pull Podman image" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- No internet connection" "$YELLOW"
        print_message "- GitHub container registry being unreachable" "$YELLOW"
        print_message "- Invalid image name or tag" "$YELLOW"
        print_message "- Podman registry authentication issues" "$YELLOW"
        exit 1
    fi
}

# Function to log network state
log_network_state() {
    local context="${1:-general}"
    
    log_message "INFO" "=== Network State Check ($context) ==="
    
    # Check default route
    local default_route
    if command_exists ip; then
        default_route=$(ip route show default 2>/dev/null | head -1 || echo "none")
        log_message "INFO" "Default route: $default_route"
    fi
    
    # Check DNS
    local dns_servers
    if [ -f /etc/resolv.conf ]; then
        dns_servers=$(grep nameserver /etc/resolv.conf | awk '{print $2}' | tr '\n' ' ' || echo "none")
        log_message "INFO" "DNS servers: $dns_servers"
    fi
    
    # Check network connectivity
    if ping -c 1 8.8.8.8 >/dev/null 2>&1; then
        log_message "INFO" "Network connectivity: OK"
    else
        log_message "ERROR" "Network connectivity: FAILED"
    fi
}

# Function to log Podman state
log_podman_state() {
    local context="${1:-general}"
    
    log_message "INFO" "=== Podman State Check ($context) ==="
    
    if command_exists podman; then
        # Podman version
        local podman_version
        podman_version=$(podman --version 2>/dev/null || echo "unknown")
        log_message "INFO" "Podman version: $podman_version"
        
        # Podman info (storage, etc.)
        local storage_driver
        storage_driver=$(podman info --format '{{.Store.GraphDriverName}}' 2>/dev/null || echo "unknown")
        log_message "INFO" "Podman storage driver: $storage_driver"
        
        # Image count
        local image_count
        image_count=$(podman images --quiet 2>/dev/null | wc -l || echo "unknown")
        log_message "INFO" "Total images: $image_count"
        
        # Container count
        local container_count
        container_count=$(podman ps -a --quiet 2>/dev/null | wc -l || echo "unknown")
        log_message "INFO" "Total containers: $container_count"
        
        # Running container count
        local running_count
        running_count=$(podman ps --quiet 2>/dev/null | wc -l || echo "unknown")
        log_message "INFO" "Running containers: $running_count"
    else
        log_message "ERROR" "Podman not found or not accessible"
    fi
}

# Function to log service state
log_service_state() {
    local context="${1:-general}"
    
    log_message "INFO" "=== Service State Check ($context) ==="
    
    # Check if birdnet-go service exists and its status
    if systemctl --user list-unit-files --type=service 2>/dev/null | grep -q "birdnet-go"; then
        local service_status
        service_status=$(systemctl --user is-active birdnet-go.service 2>/dev/null || echo "unknown")
        log_message "INFO" "BirdNET-Go service status: $service_status"
        
        local service_enabled
        service_enabled=$(systemctl --user is-enabled birdnet-go.service 2>/dev/null || echo "unknown")
        log_message "INFO" "BirdNET-Go service enabled: $service_enabled"
    else
        log_message "INFO" "BirdNET-Go service not found"
    fi
}

# Function to safely execute podman commands, suppressing errors if Podman isn't installed
safe_podman() {
    if command_exists podman; then
        podman "$@" 2>/dev/null
        return $?
    fi
    return 1
}

# Function to check if BirdNET-Go service exists (Quadlet)
detect_birdnet_service() {
    # Check for Quadlet service files
    if [ -f "$QUADLET_DIR/birdnet-go.container" ]; then
        return 0
    fi
    
    # Check if systemd --user service exists
    if systemctl --user list-unit-files --type=service 2>/dev/null | grep -q "birdnet-go"; then
        return 0
    fi
    
    return 1
}

# Function to check if BirdNET service exists
check_service_exists() {
    detect_birdnet_service
    return $?
}

# Function to check if BirdNET-Go is fully installed (service + container)
check_birdnet_installation() {
    local service_exists=false
    local image_exists=false
    local container_exists=false
    local container_running=false
    local debug_output=""

    # Check for Quadlet service
    if detect_birdnet_service; then
        service_exists=true
        debug_output="${debug_output}Quadlet service detected. "
    fi
    
    # Only check Podman components if Podman is installed
    if command_exists podman; then
        # Check for BirdNET-Go images
        if safe_podman images --format "{{.Repository}}:{{.Tag}}" | grep -q "birdnet-go"; then
            image_exists=true
            debug_output="${debug_output}Podman image exists. "
        fi
        
        # Check for any BirdNET-Go containers (running or stopped)
        container_count=$(safe_podman ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | wc -l)
        
        if [ "$container_count" -gt 0 ]; then
            container_exists=true
            debug_output="${debug_output}Container exists. "
            
            # Check if any of these containers are running
            running_count=$(safe_podman ps --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | wc -l)
            if [ "$running_count" -gt 0 ]; then
                container_running=true
                debug_output="${debug_output}Container running. "
            fi
        fi
        
        # Fallback check for containers with birdnet-go in the name
        if [ "$container_exists" = false ]; then
            if safe_podman ps -a | grep -q "birdnet-go"; then
                container_exists=true
                debug_output="${debug_output}Container with birdnet name exists. "
                
                # Check if any of these containers are running
                if safe_podman ps | grep -q "birdnet-go"; then
                    container_running=true
                    debug_output="${debug_output}Named container running. "
                fi
            fi
        fi
    fi
    
    log_message "INFO" "Installation check results: $debug_output"
    
    # Return status: 0=not installed, 1=partial, 2=full installation
    if [ "$service_exists" = true ] && [ "$image_exists" = true ] && [ "$container_exists" = true ]; then
        return 2  # Full installation
    elif [ "$service_exists" = true ] || [ "$image_exists" = true ] || [ "$container_exists" = true ]; then
        return 1  # Partial installation  
    else
        return 0  # Not installed
    fi
}

# Function to create Quadlet service files
create_quadlet_service() {
    log_message "INFO" "Creating Quadlet service configuration"
    
    # Create Quadlet directory if it doesn't exist
    check_directory "$QUADLET_DIR" "Quadlet configuration"
    
    # Determine which Quadlet configuration to use based on SSL settings
    local quadlet_source=""
    local quadlet_target="$QUADLET_DIR/birdnet-go.container"
    local network_target="$QUADLET_DIR/birdnet-go.network"
    
    # Copy network configuration
    if [ -f "Podman/quadlet/birdnet-go.network" ]; then
        cp "Podman/quadlet/birdnet-go.network" "$network_target"
        log_message "INFO" "Copied Quadlet network configuration"
    else
        # Create network configuration if source doesn't exist
        cat > "$network_target" << 'EOF'
[Unit]
Description=BirdNET-Go Bridge Network
Documentation=man:podman-systemd.unit(5)

[Network]
NetworkName=birdnet-bridge
Driver=bridge
IPAMDriver=host-local
Subnet=172.16.0.0/24
Gateway=172.16.0.1

[Install]
WantedBy=default.target
EOF
        log_message "INFO" "Created default Quadlet network configuration"
    fi
    
    # Determine SSL configuration
    local use_autotls=false
    if [ -f "$CONFIG_FILE" ]; then
        # Check if AutoTLS is enabled in config
        if grep -q "autotls:.*true" "$CONFIG_FILE" 2>/dev/null; then
            use_autotls=true
        fi
    fi
    
    # Choose the appropriate Quadlet template
    if [ "$use_autotls" = true ]; then
        if [ -f "Podman/quadlet/birdnet-go-autotls.container" ]; then
            quadlet_source="Podman/quadlet/birdnet-go-autotls.container"
            log_message "INFO" "Using AutoTLS Quadlet configuration"
        fi
    else
        if [ -f "Podman/quadlet/birdnet-go.container" ]; then
            quadlet_source="Podman/quadlet/birdnet-go.container"
            log_message "INFO" "Using standard Quadlet configuration"
        fi
    fi
    
    # Create Quadlet container configuration
    if [ -n "$quadlet_source" ] && [ -f "$quadlet_source" ]; then
        # Copy and customize the Quadlet file
        cp "$quadlet_source" "$quadlet_target"
        
        # Update image reference
        sed -i "s|ghcr.io/tphakala/birdnet-go:nightly|${BIRDNET_GO_IMAGE}|g" "$quadlet_target"
        
        # Update timezone if configured
        if [ -n "$CONFIGURED_TZ" ]; then
            sed -i "s|Environment=TZ=UTC|Environment=TZ=$CONFIGURED_TZ|g" "$quadlet_target"
        fi
        
        log_message "INFO" "Created Quadlet container configuration: $quadlet_target"
    else
        # Create default Quadlet configuration if source doesn't exist
        cat > "$quadlet_target" << EOF
[Unit]
Description=BirdNET-Go Container
After=network-online.target
Wants=network-online.target

[Container]
Image=${BIRDNET_GO_IMAGE}
ContainerName=birdnet-go
Volume=./config:/config
Volume=./data:/data
PublishPort=${WEB_PORT}:8080
Environment=TZ=${CONFIGURED_TZ:-UTC}
Environment=BIRDNET_UID=\$(id -u)
Environment=BIRDNET_GID=\$(id -g)
Device=/dev/snd:/dev/snd
Network=birdnet-bridge
Tmpfs=/config/hls:exec,size=50M,uid=\$(id -u),gid=\$(id -g),mode=0755

[Service]
Restart=always
TimeoutStartSec=900

[Install]
WantedBy=default.target
EOF
        log_message "INFO" "Created default Quadlet container configuration"
    fi
    
    # Reload systemd to recognize new Quadlet files
    systemctl --user daemon-reload
    log_command_result "systemctl --user daemon-reload" $? "Quadlet service reload"
    
    print_message "✅ Quadlet service configuration created" "$GREEN"
}

# Function to start Quadlet service
start_quadlet_service() {
    log_message "INFO" "Starting BirdNET-Go Quadlet service"
    
    # Enable the service
    if systemctl --user enable birdnet-go.service; then
        log_command_result "systemctl --user enable birdnet-go.service" $? "service enable"
        print_message "✅ BirdNET-Go service enabled" "$GREEN"
    else
        log_message "ERROR" "Failed to enable BirdNET-Go service"
        print_message "❌ Failed to enable BirdNET-Go service" "$RED"
        return 1
    fi
    
    # Start the service
    if systemctl --user start birdnet-go.service; then
        log_command_result "systemctl --user start birdnet-go.service" $? "service start"
        print_message "✅ BirdNET-Go service started" "$GREEN"
    else
        log_message "ERROR" "Failed to start BirdNET-Go service"
        print_message "❌ Failed to start BirdNET-Go service" "$RED"
        
        # Show service status for debugging
        print_message "Service status:" "$YELLOW"
        systemctl --user status birdnet-go.service --no-pager -l
        return 1
    fi
    
    return 0
}

# Function to update paths in config file for Podman
update_paths_in_config() {
    if [ ! -f "$CONFIG_FILE" ]; then
        log_message "INFO" "No existing config file to update paths"
        return 0
    fi
    
    log_message "INFO" "Updating paths in config file for Podman"
    
    # Update paths to use container paths (same as Docker version)
    # Most path updates are the same since both Docker and Podman use same container filesystem
    
    # Backup config before modification
    cp "$CONFIG_FILE" "${CONFIG_FILE}.backup-$(date +%s)" 2>/dev/null || true
    
    # Update specific paths that might differ
    # Note: Most paths should already be correct from Docker version
    # This is mainly for consistency and future-proofing
    
    log_message "INFO" "Config path updates completed"
    return 0
}

# Function to create default configuration
create_default_config() {
    if [ -f "$CONFIG_FILE" ]; then
        log_message "INFO" "Config file already exists, skipping default config creation"
        return 0
    fi
    
    log_message "INFO" "Creating default configuration file"
    
    # Get IP address for config
    local ip_address
    ip_address=$(get_ip_address)
    
    # Set timezone
    local timezone
    timezone=$(timedatectl show -p Timezone --value 2>/dev/null || echo "UTC")
    CONFIGURED_TZ="$timezone"
    
    # Create default config
    cat > "$CONFIG_FILE" << EOF
# BirdNET-Go Configuration (Podman Edition)
# Generated by podman-install.sh on $(date)

# Web interface settings
webserver:
  host: "0.0.0.0"
  port: 8080
  autotls: false
  
# Audio input settings  
audio:
  source: "default"
  channels: 1
  samplerate: 22050
  bitdepth: 16
  
# Analysis settings
analysis:
  locale: "en"
  latitude: 0.0
  longitude: 0.0
  sensitivity: 1.0
  overlap: 0.0
  
# Data storage
data:
  retention:
    days: 7
    
# System settings
system:
  timezone: "$timezone"
  
# Logging
logging:
  level: "info"
EOF
    
    log_message "INFO" "Default configuration created: $CONFIG_FILE"
    print_message "✅ Default configuration created" "$GREEN"
}

# Function to perform fresh installation
perform_fresh_installation() {
    log_message "INFO" "=== Starting Fresh Podman Installation ==="
    
    print_message "✨ Performing fresh BirdNET-Go Podman installation..." "$GREEN"
    
    # Create directories
    check_directory "$CONFIG_DIR" "Configuration"
    check_directory "$DATA_DIR" "Data"
    
    # Pull container image
    pull_podman_image
    
    # Create default configuration
    create_default_config
    
    # Create and start Quadlet service
    create_quadlet_service
    start_quadlet_service
    
    # Wait a moment for service to start
    sleep 5
    
    # Verify installation
    if systemctl --user is-active --quiet birdnet-go.service; then
        log_message "INFO" "Fresh installation completed successfully"
        print_message "✅ Fresh installation completed successfully!" "$GREEN"
        show_success_message
        send_telemetry_event "success" "Fresh Podman installation completed" "info" "version=$BIRDNET_GO_VERSION"
    else
        log_message "ERROR" "Fresh installation failed - service not running"
        print_message "❌ Fresh installation failed - service not running" "$RED"
        print_message "Check service status: systemctl --user status birdnet-go.service" "$YELLOW"
        send_telemetry_event "error" "Fresh Podman installation failed" "error" "version=$BIRDNET_GO_VERSION"
        exit 1
    fi
}

# Function to show success message
show_success_message() {
    local ip_address
    ip_address=$(get_ip_address)
    
    print_message "" "$GREEN"
    
    # Show transition success message if applicable
    if [ -f "$CONFIG_DIR/.docker-to-podman-transition" ]; then
        print_message "✨✨✨ Docker to Podman Transition Complete! ✨✨✨" "$GREEN"
        print_message "" "$GREEN"
        print_message "🚀 Your BirdNET-Go has been successfully migrated from Docker to Podman!" "$GREEN"
        
        local transition_date
        transition_date=$(grep "transition_date=" "$CONFIG_DIR/.docker-to-podman-transition" 2>/dev/null | cut -d'=' -f2- || echo "unknown")
        print_message "📅 Migration completed: $transition_date" "$GRAY"
        print_message "✅ All your data and configuration have been preserved" "$GREEN"
        print_message "📊 Now running with improved Podman + Quadlet integration" "$GREEN"
    else
        print_message "✨✨✨ BirdNET-Go Podman Installation Complete! ✨✨✨" "$GREEN"
        print_message "" "$GREEN"
        print_message "Your BirdNET-Go installation is now running with Podman!" "$GREEN"
    fi
    
    print_message "" "$GREEN"
    
    if [ -n "$ip_address" ]; then
        print_message "🔗 Web Interface: http://$ip_address:${WEB_PORT:-8080}" "$YELLOW"
    else
        print_message "🔗 Web Interface: http://localhost:${WEB_PORT:-8080}" "$YELLOW"
    fi
    
    if check_mdns; then
        local hostname
        hostname=$(hostname -s)
        print_message "🔗 mDNS Address: http://$hostname.local:${WEB_PORT:-8080}" "$YELLOW"
    fi
    
    print_message "" "$GREEN"
    print_message "🔧 Useful Commands:" "$YELLOW"
    print_message "  • Check status:   systemctl --user status birdnet-go.service" "$NC"
    print_message "  • View logs:      systemctl --user logs birdnet-go.service" "$NC"
    print_message "  • Stop service:   systemctl --user stop birdnet-go.service" "$NC"
    print_message "  • Start service:  systemctl --user start birdnet-go.service" "$NC"
    print_message "  • Restart:        systemctl --user restart birdnet-go.service" "$NC"
    print_message "  • Container info: podman ps" "$NC"
    print_message "" "$GREEN"
    print_message "📋 Configuration: $CONFIG_FILE" "$NC"
    print_message "📁 Data Directory: $DATA_DIR" "$NC"
    print_message "🔧 Quadlet Config:  $QUADLET_DIR" "$NC"
    print_message "" "$GREEN"
}

# Main installation workflow
run_installation() {
    log_message "INFO" "=== Starting BirdNET-Go Podman Installation Workflow ==="
    
    # Load telemetry configuration
    load_telemetry_config
    
    # Send installation start event
    send_telemetry_event "info" "Podman installation started" "info" "version=$BIRDNET_GO_VERSION"
    
    # Check current installation status
    if check_birdnet_installation; then
        local install_status=$?
        case $install_status in
            2)
                log_message "INFO" "Full BirdNET-Go installation detected"
                print_message "ℹ️ BirdNET-Go appears to already be installed with Podman" "$YELLOW"
                print_message "Service status: $(systemctl --user is-active birdnet-go.service 2>/dev/null || echo 'unknown')" "$GRAY"
                
                # Ask user what to do
                print_message "" "$NC"
                print_message "What would you like to do?" "$YELLOW"
                print_message "1) Update/reinstall BirdNET-Go" "$NC"
                print_message "2) Check current status" "$NC"
                print_message "3) Exit" "$NC"
                print_message "" "$NC"
                
                read -p "Please select an option (1-3): " choice
                
                case $choice in
                    1)
                        log_message "INFO" "User selected update/reinstall"
                        print_message "⚙️ Proceeding with update/reinstall..." "$GREEN"
                        perform_fresh_installation
                        ;;
                    2)
                        log_message "INFO" "User requested status check"
                        show_current_status
                        ;;
                    3|"")  # Default to exit if user presses enter
                        log_message "INFO" "User chose to exit"
                        print_message "Installation cancelled." "$YELLOW"
                        exit 0
                        ;;
                    *)
                        log_message "ERROR" "Invalid user selection: $choice"
                        print_message "❌ Invalid selection. Exiting." "$RED"
                        exit 1
                        ;;
                esac
                ;;
            1)
                log_message "INFO" "Partial BirdNET-Go installation detected"
                print_message "⚠️ Partial BirdNET-Go installation detected" "$YELLOW"
                print_message "Proceeding with fresh installation to fix any issues..." "$YELLOW"
                perform_fresh_installation
                ;;
            0)
                log_message "INFO" "No existing BirdNET-Go installation found"
                print_message "✨ No existing installation found. Performing fresh installation..." "$GREEN"
                FRESH_INSTALL="true"
                perform_fresh_installation
                ;;
        esac
    else
        log_message "INFO" "No existing BirdNET-Go installation found"
        print_message "✨ No existing installation found. Performing fresh installation..." "$GREEN"
        FRESH_INSTALL="true"
        perform_fresh_installation
    fi
    
    log_message "INFO" "=== BirdNET-Go Podman Installation Workflow Completed ==="
}

# Function to show current status
show_current_status() {
    print_message "" "$NC"
    print_message "📊 Current BirdNET-Go Status (Podman)" "$GREEN"
    print_message "=======================================" "$GRAY"
    
    # Service status
    if systemctl --user is-active --quiet birdnet-go.service; then
        print_message "✅ Service Status: Running" "$GREEN"
    else
        print_message "❌ Service Status: Not running" "$RED"
    fi
    
    # Service enabled status
    if systemctl --user is-enabled --quiet birdnet-go.service; then
        print_message "✅ Service Enabled: Yes" "$GREEN"
    else
        print_message "❌ Service Enabled: No" "$RED"
    fi
    
    # Container status
    if command_exists podman; then
        local container_status
        if safe_podman ps --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.Status}}" | grep -q "Up"; then
            print_message "✅ Container Status: Running" "$GREEN"
        else
            print_message "❌ Container Status: Not running" "$RED"
        fi
        
        # Image info
        if safe_podman images --format "{{.Repository}}:{{.Tag}}" | grep -q "birdnet-go"; then
            local image_info
            image_info=$(safe_podman images --format "table {{.Repository}}:{{.Tag}}\t{{.Created}}\t{{.Size}}" | grep birdnet-go | head -1)
            print_message "✅ Container Image: $image_info" "$GREEN"
        else
            print_message "❌ Container Image: Not found" "$RED"
        fi
    else
        print_message "❌ Podman: Not available" "$RED"
    fi
    
    # Configuration
    if [ -f "$CONFIG_FILE" ]; then
        print_message "✅ Configuration: $CONFIG_FILE" "$GREEN"
    else
        print_message "❌ Configuration: Not found" "$RED"
    fi
    
    # Directories
    if [ -d "$CONFIG_DIR" ]; then
        print_message "✅ Config Directory: $CONFIG_DIR" "$GREEN"
    else
        print_message "❌ Config Directory: Not found" "$RED"
    fi
    
    if [ -d "$DATA_DIR" ]; then
        print_message "✅ Data Directory: $DATA_DIR" "$GREEN"
    else
        print_message "❌ Data Directory: Not found" "$RED"
    fi
    
    # Quadlet configuration
    if [ -f "$QUADLET_DIR/birdnet-go.container" ]; then
        print_message "✅ Quadlet Config: $QUADLET_DIR/birdnet-go.container" "$GREEN"
    else
        print_message "❌ Quadlet Config: Not found" "$RED"
    fi
    
    print_message "" "$NC"
    
    # Show web interface URLs if service is running
    if systemctl --user is-active --quiet birdnet-go.service; then
        local ip_address
        ip_address=$(get_ip_address)
        
        print_message "🔗 Web Interface URLs:" "$YELLOW"
        if [ -n "$ip_address" ]; then
            print_message "  • http://$ip_address:${WEB_PORT:-8080}" "$NC"
        fi
        print_message "  • http://localhost:${WEB_PORT:-8080}" "$NC"
        
        if check_mdns; then
            local hostname
            hostname=$(hostname -s)
            print_message "  • http://$hostname.local:${WEB_PORT:-8080}" "$NC"
        fi
        
        print_message "" "$NC"
    fi
    
    # Show transition info if available
    if [ -f "$CONFIG_DIR/.docker-to-podman-transition" ]; then
        local transition_date
        transition_date=$(grep "transition_date=" "$CONFIG_DIR/.docker-to-podman-transition" 2>/dev/null | cut -d'=' -f2- || echo "unknown")
        print_message "🔄 Transitioned from Docker: $transition_date" "$GRAY"
        print_message "" "$NC"
    fi
    
    # Show useful commands
    print_message "🔧 Useful Commands:" "$YELLOW"
    print_message "  • Check logs:      systemctl --user logs birdnet-go.service" "$NC"
    print_message "  • Restart service: systemctl --user restart birdnet-go.service" "$NC"
    print_message "  • Stop service:    systemctl --user stop birdnet-go.service" "$NC"
    print_message "  • Container info:  podman ps" "$NC"
    
    # Show transition commands if Docker is still present
    if command_exists docker; then
        print_message "  • Check old Docker: docker ps -a | grep birdnet" "$GRAY"
    fi
    
    print_message "" "$NC"
}

# =============================================================================
# MAIN EXECUTION
# =============================================================================

print_message "🚀 System checks completed successfully!" "$GREEN"
print_message "Ready to proceed with BirdNET-Go Podman installation\n" "$YELLOW"

# Run the main installation workflow
run_installation

# Final cleanup
cleanup_temp_files

print_message "" "$GREEN"
print_message "Installation script completed." "$GREEN"
print_message "Thank you for using BirdNET-Go with Podman! 🚀" "$GREEN"