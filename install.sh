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
|____/|_|_|  \__,_|_| \_|_____| |_|    \____|\___/ 
EOF


# Default version (will be set by parse_arguments function)
BIRDNET_GO_VERSION="nightly"
BIRDNET_GO_IMAGE=""

# Silent mode for non-interactive installation (set via --silent flag)
SILENT_MODE="false"

# Force root mode - allow running as root despite warnings (set via --force-root flag)
FORCE_ROOT="false"

# Flag to track if Docker image was changed during update/rollback
IMAGE_CHANGED="false"

# Logging configuration
LOG_DIR="$HOME/birdnet-go-app/data/logs"
# Generate timestamped log file name: install-YYYYMMDD-HHMMSS.log
LOG_TIMESTAMP=$(date '+%Y%m%d-%H%M%S')
LOG_FILE="$LOG_DIR/install-${LOG_TIMESTAMP}.log"

# Logging system will be initialized after function definitions

# Version management configuration
MAX_CONFIG_BACKUPS=10
VERSION_HISTORY_FILE="$LOG_DIR/version_history.log"
CONFIG_BACKUP_PREFIX="config-backup-"

# Set secure umask for file creation
umask 077

# Telemetry diagnostic truncation limits
MAX_ERROR_LENGTH=500
MAX_LOG_LENGTH=1000
MAX_FLAGS_LENGTH=300

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
    # Example: 20240826-134817|sha256:abc123...|config-backup-20240826-134817.yaml|ghcr.io/tphakala/birdnet-go:nightly|pre-update
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
        log_message "INFO" "=== BirdNET-Go Installation/Update Session Started ==="
        log_message "INFO" "Log file: $(basename "$LOG_FILE")"
        log_message "INFO" "Script version: $(grep -o 'script_version.*[0-9]\+\.[0-9]\+\.[0-9]\+' "$0" | head -1 || echo 'unknown')"
        log_message "INFO" "User: $USER (UID: $(id -u)), Working directory: $(pwd)"
        log_message "INFO" "System: $(uname -a)"
        
        # Log initial system state
        log_system_resources "initial"
        log_network_state "initial"
        
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
    | sed -E 's#(password|passwd|pwd|token|secret|api[_-]?key)["'"'"']?\s*[:=]\s*[^"'"'"'[:space:]]+#\1: ***#Ig'
}

# Prevent sed injection from user-supplied values (RTSP URLs, device names, passwords).
sed_escape_replacement() {
    printf '%s' "$1" | tr -d '\n\r' | sed -e 's/[\\|&]/\\&/g'
}

# Escape a string for use on the PATTERN (left-hand) side of a sed s||| command using '|'
# as the delimiter. Escapes BRE/ERE metacharacters and the '|' delimiter so the value is
# matched literally (e.g. a filesystem path whose '.' must not act as "any character").
sed_escape_pattern() {
    printf '%s' "$1" | tr -d '\n\r' | sed -e 's/[][\\.^$*|]/\\&/g'
}

# Safely set a scalar VALUE for KEY inside the top-level YAML block BLOCK of the config
# file (default $CONFIG_FILE). The substitution is scoped with a sed address range to the
# block, so it never touches an identically named key in another block (for example
# webserver.port vs mysql.port vs the rtsp port). BLOCK and KEY MUST be literal YAML
# identifiers from the caller (never user input); VALUE is the only untrusted part and is
# escaped via sed_escape_replacement, with '|' as the sed delimiter to match that escaping.
# Any inline comment on the edited line is dropped. Re-run-safe: matches the current value
# regardless of what it is. No-op if BLOCK or KEY is absent.
set_config_value() {
    local block="$1"
    local key="$2"
    local value="$3"
    local file="${4:-$CONFIG_FILE}"
    [ -f "$file" ] || return 1
    local escaped
    escaped=$(sed_escape_replacement "$value")
    # Anchor to exactly two leading spaces (the indentation of a direct child of a top-level
    # block) so a same-named key in a deeper nested block is never overwritten.
    sed -i -E "/^${block}:/,/^[A-Za-z0-9_]/ s|^([[:space:]]{2}${key}:[[:space:]]*).*|\\1${escaped}|" -- "$file"
}

# Re-run-safe update of the first audio source's device id and friendly name, scoped to the
# realtime.audio.sources block (4-space indent) so it works whether the config is pristine
# ("sysdefault" / "Sound Card 1") or already reconfigured (Forgejo #729). The old code only
# matched the literal template values and silently no-opped on a re-run. DEVICE and NAME are
# escaped for sed. Assumes the single-source layout the installer manages.
set_first_audio_source() {
    local device="$1"
    local name="$2"
    local file="${3:-$CONFIG_FILE}"
    [ -f "$file" ] || return 1
    # If there is no active (uncommented) source device line to edit (e.g. the config is
    # currently RTSP-only with the sound-card source commented out), report a no-op so the
    # caller can warn instead of the sed silently changing nothing while exit 0 looks like
    # success.
    if ! sed -n '/^[[:space:]]\{4\}sources:/,/^[[:space:]]\{4\}[A-Za-z]/p' "$file" | grep -qE '^[[:space:]]+(-[[:space:]]+)?device:[[:space:]]'; then
        return 1
    fi
    # Edit ONLY the first source item, not every entry in the sources block, so a
    # multi-source config (e.g. extra sources added via the web UI) is not clobbered. The
    # item boundary is the list dash (`- `), and the name/device fields are matched with an
    # optional leading dash so the edit works regardless of which field comes first.
    # device/name are passed as literal awk variables, so sed/regex metacharacters in a
    # device name need no escaping here. Write to a temp file then copy back over the
    # original so its ownership and permissions are preserved.
    if awk -v device="$device" -v name="$name" '
        /^    sources:/ { in_sources=1; print; next }
        in_sources && /^    [A-Za-z]/ { in_sources=0 }
        in_sources && /^[[:space:]]*-[[:space:]]/ { item++ }
        in_sources && item == 1 && !name_done && /^[[:space:]]*(-[[:space:]]+)?name:/ {
            match($0, /^[[:space:]]*(-[[:space:]]+)?name:[[:space:]]*/)
            print substr($0, 1, RLENGTH) "\"" name "\""
            name_done = 1
            next
        }
        in_sources && item == 1 && !device_done && /^[[:space:]]*(-[[:space:]]+)?device:/ {
            match($0, /^[[:space:]]*(-[[:space:]]+)?device:[[:space:]]*/)
            print substr($0, 1, RLENGTH) "\"" device "\""
            device_done = 1
            next
        }
        { print }
    ' "$file" > "${file}.tmp"; then
        cat "${file}.tmp" > "$file"
        rm -f "${file}.tmp"
    else
        rm -f "${file}.tmp"
        return 1
    fi
}

# Prevent sed injection from user-supplied lat/lon and port values.
validate_numeric() {
    local value="$1"
    [[ "$value" =~ ^-?[0-9]+(\.[0-9]+)?$ ]]
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
    local docker_space=""
    local tmp_space=""
    
    if [ -d "$CONFIG_DIR" ] || [ -d "$(dirname "$CONFIG_DIR")" ]; then
        config_dir_space=$(df -h "$(dirname "$CONFIG_DIR")" 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
        log_message "INFO" "Config directory disk space: $config_dir_space"
    fi
    
    if [ -d "$DATA_DIR" ] || [ -d "$(dirname "$DATA_DIR")" ]; then
        data_dir_space=$(df -h "$(dirname "$DATA_DIR")" 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
        log_message "INFO" "Data directory disk space: $data_dir_space"
    fi
    
    if [ -d "/var/lib/docker" ]; then
        docker_space=$(df -h /var/lib/docker 2>/dev/null | awk 'NR==2 {print "Available: " $4 ", Used: " $5}')
        log_message "INFO" "Docker directory disk space: $docker_space"
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

# Function to calculate and log config file hash
log_config_hash() {
    local context="${1:-unknown}"
    
    if [ -f "$CONFIG_FILE" ]; then
        local config_hash=$(sha256sum "$CONFIG_FILE" 2>/dev/null | cut -d' ' -f1)
        local config_size=$(stat -f%z "$CONFIG_FILE" 2>/dev/null || stat -c%s "$CONFIG_FILE" 2>/dev/null)
        log_message "INFO" "Config file hash ($context): $config_hash (size: ${config_size} bytes)"
        echo "$config_hash"
    else
        log_message "WARN" "Config file not found for hash calculation ($context)"
        echo ""
    fi
}

# Function to detect and log process type
detect_process_type() {
    local installation_type="$1"
    local preserved_data="$2"
    local fresh_install="$3"
    
    if [ "$fresh_install" = "true" ]; then
        if [ "$preserved_data" = "true" ]; then
            echo "FRESH_INSTALL_WITH_DATA"
            log_message "INFO" "Process type: Fresh installation (preserving existing data)"
        else
            echo "FRESH_INSTALL"
            log_message "INFO" "Process type: Fresh installation (clean install)"
        fi
    elif [ "$installation_type" = "full" ]; then
        echo "UPDATE"
        log_message "INFO" "Process type: Update (existing systemd service installation)"
    elif [ "$installation_type" = "docker" ]; then
        echo "MIGRATION"
        log_message "INFO" "Process type: Migration (Docker-only to systemd service)"
    elif [ "$preserved_data" = "true" ]; then
        echo "REINSTALL"
        log_message "INFO" "Process type: Reinstall (using preserved data)"
    else
        echo "INSTALL"
        log_message "INFO" "Process type: New installation"
    fi
}

# Function to log Docker container and image state
log_docker_state() {
    local context="${1:-general}"
    
    if ! command_exists docker; then
        log_message "INFO" "Docker state ($context): Docker not installed"
        return
    fi
    
    log_message "INFO" "=== Docker State ($context) ==="
    
    # Docker service status
    if command_exists systemctl; then
        local docker_status="unknown"
        if systemctl is-active --quiet docker; then
            docker_status="active"
        elif systemctl is-failed --quiet docker; then
            docker_status="failed"  
        else
            docker_status="inactive"
        fi
        log_message "INFO" "Docker service status: $docker_status"
    fi
    
    # BirdNET-Go containers
    local running_containers
    local stopped_containers
    local all_containers
    
    running_containers=$(safe_docker ps --filter "name=birdnet-go" --format "{{.ID}} {{.Image}} {{.Status}}" 2>/dev/null | wc -l)
    all_containers=$(safe_docker ps -a --filter "name=birdnet-go" --format "{{.ID}} {{.Image}} {{.Status}}" 2>/dev/null | wc -l)
    stopped_containers=$((all_containers - running_containers))
    
    log_message "INFO" "BirdNET-Go containers: $running_containers running, $stopped_containers stopped, $all_containers total"
    
    # List specific containers with details
    if [ "$all_containers" -gt 0 ]; then
        safe_docker ps -a --filter "name=birdnet-go" --format "{{.ID}} {{.Image}} {{.Status}}" 2>/dev/null | while read -r line; do
            [ -n "$line" ] && log_message "INFO" "Container: $line"
        done
    fi
    
    # BirdNET-Go images
    local birdnet_images
    birdnet_images=$(safe_docker images --filter "reference=*birdnet-go*" --format "{{.Repository}}:{{.Tag}} {{.Size}} {{.CreatedAt}}" 2>/dev/null)
    
    if [ -n "$birdnet_images" ]; then
        log_message "INFO" "BirdNET-Go images found:"
        echo "$birdnet_images" | while read -r line; do
            [ -n "$line" ] && log_message "INFO" "Image: $line"
        done
    else
        log_message "INFO" "No BirdNET-Go images found"
    fi
}

# Function to log systemd service state  
log_service_state() {
    local context="${1:-general}"
    
    if ! command_exists systemctl; then
        log_message "INFO" "Service state ($context): systemd not available"
        return
    fi
    
    log_message "INFO" "=== Systemd Service State ($context) ==="
    
    # Check if service unit file exists
    local service_file_exists="false"
    if [ -f "/etc/systemd/system/birdnet-go.service" ]; then
        service_file_exists="true"
        local service_file_size=$(stat -c%s "/etc/systemd/system/birdnet-go.service" 2>/dev/null)
        local service_file_hash=$(sha256sum "/etc/systemd/system/birdnet-go.service" 2>/dev/null | cut -d' ' -f1)
        log_message "INFO" "Service file exists: size ${service_file_size} bytes, hash: $service_file_hash"
    else
        log_message "INFO" "Service file does not exist"
    fi
    
    if [ "$service_file_exists" = "true" ]; then
        # Service status
        local service_status="unknown"
        if systemctl is-active --quiet birdnet-go.service; then
            service_status="active"
        elif systemctl is-failed --quiet birdnet-go.service; then
            service_status="failed"
        else
            service_status="inactive"
        fi
        
        local enabled_status="unknown"
        if systemctl is-enabled --quiet birdnet-go.service; then
            enabled_status="enabled"
        else
            enabled_status="disabled"
        fi
        
        log_message "INFO" "Service status: $service_status, enabled: $enabled_status"
        
        # Get last few journal entries for the service
        local journal_entries=$(journalctl -u birdnet-go.service -n 3 --no-pager --output=cat 2>/dev/null | tail -n 3)
        if [ -n "$journal_entries" ]; then
            log_message "INFO" "Recent service log entries:"
            echo "$journal_entries" | while IFS= read -r line; do
                [ -n "$line" ] && log_message "INFO" "  $line"
            done
        fi
    fi
}

# Function to log network connectivity state
log_network_state() {
    local context="${1:-general}"
    
    log_message "INFO" "=== Network Connectivity ($context) ==="
    
    # Test basic connectivity (without logging errors to console)
    if ping -c 1 -W 2 8.8.8.8 >/dev/null 2>&1; then
        log_message "INFO" "Basic connectivity: OK (ping to 8.8.8.8 successful)"
    else
        log_message "WARN" "Basic connectivity: FAILED (ping to 8.8.8.8 failed)"
    fi
    
    # Test HTTPS connectivity to key endpoints
    if command_exists curl; then
        local github_status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "https://github.com" 2>/dev/null)
        local ghcr_status=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "https://ghcr.io/v2/" 2>/dev/null)
        
        log_message "INFO" "GitHub connectivity: HTTP $github_status"
        log_message "INFO" "GitHub Container Registry: HTTP $ghcr_status"
    else
        log_message "INFO" "curl not available for HTTPS connectivity test"
    fi
}

# Function to log comprehensive session information after installation type detection
log_enhanced_session_info() {
    local installation_type="$1"
    local preserved_data="$2"
    local fresh_install="$3"
    
    log_message "INFO" "=== Enhanced Session Information ==="
    
    # Detect and log process type
    local process_type
    process_type=$(detect_process_type "$installation_type" "$preserved_data" "$fresh_install")
    log_message "INFO" "Process type detected: $process_type"
    
    # Log Docker and service state
    log_docker_state "pre-process"
    log_service_state "pre-process"
    
    # Log config file hash if it exists (for updates/reinstalls)
    if [ -f "$CONFIG_FILE" ]; then
        log_config_hash "pre-process"
    fi
    
    # Log key directory states
    log_message "INFO" "=== Directory State ==="
    log_message "INFO" "CONFIG_DIR exists: $([ -d "$CONFIG_DIR" ] && echo "yes" || echo "no")"
    log_message "INFO" "DATA_DIR exists: $([ -d "$DATA_DIR" ] && echo "yes" || echo "no")"
    log_message "INFO" "CONFIG_FILE exists: $([ -f "$CONFIG_FILE" ] && echo "yes" || echo "no")"
    
    # Count existing audio clips if data directory exists
    if [ -d "$DATA_DIR/clips" ]; then
        local clip_count=$(find "$DATA_DIR/clips" -type f -name "*.wav" -o -name "*.mp3" -o -name "*.flac" -o -name "*.aac" -o -name "*.opus" 2>/dev/null | wc -l)
        local clips_size=$(du -sh "$DATA_DIR/clips" 2>/dev/null | cut -f1)
        log_message "INFO" "Existing audio clips: $clip_count files, total size: ${clips_size:-unknown}"
    fi
}

# Function to capture current Docker image hash and details
capture_current_image_hash() {
    local context="${1:-unknown}"
    
    if ! command_exists docker; then
        log_message "WARN" "Cannot capture image hash: Docker not available"
        return 1
    fi
    
    log_message "INFO" "=== Capturing Current Image State ($context) ==="
    
    # Try BIRDNET_GO_IMAGE environment variable first as primary target
    local current_image=""
    local image_hash=""
    local image_tag=""
    
    # Check if BIRDNET_GO_IMAGE is set and verify it exists locally
    if [ -n "$BIRDNET_GO_IMAGE" ]; then
        # Try to get canonical image ID via docker inspect
        local canonical_id
        canonical_id=$(safe_docker inspect --format '{{.Id}}' "$BIRDNET_GO_IMAGE" 2>/dev/null)
        
        if [ -n "$canonical_id" ]; then
            current_image="$BIRDNET_GO_IMAGE"
            image_hash="$canonical_id"
            # Strip sha256: prefix and use first 12 chars for display
            local normalized_id="${canonical_id#sha256:}"
            log_message "INFO" "Using BIRDNET_GO_IMAGE environment variable: $current_image (ID: ${normalized_id:0:12}...)"
        elif safe_docker images --format "{{.Repository}}:{{.Tag}}" | grep -Fxq "${BIRDNET_GO_IMAGE}" 2>/dev/null; then
            # Fall back to checking if image exists in local images (exact match)
            current_image="$BIRDNET_GO_IMAGE"
            log_message "INFO" "Found BIRDNET_GO_IMAGE in local images: $current_image"
        else
            log_message "WARN" "BIRDNET_GO_IMAGE ($BIRDNET_GO_IMAGE) not found locally, falling back to container detection"
        fi
    fi
    
    # Fall back to existing container/image detection if BIRDNET_GO_IMAGE validation failed
    if [ -z "$current_image" ]; then
        # Check for running BirdNET-Go container
        local running_container=$(safe_docker ps --filter "name=birdnet-go" --format "{{.Image}}" 2>/dev/null | head -1)
        
        if [ -n "$running_container" ]; then
            current_image="$running_container"
            log_message "INFO" "Found running container using image: $current_image"
        else
            # Check for any BirdNET-Go containers (stopped)
            local any_container=$(safe_docker ps -a --filter "name=birdnet-go" --format "{{.Image}}" 2>/dev/null | head -1)
            if [ -n "$any_container" ]; then
                current_image="$any_container"
                log_message "INFO" "Found stopped container using image: $current_image"
            else
                # Fall back to checking for local BirdNET-Go images
                current_image=$(safe_docker images --filter "reference=*birdnet-go*" --format "{{.Repository}}:{{.Tag}}" 2>/dev/null | head -1)
                if [ -n "$current_image" ]; then
                    log_message "INFO" "Found local image: $current_image"
                else
                    log_message "WARN" "No BirdNET-Go images found"
                    return 1
                fi
            fi
        fi
    fi
    
    # Get image hash and details
    if [ -n "$current_image" ]; then
        # Try to get canonical ID first, fall back to image hash
        if [ -z "$image_hash" ]; then
            # Try docker inspect first for canonical ID
            image_hash=$(safe_docker inspect --format '{{.Id}}' "$current_image" 2>/dev/null)
            
            # Fall back to docker images if inspect fails
            if [ -z "$image_hash" ]; then
                image_hash=$(safe_docker images --no-trunc --format "{{.ID}}" "$current_image" 2>/dev/null | head -1)
            fi
        fi
        
        image_tag="${current_image}"
        
        if [ -n "$image_hash" ]; then
            log_message "INFO" "Current image: $image_tag"
            # Strip sha256: prefix and use first 12 chars for display
            local normalized_hash="${image_hash#sha256:}"
            log_message "INFO" "Image hash: ${normalized_hash:0:12}..."
            
            # Generate fresh timestamp for this capture
            local capture_timestamp
            capture_timestamp=$(date '+%Y%m%d-%H%M%S')
            
            # Store in version history file format: timestamp|image_hash|config_backup|image_tag|context
            local version_entry="${capture_timestamp}|${image_hash}|none|${image_tag}|${context}"
            append_version_history "$version_entry"
            
            # Return the hash for use by calling functions
            echo "$image_hash"
            return 0
        else
            log_message "ERROR" "Failed to get image hash for: $current_image"
            return 1
        fi
    else
        log_message "WARN" "No current image to capture"
        return 1
    fi
}

# Function to create config backup with version association
backup_config_with_version() {
    local context="${1:-backup}"
    local image_hash="${2:-}"
    local image_tag="${3:-unknown}"
    
    if [ ! -f "$CONFIG_FILE" ]; then
        log_message "WARN" "No config file to backup: $CONFIG_FILE"
        return 1
    fi
    
    # Rate limiting check to prevent rapid backup operations (except for critical contexts)
    if [ "$context" != "pre-update" ] && [ "$context" != "REVERT" ]; then
        if [ -f "$LOG_DIR/.last_backup_time" ]; then
            local last_backup_time=$(cat "$LOG_DIR/.last_backup_time" 2>/dev/null || echo 0)
            local current_time=$(date +%s)
            if [ $((current_time - last_backup_time)) -lt 60 ]; then
                log_message "WARN" "Backup throttled: too frequent (wait $((60 - (current_time - last_backup_time)))s)"
                return 1
            fi
        fi
        date +%s > "$LOG_DIR/.last_backup_time"
    fi
    
    log_message "INFO" "=== Creating Config Backup ($context) ==="
    
    # Create backup filename with a per-action timestamp
    local backup_timestamp
    backup_timestamp=$(date '+%Y%m%d-%H%M%S')
    local backup_filename="${CONFIG_BACKUP_PREFIX}${backup_timestamp}.yaml"
    local backup_path="$CONFIG_DIR/$backup_filename"
    
    # Create backup
    if cp "$CONFIG_FILE" "$backup_path" 2>/dev/null; then
        log_message "INFO" "Config backup created: $backup_filename"
        
        # Calculate backup hash for verification
        local backup_hash=$(sha256sum "$backup_path" 2>/dev/null | cut -d' ' -f1)
        log_message "INFO" "Config backup hash: ${backup_hash:0:16}..."
        
        # Update version history with backup info
        if [ -n "$image_hash" ]; then
            # Update only the most recent entry matching this image_hash with empty backup
            local temp_file
            temp_file=$(mktemp /tmp/version_history_XXXXXX.tmp)
            if [ -f "$VERSION_HISTORY_FILE" ]; then
                # Store lines and find last matching row index, then update only that row
                awk -F'|' -v OFS='|' -v ih="$image_hash" -v bf="$backup_filename" '
                  { lines[NR]=$0 }
                  $2==ih && $3=="none" { idx=NR }
                  END {
                    for (i=1; i<=NR; i++) {
                      if (i==idx) {
                        split(lines[i], a, "|"); 
                        a[3]=bf;
                        print a[1] OFS a[2] OFS a[3] OFS a[4] OFS a[5]
                      } else {
                        print lines[i]
                      }
                    }
                  }
                ' "$VERSION_HISTORY_FILE" > "$temp_file"
                mv "$temp_file" "$VERSION_HISTORY_FILE"
                rm -f "$temp_file" 2>/dev/null  # Clean up in case mv failed
            fi
            log_message "INFO" "Version history updated with config backup association"
        fi
        
        # Clean up old backups
        cleanup_old_backups
        
        echo "$backup_filename"
        return 0
    else
        log_message "ERROR" "Failed to create config backup"
        return 1
    fi
}

# Function to cleanup old config backups
cleanup_old_backups() {
    if [ ! -d "$CONFIG_DIR" ]; then
        return 0
    fi
    
    log_message "INFO" "Checking config backup cleanup (max: $MAX_CONFIG_BACKUPS)"
    
    # Count existing backup files
    local backup_count
    backup_count=$(find "$CONFIG_DIR" -name "${CONFIG_BACKUP_PREFIX}*.yaml" 2>/dev/null | wc -l)
    
    if [ "$backup_count" -le "$MAX_CONFIG_BACKUPS" ]; then
        log_message "INFO" "Backup count ($backup_count) within limit ($MAX_CONFIG_BACKUPS)"
        return 0
    fi
    
    log_message "INFO" "Cleaning up old backups: $backup_count > $MAX_CONFIG_BACKUPS"
    
    # Remove oldest backups beyond the limit
    local to_remove=$((backup_count - MAX_CONFIG_BACKUPS))
    find "$CONFIG_DIR" -type f -name "${CONFIG_BACKUP_PREFIX}*.yaml" -printf '%T@ %p\0' 2>/dev/null \
      | sort -z -n \
      | head -z -n "$to_remove" \
      | awk -v RS='\0' -v ORS='\0' '{ $1=""; sub(/^ /,""); print }' \
      | while IFS= read -r -d '' old_backup; do
            if rm -f "$old_backup" 2>/dev/null; then
                log_message "INFO" "Removed old backup: $(basename "$old_backup")"
                
                # Remove from version history too
                local backup_name=$(basename "$old_backup")
                if [ -f "$VERSION_HISTORY_FILE" ]; then
                    local cleanup_temp
                    cleanup_temp=$(mktemp /tmp/version_history_cleanup_XXXXXX.tmp)
                    # Use grep -F for fixed-string matching to avoid regex interpretation
                    grep -F -v "|${backup_name}|" "$VERSION_HISTORY_FILE" > "$cleanup_temp" 2>/dev/null
                    mv "$cleanup_temp" "$VERSION_HISTORY_FILE" 2>/dev/null
                    rm -f "$cleanup_temp" 2>/dev/null  # Clean up in case mv failed
                fi
            else
                log_message "WARN" "Failed to remove old backup: $old_backup"
            fi
        done
    
    # Final count
    local final_count
    final_count=$(find "$CONFIG_DIR" -name "${CONFIG_BACKUP_PREFIX}*.yaml" 2>/dev/null | wc -l)
    log_message "INFO" "Backup cleanup completed: $final_count backups remaining"
}

# Function to check if any versions are available for rollback
has_previous_versions() {
    if [ ! -f "$VERSION_HISTORY_FILE" ]; then
        return 1
    fi
    
    # Check if there are any valid non-REVERT entries in the version history
    local version_count=0
    while IFS='|' read -r timestamp image_hash config_backup image_tag context; do
        # Skip empty lines and comments
        [ -z "$timestamp" ] || [[ "$timestamp" == \#* ]] && continue
        
        # Skip REVERT entries - they shouldn't be rollback targets
        [ "$context" = "REVERT" ] && continue
        
        # Validate entry format
        if validate_version_history_entry "${timestamp}|${image_hash}|${config_backup}|${image_tag}|${context}"; then
            version_count=$((version_count + 1))
        fi
    done < "$VERSION_HISTORY_FILE"
    
    [ "$version_count" -gt 0 ]
}

# Function to list available versions for rollback
list_available_versions() {
    if [ ! -f "$VERSION_HISTORY_FILE" ]; then
        log_message "INFO" "No version history file found"
        return 1
    fi
    
    log_message "INFO" "Listing available versions for rollback"
    
    # Read version history file and display options (exclude REVERT entries)
    local version_count=0
    local -A seen_hashes  # Track unique image hashes to show most recent
    
    while IFS='|' read -r timestamp image_hash config_backup image_tag context; do
        # Skip empty lines and comments
        [ -z "$timestamp" ] || [[ "$timestamp" == \#* ]] && continue
        
        # Skip REVERT entries - they are not rollback targets
        [ "$context" = "REVERT" ] && continue
        
        # Validate entry format
        if ! validate_version_history_entry "${timestamp}|${image_hash}|${config_backup}|${image_tag}|${context}"; then
            continue
        fi
        
        # Skip duplicate image hashes (keep only the most recent)
        if [ -n "${seen_hashes[$image_hash]:-}" ]; then
            log_message "INFO" "Skipping duplicate image hash: ${image_hash:0:12}..."
            continue
        fi
        seen_hashes[$image_hash]=1
        
        version_count=$((version_count + 1))
        
        # Format timestamp for display
        local display_time=""
        if [[ "$timestamp" =~ ^([0-9]{4})([0-9]{2})([0-9]{2})-([0-9]{2})([0-9]{2})([0-9]{2})$ ]]; then
            display_time="${BASH_REMATCH[1]}-${BASH_REMATCH[2]}-${BASH_REMATCH[3]} ${BASH_REMATCH[4]}:${BASH_REMATCH[5]}:${BASH_REMATCH[6]}"
        else
            display_time="$timestamp"
        fi
        
        # Check if config backup still exists
        local config_status="❌ missing"
        if [ "$config_backup" != "none" ] && [ -f "$CONFIG_DIR/$config_backup" ]; then
            config_status="✅ available"
        elif [ "$config_backup" = "none" ]; then
            config_status="➖ none"
        fi
        
        # Truncate image hash for display (strip sha256: prefix if present)
        local hash_without_prefix="${image_hash#sha256:}"
        local short_hash="${hash_without_prefix:0:12}..."
        
        # Format context for better readability
        local display_context="$context"
        case "$context" in
            "pre-update") display_context="📦 Pre-update backup" ;;
            "backup") display_context="💾 Manual backup" ;;
            "initial") display_context="🎬 Initial capture" ;;
            *) display_context="📍 $context" ;;
        esac
        
        echo "[$version_count] $display_time | Image: $short_hash | Config: $config_status"
        echo "    Tag: $image_tag"
        echo "    Context: $display_context"
        echo ""
        
    done < "$VERSION_HISTORY_FILE"
    
    if [ "$version_count" -eq 0 ]; then
        log_message "INFO" "No revertable versions found in tracking file"
        print_message "❌ No previous versions available for rollback" "$RED"
        print_message "💡 Rollback versions are created during updates" "$YELLOW"
        return 1
    fi
    
    log_message "INFO" "Found $version_count unique revertable versions"
    return 0
}

# Function to get version info by index
get_version_info() {
    local version_index="$1"
    
    if [ ! -f "$VERSION_HISTORY_FILE" ]; then
        return 1
    fi
    
    local current_index=0
    local -A seen_hashes  # Track unique image hashes to match list display
    
    while IFS='|' read -r timestamp image_hash config_backup image_tag context; do
        # Skip empty lines and comments
        [ -z "$timestamp" ] || [[ "$timestamp" == \#* ]] && continue
        
        # Skip REVERT entries - matching the list_available_versions logic
        [ "$context" = "REVERT" ] && continue
        
        # Validate entry format
        if ! validate_version_history_entry "${timestamp}|${image_hash}|${config_backup}|${image_tag}|${context}"; then
            continue
        fi
        
        # Skip duplicate image hashes (keep only the most recent) - matching list display
        if [ -n "${seen_hashes[$image_hash]:-}" ]; then
            continue
        fi
        seen_hashes[$image_hash]=1
        
        current_index=$((current_index + 1))
        
        if [ "$current_index" -eq "$version_index" ]; then
            echo "$timestamp|$image_hash|$config_backup|$image_tag|$context"
            return 0
        fi
    done < "$VERSION_HISTORY_FILE"
    
    return 1
}

# Function to show complete version history including all operations (for audit purposes)
show_version_history() {
    if [ ! -f "$VERSION_HISTORY_FILE" ]; then
        print_message "No version history file found" "$YELLOW"
        return 1
    fi
    
    print_message "\n📜 Complete Version History (including all operations):" "$GREEN"
    print_message "=" "$GRAY"
    
    local entry_count=0
    while IFS='|' read -r timestamp image_hash config_backup image_tag context; do
        # Skip empty lines and comments
        [ -z "$timestamp" ] || [[ "$timestamp" == \#* ]] && continue
        
        # Validate entry format
        if ! validate_version_history_entry "${timestamp}|${image_hash}|${config_backup}|${image_tag}|${context}"; then
            continue
        fi
        
        entry_count=$((entry_count + 1))
        
        # Format timestamp for display
        local display_time=""
        if [[ "$timestamp" =~ ^([0-9]{4})([0-9]{2})([0-9]{2})-([0-9]{2})([0-9]{2})([0-9]{2})$ ]]; then
            display_time="${BASH_REMATCH[1]}-${BASH_REMATCH[2]}-${BASH_REMATCH[3]} ${BASH_REMATCH[4]}:${BASH_REMATCH[5]}:${BASH_REMATCH[6]}"
        else
            display_time="$timestamp"
        fi
        
        # Truncate image hash for display
        local hash_without_prefix="${image_hash#sha256:}"
        local short_hash="${hash_without_prefix:0:12}..."
        
        # Format context with color coding
        local context_color="$NC"
        local context_icon="📍"
        case "$context" in
            "REVERT")
                context_color="$YELLOW"
                context_icon="🔄"
                ;;
            "pre-update")
                context_color="$GREEN"
                context_icon="📦"
                ;;
            "backup")
                context_color="$GREEN"
                context_icon="💾"
                ;;
            "initial")
                context_color="$GREEN"
                context_icon="🎬"
                ;;
        esac
        
        print_message "$context_icon [$entry_count] $display_time - $context" "$context_color"
        print_message "    Image: $short_hash | Tag: $image_tag" "$GRAY"
        
        if [ "$config_backup" != "none" ]; then
            if [ -f "$CONFIG_DIR/$config_backup" ]; then
                print_message "    Config: ✅ $config_backup" "$GRAY"
            else
                print_message "    Config: ❌ $config_backup (missing)" "$GRAY"
            fi
        fi
        print_message "" "$NC"
        
    done < "$VERSION_HISTORY_FILE"
    
    print_message "Total entries: $entry_count" "$GREEN"
    return 0
}

# Function to revert to a previous version
revert_to_version() {
    local version_index="$1"
    local revert_config="${2:-ask}"
    
    # Mark image as changed since we're reverting to a different version
    IMAGE_CHANGED="true"
    
    log_message "INFO" "=== Starting Version Revert Process ==="
    
    # Get version info
    local version_info
    version_info=$(get_version_info "$version_index")
    
    if [ -z "$version_info" ]; then
        log_message "ERROR" "Invalid version index: $version_index"
        return 1
    fi
    
    # Parse version info
    local timestamp image_hash config_backup image_tag context
    IFS='|' read -r timestamp image_hash config_backup image_tag context <<< "$version_info"
    
    log_message "INFO" "Reverting to version from: $timestamp"
    log_message "INFO" "Target image: $image_tag"
    log_message "INFO" "Target hash: $image_hash"
    log_message "INFO" "Config backup: $config_backup"
    
    # Capture current state before revert
    log_message "INFO" "=== Pre-Revert State Capture ==="
    log_system_resources "pre-revert"
    log_docker_state "pre-revert"
    log_service_state "pre-revert"
    
    # Stop current service
    log_message "INFO" "Stopping current service for revert"
    if systemctl is-active --quiet birdnet-go.service; then
        sudo systemctl stop birdnet-go.service
        log_command_result "systemctl stop birdnet-go.service" $? "stopping service for revert"
    fi
    
    # Try to pull the specific image by hash first, then by tag
    log_message "INFO" "Attempting to restore Docker image"
    
    # First check if image is already available locally
    local local_image_check
    local_image_check=$(safe_docker images --no-trunc --format "{{.ID}}" | grep -F "$image_hash" 2>/dev/null)
    
    if [ -n "$local_image_check" ]; then
        log_message "INFO" "Target image already available locally: $image_hash"
    else
        log_message "INFO" "Target image not found locally, attempting to pull: $image_tag"
        
        # Try pulling by tag (hash-based pulls are not typically supported in registries)
        if ! safe_docker pull "$image_tag" 2>/dev/null; then
            log_message "ERROR" "Failed to pull target image: $image_tag"
            log_message "WARN" "The target image may no longer be available in the registry"
            
            # Ask user if they want to continue with local image or abort
            print_message "❌ Could not pull target image from registry" "$RED"
            print_message "The image may no longer be available remotely." "$YELLOW"
            print_message "❓ Continue with local image if available? (y/n): " "$YELLOW" "nonewline"
            read -r continue_local
            
            if [[ ! "$continue_local" =~ ^[Yy]$ ]]; then
                log_message "INFO" "User cancelled revert due to image unavailability"
                return 1
            fi
            
            # Check again for local image
            local_image_check=$(safe_docker images --no-trunc --format "{{.ID}}" | grep -F "$image_hash" 2>/dev/null)
            if [ -z "$local_image_check" ]; then
                log_message "ERROR" "Target image not available locally either"
                print_message "❌ Target image not available locally or remotely" "$RED"
                return 1
            fi
            
            log_message "INFO" "Continuing with local image: $image_hash"
        else
            log_command_result "docker pull $image_tag" $? "pulling target image"
        fi
    fi
    
    # Handle config revert
    local config_reverted="false"
    if [ "$config_backup" != "none" ] && [ -f "$CONFIG_DIR/$config_backup" ]; then
        if [ "$revert_config" = "ask" ]; then
            print_message "📄 Config backup is available from the target version" "$GREEN"
            print_message "❓ Do you want to revert the configuration as well? (y/n): " "$YELLOW" "nonewline"
            read -r revert_config_choice
        else
            revert_config_choice="$revert_config"
        fi
        
        if [[ "$revert_config_choice" =~ ^[Yy]$ ]]; then
            log_message "INFO" "Reverting configuration file"
            
            # Create backup of current config first
            if [ -f "$CONFIG_FILE" ]; then
                local pre_revert_timestamp=$(date '+%Y%m%d-%H%M%S')
                local current_backup="${CONFIG_BACKUP_PREFIX}pre-revert-${pre_revert_timestamp}.yaml"
                cp "$CONFIG_FILE" "$CONFIG_DIR/$current_backup" 2>/dev/null
                log_message "INFO" "Current config backed up as: $current_backup"
            fi
            
            # Restore target config
            if cp "$CONFIG_DIR/$config_backup" "$CONFIG_FILE" 2>/dev/null; then
                log_message "INFO" "Configuration reverted to: $config_backup"
                config_reverted="true"
            else
                log_message "ERROR" "Failed to revert configuration"
            fi
        else
            log_message "INFO" "Keeping current configuration"
        fi
    elif [ "$config_backup" != "none" ]; then
        log_message "WARN" "Config backup file not found: $CONFIG_DIR/$config_backup"
    fi
    
    # Update systemd service to use the target image
    log_message "INFO" "Updating systemd service for reverted image"
    
    # We need to temporarily update BIRDNET_GO_IMAGE variable for service generation
    local original_image="$BIRDNET_GO_IMAGE"
    BIRDNET_GO_IMAGE="$image_tag"
    
    # Regenerate systemd service
    if add_systemd_config; then
        log_message "INFO" "Systemd service updated for reverted image"
    else
        log_message "ERROR" "Failed to update systemd service"
        BIRDNET_GO_IMAGE="$original_image"
        return 1
    fi
    
    # Restart the service to ensure container uses the reverted image
    log_message "INFO" "Restarting service with reverted image"
    sudo systemctl daemon-reload
    log_command_result "systemctl daemon-reload" $? "reloading systemd after revert"
    
    if sudo systemctl restart birdnet-go.service; then
        log_command_result "systemctl restart birdnet-go.service" $? "restarting reverted service"
        log_message "INFO" "Service restarted successfully with reverted image"
    else
        log_message "ERROR" "Failed to start service with reverted image"
        # Restore original image setting
        BIRDNET_GO_IMAGE="$original_image"
        return 1
    fi
    
    # Restore original image setting
    BIRDNET_GO_IMAGE="$original_image"
    
    # Post-revert validation
    log_message "INFO" "=== Post-Revert Validation ==="
    log_docker_state "post-revert"
    log_service_state "post-revert"
    
    # Test service responsiveness
    sleep 5
    if curl -s -f --connect-timeout 5 "http://localhost:${WEB_PORT:-8080}" >/dev/null 2>&1; then
        log_message "INFO" "Reverted service is responding on port ${WEB_PORT:-8080}"
        print_message "✅ Version revert completed successfully!" "$GREEN"
        print_message "📄 Configuration reverted: $config_reverted" "$GREEN"
    else
        log_message "WARN" "Reverted service may not be fully ready yet"
        print_message "⚠️ Version reverted, but service may still be starting..." "$YELLOW"
    fi
    
    # Record the revert operation with fresh timestamp
    local revert_timestamp
    revert_timestamp=$(date '+%Y%m%d-%H%M%S')
    local revert_entry="${revert_timestamp}|${image_hash}|$([ "$config_reverted" = "true" ] && echo "$config_backup" || echo "none")|${image_tag}|REVERT"
    append_version_history "$revert_entry"
    
    return 0
}

# Function to get IP address
get_ip_address() {
    # Get primary IP address, excluding docker and localhost interfaces
    local ip=""
    
    # Method 1: Try using ip command with POSIX-compatible regex
    if command_exists ip; then
        ip=$(ip -4 addr show scope global \
          | grep -vE 'docker|br-|veth' \
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
        # Collect detailed network diagnostics
        local dns_resolv=$(cat /etc/resolv.conf 2>/dev/null | grep -E "^nameserver" | head -3 | tr '\n' ';' || echo "unavailable")
        local default_route=$(ip route show default 2>/dev/null | head -1 || echo "unavailable")
        local network_interfaces=$(ip -br addr show 2>/dev/null | grep -v "lo" | tr '\n' ';' || echo "unavailable")
        local ping_error=$(ping -c 1 -W 2 8.8.8.8 2>&1 || echo "timeout")

        # Try alternative DNS servers to diagnose DNS vs routing issues
        local cloudflare_ping="failed"
        local quad9_ping="failed"
        ping -c 1 -W 2 1.1.1.1 >/dev/null 2>&1 && cloudflare_ping="success"
        ping -c 1 -W 2 9.9.9.9 >/dev/null 2>&1 && quad9_ping="success"

        local diagnostic_json=$(cat <<EOF
{
    "test": "ping",
    "target": "8.8.8.8",
    "error": "$(echo "$ping_error" | head -1 | sed 's/"/\\"/g')",
    "dns_servers": "$dns_resolv",
    "default_route": "$(echo "$default_route" | sed 's/"/\\"/g')",
    "network_interfaces": "$network_interfaces",
    "alternative_dns_tests": {
        "cloudflare_1.1.1.1": "$cloudflare_ping",
        "quad9_9.9.9.9": "$quad9_ping"
    }
}
EOF
)

        send_telemetry_event "error" "Network connectivity failed: ping test unsuccessful" "error" "step=network_check,error=ping_failed" "$diagnostic_json"
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

    # Docker Registry Check
    print_message "\n📡 Testing GitHub registry connectivity..." "$YELLOW"
    if curl -s "https://ghcr.io/v2/" >/dev/null 2>&1; then
        print_message "✅ GitHub registry is accessible" "$GREEN"
    else
        print_message "❌ Cannot access Docker registry" "$RED"
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

# Function to check system prerequisites
check_prerequisites() {
    print_message "🔧 Checking system prerequisites..." "$YELLOW"
    log_message "INFO" "Starting system prerequisites check"

    # Check CPU architecture and generation
    case "$(uname -m)" in
        "x86_64")
            log_message "INFO" "Detected x86_64 architecture, checking for AVX2 support"
            # Check CPU flags for AVX2 (Haswell and newer)
            if ! grep -q "avx2" /proc/cpuinfo; then
                log_message "WARN" "CPU does not have AVX2 support, user will be prompted"

                # Collect CPU details for diagnostics
                local cpu_model=$(grep -m1 "model name" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")
                local cpu_flags=$(grep -m1 "^flags" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs | head -c "$MAX_FLAGS_LENGTH" || echo "unknown")

                local diagnostic_json=$(cat <<EOF
{
    "architecture": "x86_64",
    "cpu_model": "$(echo "$cpu_model" | sed 's/"/\\"/g')",
    "required_feature": "avx2",
    "cpu_flags": "$(echo "$cpu_flags" | sed 's/"/\\"/g')...",
    "minimum_recommended": "Intel Haswell (2013) or AMD Excavator (2015)",
    "user_choice": "prompted"
}
EOF
)

                print_message "⚠️  CPU Compatibility Warning" "$YELLOW"
                print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$YELLOW"
                print_message "Your CPU: $cpu_model" "$NC"
                print_message "\nYour CPU does not support AVX2 instructions." "$YELLOW"
                print_message "BirdNET-Go is optimized for Intel Haswell (2013) or newer CPUs." "$YELLOW"
                print_message "\n⚠️  What this means:" "$YELLOW"
                print_message "  • The application may not start on systems without AVX2 support" "$YELLOW"
                print_message "  • TensorFlow Lite cannot load the model without necessary hardware support" "$YELLOW"
                print_message "  • However, some users have reported success on certain non-AVX2 systems" "$YELLOW"
                print_message "\n💡 You can try installing anyway, but the application may fail to start." "$NC"
                print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$YELLOW"

                print_message "\n❓ Do you want to proceed with installation anyway? (y/n): " "$YELLOW" "nonewline"
                read -r -t 60 response || response="n"

                if [[ "$response" =~ ^[Yy]$ ]]; then
                    log_message "INFO" "User chose to proceed despite missing AVX2 support"
                    diagnostic_json=$(echo "$diagnostic_json" | sed 's/"user_choice": "prompted"/"user_choice": "proceed_anyway"/')
                    send_telemetry_event "warning" "Installation proceeding without AVX2 support (user override)" "warning" "step=check_prerequisites,error=no_avx2,user_override=yes" "$diagnostic_json"
                    print_message "⚠️  Proceeding with installation (unsupported CPU configuration)" "$YELLOW"
                else
                    log_message "INFO" "User chose not to proceed without AVX2 support"
                    diagnostic_json=$(echo "$diagnostic_json" | sed 's/"user_choice": "prompted"/"user_choice": "declined"/')
                    send_telemetry_event "info" "Installation cancelled: CPU lacks AVX2 support" "info" "step=check_prerequisites,error=no_avx2,user_override=no" "$diagnostic_json"
                    print_message "❌ Installation cancelled" "$RED"
                    print_message "\n💡 Consider upgrading to a newer CPU with AVX2 support for best results." "$YELLOW"
                    exit 1
                fi
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

            local cpu_model=$(grep -m1 "model name" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")
            local cpu_hardware=$(grep -m1 "^Hardware" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")

            local diagnostic_json=$(cat <<EOF
{
    "architecture": "$(uname -m)",
    "cpu_model": "$(echo "$cpu_model" | sed 's/"/\\"/g')",
    "cpu_hardware": "$(echo "$cpu_hardware" | sed 's/"/\\"/g')",
    "issue": "32-bit ARM not supported",
    "required": "64-bit ARM (aarch64/arm64)"
}
EOF
)

            send_telemetry_event "error" "Architecture requirements not met: 32-bit ARM detected" "error" "step=check_prerequisites,error=32bit_arm" "$diagnostic_json"
            print_message "❌ 32-bit ARM architecture detected. BirdNET-Go requires 64-bit ARM processor and OS" "$RED"
            exit 1
            ;;
        *)
            log_message "ERROR" "Unsupported CPU architecture: $(uname -m)"

            local cpu_info=$(grep -m1 "model name" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")

            local diagnostic_json=$(cat <<EOF
{
    "architecture": "$(uname -m)",
    "cpu_info": "$(echo "$cpu_info" | sed 's/"/\\"/g')",
    "supported_architectures": ["x86_64 (with AVX2)", "aarch64", "arm64"],
    "issue": "unsupported_architecture"
}
EOF
)

            send_telemetry_event "error" "Unsupported CPU architecture: $(uname -m)" "error" "step=check_prerequisites,error=unsupported_arch" "$diagnostic_json"
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
            # Debian 11 (Bullseye) has VERSION_ID="11"
            if [ -n "$VERSION_ID" ] && [ "$VERSION_ID" -lt 11 ]; then
                log_message "ERROR" "Debian version $VERSION_ID too old, minimum version 11 required"
                print_message "❌ Debian $VERSION_ID too old. Version 11 (Bullseye) or newer required" "$RED"
                exit 1
            else
                log_message "INFO" "OS compatibility check passed: Debian $VERSION_ID"
                print_message "✅ Debian $VERSION_ID found" "$GREEN"
            fi
            ;;
        raspbian)
            log_message "ERROR" "32-bit Raspberry Pi OS detected, 64-bit version required"
            print_message "❌ You are running 32-bit version of Raspberry Pi OS. BirdNET-Go requires 64-bit version" "$RED"
            exit 1
            ;;
        ubuntu)
            # Ubuntu 20.04 has VERSION_ID="20.04"
            ubuntu_version=$(echo "$VERSION_ID" | awk -F. '{print $1$2}')
            if [ "$ubuntu_version" -lt 2004 ]; then
                log_message "ERROR" "Ubuntu version $VERSION_ID too old, minimum version 20.04 required"
                print_message "❌ Ubuntu $VERSION_ID too old. Version 20.04 or newer required" "$RED"
                exit 1
            else
                log_message "INFO" "OS compatibility check passed: Ubuntu $VERSION_ID"
                print_message "✅ Ubuntu $VERSION_ID found" "$GREEN"
            fi
            ;;
        *)
            log_message "ERROR" "Unsupported Linux distribution: $ID"
            print_message "❌ Unsupported Linux distribution for install.sh. Please use Debian 11+, Ubuntu 20.04+, or Raspberry Pi OS (Bullseye+)" "$RED"
            exit 1
            ;;
    esac

    # Function to add user to required groups
    add_user_to_groups() {
        print_message "🔧 Adding user $USER to required groups..." "$YELLOW"
        local groups_added=false

        if ! groups "$USER" | grep &>/dev/null "\bdocker\b"; then
            if sudo usermod -aG docker "$USER"; then
                print_message "✅ Added user $USER to docker group" "$GREEN"
                groups_added=true
            else
                print_message "❌ Failed to add user $USER to docker group" "$RED"
                exit 1
            fi
        fi

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

        if [ "$groups_added" = true ]; then
            print_message "Please log out and log back in for group changes to take effect, and rerun install.sh to continue with install" "$YELLOW"
            exit 0
        fi
    }

    # Check and install Docker
    if ! command_exists docker; then
        log_message "INFO" "Docker not found, installing Docker from apt repository"
        print_message "🐳 Docker not found. Installing Docker..." "$YELLOW"
        # Install Docker from apt repository
        sudo apt -qq update
        log_command_result "apt update" $? "Docker installation preparation"
        sudo apt -qq install -y docker.io
        log_command_result "apt install docker.io" $? "Docker package installation"
        # Add current user to required groups
        add_user_to_groups
        # Start Docker service
        if sudo systemctl start docker; then
            log_message "INFO" "Docker service started successfully"
            print_message "✅ Docker service started successfully" "$GREEN"
        else
            log_message "ERROR" "Failed to start Docker service"
            print_message "❌ Failed to start Docker service" "$RED"
            exit 1
        fi
        
        # Enable Docker service on boot
        if  sudo systemctl enable docker; then
            log_message "INFO" "Docker service enabled for boot startup"
            print_message "✅ Docker service start on boot enabled successfully" "$GREEN"
        else
            log_message "ERROR" "Failed to enable Docker service on boot"
            print_message "❌ Failed to enable Docker service on boot" "$RED"
            exit 1
        fi
        log_message "INFO" "Docker installation completed, user needs to log out and back in for group changes"
        print_message "⚠️ Docker installed successfully. To make group member changes take effect, please log out and log back in and rerun install.sh to continue with install" "$YELLOW"
        # exit install script
        exit 0
    else
        log_message "INFO" "Docker already installed and available"
        print_message "✅ Docker found" "$GREEN"
        
        # Check if user is in required groups
        add_user_to_groups

        # Check if Docker can be used by the user
        if ! docker info &>/dev/null; then
            log_message "ERROR" "Docker installed but not accessible by user $USER"
            print_message "❌ Docker cannot be accessed by user $USER. Please ensure you have the necessary permissions." "$RED"
            exit 1
        else
            log_message "INFO" "Docker accessibility check passed for user $USER"
            print_message "✅ Docker is accessible by user $USER" "$GREEN"
        fi
    fi

    # Web interface port heads-up. Ports are configurable now, so this is informational
    # only and never fatal (GH #3485): a busy port must not block the install. The web
    # port can be changed during configuration (configure_web_port), and the optional
    # 80/443 (AutoTLS) and 8090 (metrics) bindings are opt-in and checked when enabled.
    print_message "🔌 Checking web interface port availability..." "$YELLOW"
    local web_port_check="${WEB_PORT:-8080}"
    if check_port_availability "$web_port_check"; then
        print_message "✅ Web interface port $web_port_check is available" "$GREEN"
    else
        local web_port_proc
        web_port_proc=$(get_port_process_info "$web_port_check")
        print_message "⚠️ Default web interface port $web_port_check is in use by: $web_port_proc" "$YELLOW"
        print_message "   You can choose a different web interface port during configuration." "$YELLOW"
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

# Function to check if a directory exists
check_directory_exists() {
    local dir="$1"
    if [ -d "$dir" ]; then
        return 0 # Directory exists
    else
        return 1 # Directory does not exist
    fi
}

# Function to check if directories can be created
check_directory() {
    local dir="$1"
    if [ ! -d "$dir" ]; then
        if ! mkdir -p "$dir" 2>/dev/null; then
            print_message "❌ Cannot create directory $dir" "$RED"
            print_message "Please check permissions" "$YELLOW"
            exit 1
        fi
    elif [ ! -w "$dir" ]; then
        print_message "❌ Cannot write to directory $dir" "$RED"
        print_message "Please check permissions" "$YELLOW"
        exit 1
    fi
}

# Function to warn against running as root (soft block with bypass)
check_not_root() {
    if [ "$(id -u)" -eq 0 ]; then
        if [ "$FORCE_ROOT" = "true" ]; then
            print_message "⚠️  Running as root (--force-root was specified)" "$YELLOW"
            return 0
        fi

        print_message "" "$NC"
        print_message "⚠️  Running as root is strongly discouraged" "$YELLOW"
        print_message "" "$NC"
        print_message "Running as root places all data under /root/birdnet-go-app/," "$YELLOW"
        print_message "which causes settings and recordings to become inaccessible" "$YELLOW"
        print_message "if you later try to manage the application as a regular user." "$YELLOW"
        print_message "" "$NC"
        print_message "It is strongly recommended to run this script as a non-privileged user." "$GREEN"
        print_message "The script uses sudo internally when elevated privileges are needed." "$NC"
        print_message "" "$NC"
        print_message "Recommended: run as your regular user instead:" "$GREEN"
        print_message "  ./install.sh" "$NC"
        print_message "" "$NC"
        print_message "To proceed as root anyway, re-run with --force-root:" "$YELLOW"
        print_message "  ./install.sh --force-root" "$NC"
        print_message "" "$NC"
        exit 1
    fi
}

# Rewrite absolute paths in a migrated config that still reference the old installation
# location so they resolve under the new owner's home (GH #3179: a config carried over from
# /root keeps paths like monitoring.disk.paths: /root/.config/birdnet-go, which then fail
# with "permission denied"). Only the exact old-install prefixes are rewritten so unrelated
# user paths are left untouched. The pattern side is escaped so '.' is matched literally.
rewrite_migrated_config_paths() {
    local config="$1"
    local old_home="$2"   # e.g. /root
    local new_home="$3"   # e.g. /home/pi
    [ -f "$config" ] || return 0
    [ -n "$old_home" ] && [ -n "$new_home" ] || return 0
    [ "$old_home" = "$new_home" ] && return 0

    local pair
    for pair in "birdnet-go-app" ".config/birdnet-go" ".cache/birdnet-go"; do
        local old_path="${old_home}/${pair}"
        local new_path="${new_home}/${pair}"
        local esc_old esc_new
        esc_old=$(sed_escape_pattern "$old_path")
        esc_new=$(sed_escape_replacement "$new_path")
        sed -i -e "s|${esc_old}|${esc_new}|g" -- "$config"
    done
    log_message "INFO" "Rewrote migrated config paths from ${old_home}/* to ${new_home}/* in $config"
}

# Function to migrate a root installation to the current user's home directory
migrate_installation() {
    local source_path="$1"
    local dest_path="$2"

    log_message "INFO" "=== Starting Installation Migration ==="
    log_message "INFO" "Source: $source_path, Destination: $dest_path"

    print_message "" "$NC"
    print_message "🔄 Migrating installation from $source_path to $dest_path..." "$YELLOW"

    # Pre-flight: verify source has valid data (use sudo since source may be under /root)
    if ! sudo test -f "${source_path}/config/config.yaml" 2>/dev/null; then
        print_message "❌ Source directory does not contain a valid BirdNET-Go installation" "$RED"
        log_message "ERROR" "Migration source missing config/config.yaml: $source_path"
        send_telemetry_event "error" "Root migration failed: no config at source" "error" "step=migrate,reason=no_source_config"
        return 1
    fi

    # Pre-flight: verify destination does not already contain data
    if [ -d "$dest_path" ] && [ -n "$(ls -A "$dest_path" 2>/dev/null)" ]; then
        print_message "❌ Destination already contains BirdNET-Go data" "$RED"
        print_message "   Remove or empty $dest_path first if you want to re-migrate." "$YELLOW"
        log_message "ERROR" "Migration destination is not empty: $dest_path"
        send_telemetry_event "error" "Root migration failed: destination exists" "error" "step=migrate,reason=dest_exists"
        return 1
    fi

    # Pre-flight: check disk space
    local source_size_kb
    source_size_kb=$(sudo du -sk "$source_path" 2>/dev/null | cut -f1)
    if [ -n "$source_size_kb" ] && [ "$source_size_kb" -gt 0 ]; then
        local dest_parent
        dest_parent=$(dirname "$dest_path")
        mkdir -p "$dest_parent" 2>/dev/null
        local dest_available_kb
        dest_available_kb=$(df -Pk "$dest_parent" 2>/dev/null | awk 'NR==2 {print $4}')

        if [ -n "$dest_available_kb" ]; then
            # Require 10% safety margin
            local required_kb=$(( source_size_kb + source_size_kb / 10 ))
            if [ "$dest_available_kb" -lt "$required_kb" ]; then
                local required_mb=$(( required_kb / 1024 ))
                local available_mb=$(( dest_available_kb / 1024 ))
                print_message "❌ Not enough disk space for migration" "$RED"
                print_message "   Need ~${required_mb} MB (including 10% buffer), only ${available_mb} MB available" "$YELLOW"
                log_message "ERROR" "Migration failed: insufficient disk space (need ${source_size_kb}KB, have ${dest_available_kb}KB)"
                send_telemetry_event "error" "Root migration failed: insufficient disk space" "error" "step=migrate,reason=disk_space"
                return 1
            fi
        fi
    fi

    # Step 1: Stop service and remove container (reuse existing robust stop logic)
    if detect_birdnet_service || check_container_running 2>/dev/null; then
        stop_birdnet_service true
        log_message "INFO" "Stopped birdnet-go service and container"
    fi
    # Force-remove container to release SQLite locks
    if command_exists docker; then
        sudo docker rm -f birdnet-go 2>/dev/null || true
    fi

    # Step 2: Copy data
    print_message "📋 Copying data..." "$YELLOW"
    mkdir -p -- "$dest_path" || {
        print_message "❌ Failed to create destination directory" "$RED"
        log_message "ERROR" "Migration mkdir failed: $dest_path"
        return 1
    }
    if ! sudo cp -a "${source_path}/." "$dest_path/"; then
        print_message "❌ Failed to copy data, rolling back" "$RED"
        sudo rm -rf -- "$dest_path"
        log_message "ERROR" "Migration cp -a failed, rolled back"
        send_telemetry_event "error" "Root migration failed: copy error" "error" "step=migrate,reason=copy_failed"
        return 1
    fi
    log_message "INFO" "Data copied successfully"

    # Step 3: Fix ownership
    print_message "🔧 Fixing file ownership..." "$YELLOW"
    if ! sudo chown -R "$USER:$USER" "$dest_path/"; then
        print_message "❌ Failed to change file ownership, rolling back copy" "$RED"
        sudo rm -rf -- "$dest_path"
        log_message "ERROR" "Migration chown failed, rolled back"
        send_telemetry_event "error" "Root migration failed: chown error" "error" "step=migrate,reason=chown_failed"
        return 1
    fi
    log_message "INFO" "File ownership updated to $USER"

    # Rewrite absolute paths that still point at the old install location (GH #3179)
    rewrite_migrated_config_paths "${dest_path}/config/config.yaml" "$(dirname "$source_path")" "$HOME"

    # Step 4: Post-migration validation
    print_message "🔍 Validating migration..." "$YELLOW"
    local validation_ok=true

    if [ ! -f "${dest_path}/config/config.yaml" ]; then
        print_message "❌ Config file missing after migration" "$RED"
        validation_ok=false
    else
        local file_owner
        file_owner=$(stat -c %U "${dest_path}/config/config.yaml" 2>/dev/null)
        if [ "$file_owner" != "$USER" ]; then
            print_message "❌ File ownership incorrect (expected $USER, got $file_owner)" "$RED"
            validation_ok=false
        fi
    fi

    # SQLite integrity check (best-effort, skip if sqlite3 not available)
    local db_file="${dest_path}/data/birdnet.db"
    if [ -f "$db_file" ] && command_exists sqlite3; then
        local integrity_result
        integrity_result=$(sqlite3 "$db_file" "PRAGMA integrity_check;" 2>/dev/null)
        if [ "$integrity_result" != "ok" ]; then
            print_message "⚠️  Database integrity check returned: $integrity_result" "$YELLOW"
            print_message "   The database may still work, but consider restoring from backup if issues arise." "$YELLOW"
            log_message "WARN" "SQLite integrity check after migration: $integrity_result"
        else
            log_message "INFO" "SQLite integrity check passed"
        fi
    fi

    if [ "$validation_ok" = false ]; then
        print_message "❌ Migration validation failed, rolling back copy" "$RED"
        sudo rm -rf -- "$dest_path"
        log_message "ERROR" "Migration validation failed, rolled back"
        send_telemetry_event "error" "Root migration failed: validation error" "error" "step=migrate,reason=validation_failed"
        return 1
    fi

    # Step 5: Preserve the web port, TLS/metrics port bindings, and timezone from the old
    # unit BEFORE deleting it. The migration path sets MIGRATION_DONE=true and skips the
    # interactive config steps, so without this the regenerated unit would fall back to
    # fresh-install defaults and a migrated install would lose a custom port, AutoTLS, or the
    # metrics binding.
    load_existing_service_config
    if [ -n "$CONFIGURED_TZ" ]; then
        log_message "INFO" "Preserved settings from old service (timezone: $CONFIGURED_TZ, web port: $WEB_PORT)"
        print_message "📍 Preserved existing timezone configuration: $CONFIGURED_TZ" "$GREEN"
    fi
    sudo systemctl disable --now birdnet-go.service 2>/dev/null || true
    sudo rm -f /etc/systemd/system/birdnet-go.service
    sudo rm -f /etc/systemd/system/multi-user.target.wants/birdnet-go.service
    sudo rm -f /lib/systemd/system/birdnet-go.service
    sudo rm -rf /etc/systemd/system/birdnet-go.service.d
    sudo systemctl daemon-reload
    sudo systemctl reset-failed birdnet-go.service 2>/dev/null || true
    log_message "INFO" "Removed old systemd service and related files"

    # Step 6: Success message and cleanup suggestion
    print_message "" "$NC"
    print_message "✅ Migration complete!" "$GREEN"
    print_message "" "$NC"
    print_message "Your data has been migrated to: $dest_path" "$GREEN"
    print_message "Your old data is still at: $source_path" "$NC"
    print_message "Once you've verified everything works, you can remove it with:" "$NC"
    print_message "  sudo rm -rf $source_path" "$NC"
    print_message "" "$NC"

    log_message "INFO" "Migration completed successfully"
    send_telemetry_event "info" "Root migration completed" "info" "step=migrate,source=root_home"

    MIGRATION_DONE="true"
    load_telemetry_config
    return 0
}

# Function to check for existing BirdNET-Go installation under a different user
check_existing_installation_owner() {
    local found_other_install=false
    local other_user=""
    local other_path=""

    # If the current user already has a valid install, skip the cross-user migration check
    # entirely. Migration intentionally leaves the source directory in place, so a leftover
    # install under /root or another home must not re-trigger the migration prompt on every
    # subsequent run (GH #3258, #3273). The user's own install is the active one.
    if [ -f "$HOME/birdnet-go-app/config/config.yaml" ]; then
        log_message "INFO" "Current user already has an install at $HOME/birdnet-go-app; skipping cross-user migration check"
        return 0
    fi

    # Helper: given a home directory path, set other_user/other_path if it differs from $HOME
    _check_install_home() {
        local install_home="$1"
        if [ -n "$install_home" ] && [ "$install_home" != "$HOME" ]; then
            local candidate_path="${install_home}/birdnet-go-app"
            # Verify the detected path actually exists on disk before flagging.
            # Stale systemd service files or Docker container metadata can
            # reference paths that no longer (or never) existed, causing
            # false-positive migration prompts (see issue #3273).
            local path_exists=false
            if [ "$install_home" = "/root" ]; then
                # /root is typically mode 700; use non-interactive sudo
                if sudo -n test -d "$candidate_path" 2>/dev/null; then
                    path_exists=true
                fi
            else
                if [ -d "$candidate_path" ]; then
                    path_exists=true
                fi
            fi
            if [ "$path_exists" = true ]; then
                found_other_install=true
                other_path="$candidate_path"
                if [ "$install_home" = "/root" ]; then
                    other_user="root"
                else
                    other_user=$(basename "$install_home")
                fi
            fi
        fi
    }

    # Method 1: Parse systemd service file for volume mounts pointing to another user's home
    if detect_birdnet_service; then
        local service_file=""
        if [ -f "/etc/systemd/system/birdnet-go.service" ]; then
            service_file="/etc/systemd/system/birdnet-go.service"
        elif [ -f "/lib/systemd/system/birdnet-go.service" ]; then
            service_file="/lib/systemd/system/birdnet-go.service"
        fi

        if [ -n "$service_file" ]; then
            local service_config_path
            service_config_path=$(sed -n 's/.*-v \([^ ]*\):\/config.*/\1/p' "$service_file" 2>/dev/null | head -1)

            if [ -n "$service_config_path" ]; then
                _check_install_home "${service_config_path%/birdnet-go-app/config}"
            fi
        fi
    fi

    # Method 2: Check Docker container volume mounts
    if [ "$found_other_install" = false ]; then
        local config_mount
        config_mount=$(safe_docker inspect --format '{{range .Mounts}}{{if eq .Destination "/config"}}{{.Source}}{{end}}{{end}}' birdnet-go)

        if [ -n "$config_mount" ] && [[ "$config_mount" == *"/birdnet-go-app/"* ]]; then
            _check_install_home "${config_mount%/birdnet-go-app/*}"
        fi
    fi

    # Method 3: Scan /root and other users' home directories for birdnet-go-app
    if [ "$found_other_install" = false ]; then
        # Check /root separately since it's typically mode 700 and requires sudo
        # Try non-interactive first; fall back to interactive only in non-silent mode
        # (interactive sudo would hang in automated/CI environments)
        if [ "$HOME" != "/root" ] && { sudo -n test -f "/root/birdnet-go-app/config/config.yaml" 2>/dev/null || { [ "$SILENT_MODE" != "true" ] && sudo test -f "/root/birdnet-go-app/config/config.yaml" 2>/dev/null; }; }; then
            found_other_install=true
            other_user="root"
            other_path="/root/birdnet-go-app"
        fi

        # Check other users' home directories
        if [ "$found_other_install" = false ]; then
            for candidate in /home/*/birdnet-go-app; do
                if [ -d "$candidate" ]; then
                    local candidate_home
                    candidate_home=$(dirname "$candidate")
                    if [ "$candidate_home" != "$HOME" ] && [ -f "${candidate}/config/config.yaml" ]; then
                        found_other_install=true
                        other_user=$(basename "$candidate_home")
                        other_path="$candidate"
                        break
                    fi
                fi
            done
        fi
    fi

    if [ "$found_other_install" = true ]; then
        log_message "WARN" "Existing installation found at $other_path (user: $other_user)"

        print_message "" "$NC"
        print_message "⚠️  Existing BirdNET-Go Installation Detected" "$YELLOW"
        print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$YELLOW"
        print_message "" "$NC"
        print_message "An existing BirdNET-Go installation was found at:" "$YELLOW"
        print_message "  $other_path (user: $other_user)" "$NC"
        print_message "" "$NC"
        print_message "You are running as: $USER ($HOME)" "$NC"
        print_message "This would create a separate installation at:" "$NC"
        print_message "  $HOME/birdnet-go-app" "$NC"
        print_message "" "$NC"
        print_message "⚠️  Installing as a different user will NOT migrate your" "$YELLOW"
        print_message "existing settings, recordings, or database." "$YELLOW"
        print_message "" "$NC"

        if [ "$other_user" = "root" ]; then
            # Root installs can be migrated automatically since we have sudo access
            if [ "$SILENT_MODE" = "true" ]; then
                # Silent mode: attempt automated migration, exit on failure
                log_message "INFO" "Silent mode: attempting automated migration from $other_path"
                if migrate_installation "$other_path" "$HOME/birdnet-go-app"; then
                    log_message "INFO" "Silent mode: migration succeeded"
                    return 0
                else
                    print_message "❌ Silent mode: migration failed" "$RED"
                    send_telemetry_event "error" "Root migration failed in silent mode" "error" "step=check_owner,other_user=$other_user"
                    exit 1
                fi
            fi

            print_message "Options:" "$GREEN"
            print_message "  1) Migrate data to your account (recommended)" "$NC"
            print_message "  2) Start fresh (existing data will NOT be migrated)" "$NC"
            print_message "  3) Cancel" "$NC"
            print_message "" "$NC"
            print_message "❓ Select an option (1-3): " "$YELLOW" "nonewline"
            read -r -t 120 migrate_choice || migrate_choice="3"

            case "$migrate_choice" in
                1)
                    if migrate_installation "$other_path" "$HOME/birdnet-go-app"; then
                        return 0
                    else
                        print_message "" "$NC"
                        print_message "⚠️  Automated migration failed. You can migrate manually:" "$YELLOW"
                        print_message "  sudo systemctl stop birdnet-go.service" "$NC"
                        print_message "  sudo docker rm -f birdnet-go" "$NC"
                        print_message "  mkdir -p $HOME/birdnet-go-app" "$NC"
                        print_message "  sudo cp -a /root/birdnet-go-app/. $HOME/birdnet-go-app/" "$NC"
                        print_message "  sudo chown -R \$USER:\$USER $HOME/birdnet-go-app/" "$NC"
                        print_message "  Then run: ./install.sh" "$NC"
                        exit 1
                    fi
                    ;;
                2)
                    log_message "WARN" "User chose fresh installation despite existing install at $other_path"
                    FRESH_INSTALL="true"
                    ;;
                *)
                    print_message "Installation cancelled." "$NC"
                    exit 0
                    ;;
            esac
        else
            # Non-root cross-user: manual instructions only
            print_message "💡 To use your existing installation, log in as '$other_user'" "$GREEN"
            print_message "and run: ./install.sh" "$NC"

            if [ "$SILENT_MODE" = "true" ]; then
                print_message "" "$NC"
                print_message "❌ Silent mode: refusing to create a second installation" "$RED"
                send_telemetry_event "error" "Duplicate installation detected in silent mode" "error" "step=check_owner,other_user=$other_user"
                exit 1
            fi

            print_message "" "$NC"
            print_message "❓ Proceed with a NEW installation as $USER? (y/n): " "$YELLOW" "nonewline"
            read -r -t 60 proceed || proceed="n"

            if [[ ! "$proceed" =~ ^[Yy]$ ]]; then
                print_message "Installation cancelled." "$NC"
                exit 0
            fi

            print_message "" "$NC"
            log_message "WARN" "User chose to proceed with new installation despite existing install at $other_path"
        fi
    fi
}

# Telemetry Configuration
TELEMETRY_ENABLED=false
TELEMETRY_INSTALL_ID=""
TELEMETRY_CONFIGURED="false"
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
        TELEMETRY_CONFIGURED="true"
    else
        # No telemetry file exists - telemetry has not been configured yet
        TELEMETRY_CONFIGURED="false"
    fi
    
    # Generate install ID if missing
    if [ -z "$TELEMETRY_INSTALL_ID" ]; then
        TELEMETRY_INSTALL_ID=$(generate_install_id)
    fi
}

# Function to save telemetry config
save_telemetry_config() {
    local telemetry_file="$CONFIG_DIR/.telemetry"
    
    # Ensure directory exists
    mkdir -p "$CONFIG_DIR"
    
    # Save config
    cat > "$telemetry_file" << EOF
# BirdNET-Go telemetry configuration
# This file stores your telemetry preferences
enabled=$TELEMETRY_ENABLED
install_id=$TELEMETRY_INSTALL_ID
EOF
}

# Function to configure telemetry
configure_telemetry() {
    # Silent mode: use env var, default to false
    if [ "$SILENT_MODE" = "true" ]; then
        if [ "$BIRDNET_TELEMETRY" = "true" ]; then
            TELEMETRY_ENABLED=true
            if [ -f "$CONFIG_FILE" ]; then
                sed -i 's/enabled: false  # true to enable Sentry error tracking/enabled: true  # true to enable Sentry error tracking/' "$CONFIG_FILE"
            fi
            print_message "🔇 Silent mode: telemetry enabled" "$YELLOW"
        else
            TELEMETRY_ENABLED=false
            print_message "🔇 Silent mode: telemetry disabled" "$YELLOW"
        fi
        save_telemetry_config
        return
    fi

    print_message "\n📊 Telemetry Configuration" "$GREEN"
    print_message "BirdNET-Go can send anonymous usage data to help improve the software." "$YELLOW"
    print_message "This includes:" "$YELLOW"
    print_message "  • Installation success/failure events" "$YELLOW"
    print_message "  • Anonymous system information (OS, architecture)" "$YELLOW"  
    print_message "  • Error diagnostics (no personal data)" "$YELLOW"
    print_message "\nNo audio data or bird detections are ever collected." "$GREEN"
    print_message "You can disable this at any time in the web interface." "$GREEN"
    
    print_message "\n❓ Enable anonymous telemetry? (y/n): " "$YELLOW" "nonewline"
    read -r enable_telemetry
    
    if [[ $enable_telemetry == "y" ]]; then
        TELEMETRY_ENABLED=true
        print_message "✅ Telemetry enabled. Thank you for helping improve BirdNET-Go!" "$GREEN"
        
        # Update config.yaml to enable Sentry
        if [ -f "$CONFIG_FILE" ]; then
            sed -i 's/enabled: false  # true to enable Sentry error tracking/enabled: true  # true to enable Sentry error tracking/' "$CONFIG_FILE"
        fi
    else
        TELEMETRY_ENABLED=false
        print_message "✅ Telemetry disabled. You can enable it later in settings if you wish." "$GREEN"
    fi
    
    # Save telemetry config
    save_telemetry_config
}

# Function to collect anonymous system information
collect_system_info() {
    local os_name="unknown"
    local os_version="unknown"
    local cpu_arch=$(uname -m)
    local docker_version="unknown"
    local pi_model="none"
    
    # Read OS information from /etc/os-release
    if [ -f /etc/os-release ]; then
        # Source the file to get the variables
        . /etc/os-release
        os_name="${ID:-unknown}"
        os_version="${VERSION_ID:-unknown}"
    fi
    
    # Get Docker version if available
    if command_exists docker; then
        docker_version=$(docker --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown")
    fi
    
    # Detect Raspberry Pi model or WSL
    if [ -f /proc/device-tree/model ]; then
        pi_model=$(cat /proc/device-tree/model 2>/dev/null | tr -d '\0' | sed 's/Raspberry Pi/RPi/g' || echo "none")
    elif grep -q microsoft /proc/version 2>/dev/null; then
        pi_model="wsl"
    fi
    
    # Output as JSON
    echo "{\"os_name\":\"$os_name\",\"os_version\":\"$os_version\",\"cpu_arch\":\"$cpu_arch\",\"docker_version\":\"$docker_version\",\"pi_model\":\"$pi_model\",\"install_id\":\"$TELEMETRY_INSTALL_ID\"}"
}

# Helper function to collect CPU diagnostics
# Returns: JSON object with cpu_model and truncated cpu_flags
# Note: CPU flags are truncated to MAX_FLAGS_LENGTH to prevent oversized payloads
collect_cpu_diagnostics() {
    local cpu_model
    local cpu_flags

    cpu_model=$(grep -m1 "model name" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")
    cpu_flags=$(grep -m1 "^flags" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs | head -c "$MAX_FLAGS_LENGTH" || echo "unknown")

    cat <<EOF
{
    "cpu_model": "$(echo "$cpu_model" | sed 's/"/\\"/g')",
    "cpu_flags": "$(echo "$cpu_flags" | sed 's/"/\\"/g')..."
}
EOF
}

# Helper function to safely truncate text for diagnostics
# Args:
#   $1 - text to truncate
#   $2 - max length (optional, defaults to MAX_ERROR_LENGTH)
# Returns: Truncated text
safe_truncate() {
    local text="$1"
    local max_length="${2:-$MAX_ERROR_LENGTH}"
    echo "$text" | head -c "$max_length"
}

# Helper function to validate JSON before sending to telemetry
# Args:
#   $1 - JSON string to validate
# Returns: Valid JSON or fallback error object
# Exit codes:
#   0 - JSON is valid or jq not available (pass-through)
#   1 - JSON is invalid, fallback returned
validate_diagnostic_json() {
    local json="$1"

    # Check if jq is available
    if command -v jq >/dev/null 2>&1; then
        if echo "$json" | jq empty 2>/dev/null; then
            echo "$json"
            return 0
        else
            log_message "WARN" "Invalid diagnostic JSON detected, using fallback"
            echo '{"error": "diagnostic_collection_failed", "reason": "invalid_json"}'
            return 1
        fi
    else
        # jq not available, just return the JSON
        echo "$json"
        return 0
    fi
}

# Helper function to collect Docker pull failure diagnostics
# Args:
#   $1 - pull_output (captured stderr/stdout from failed pull)
#   $2 - operation type (optional, e.g., "install", "update")
# Returns: JSON object with comprehensive Docker pull diagnostics
# Note: Performs network tests (registry, DNS) which may take a few seconds
collect_docker_pull_diagnostics() {
    local pull_output="$1"
    local operation="${2:-pull}"
    local docker_version
    local disk_space
    local registry_reachable="unknown"
    local dns_resolution="unknown"
    local pull_error

    # Collect Docker and disk info (avoid SC2155)
    docker_version=$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo "unknown")
    disk_space=$(df -h /var/lib/docker 2>/dev/null | awk 'NR==2 {print $4}' || echo "unknown")

    # Test registry connectivity
    if curl -s --max-time 5 "https://ghcr.io/v2/" >/dev/null 2>&1; then
        registry_reachable="yes"
    else
        registry_reachable="no"
    fi

    # Test DNS resolution for ghcr.io
    if nslookup ghcr.io >/dev/null 2>&1 || host ghcr.io >/dev/null 2>&1; then
        dns_resolution="success"
    else
        dns_resolution="failed"
    fi

    # Truncate pull error safely
    pull_error=$(echo "$pull_output" | tail -5 | tr '\n' ' ' | sed 's/"/\\"/g' | head -c "$MAX_ERROR_LENGTH")

    cat <<EOF
{
    "image": "${BIRDNET_GO_IMAGE}",
    "docker_version": "$docker_version",
    "available_disk_space": "$disk_space",
    "registry_reachable": "$registry_reachable",
    "dns_resolution": "$dns_resolution",
    "pull_error": "$pull_error",
    "user": "$USER",
    "docker_socket": "$(ls -la /var/run/docker.sock 2>&1 | sed 's/"/\\"/g')",
    "operation": "$operation"
}
EOF
}

# Function to send telemetry event
send_telemetry_event() {
    # Check if telemetry is enabled
    if [ "$TELEMETRY_ENABLED" != "true" ]; then
        return 0
    fi

    local event_type="$1"
    local message="$2"
    local level="${3:-info}"
    local context="${4:-}"
    local diagnostic_json="${5:-{}}"  # Optional structured diagnostic data

    # Validate diagnostic JSON before using
    diagnostic_json=$(validate_diagnostic_json "$diagnostic_json")

    # Collect system info before background process
    local system_info
    system_info=$(collect_system_info)

    # Run in background to not block installation
    {

        # Build JSON payload with enhanced diagnostic information
        local timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        local payload=$(cat <<EOF
{
    "timestamp": "$timestamp",
    "level": "$level",
    "message": "[install.sh] $message",
    "platform": "other",
    "environment": "production",
    "release": "install-script@1.0.0",
    "tags": {
        "event_type": "$event_type",
        "script_version": "1.0.0",
        "source": "install.sh"
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
        "docker_version": "$(echo "$system_info" | jq -r .docker_version)",
        "install_id": "$(echo "$system_info" | jq -r .install_id)",
        "context": "$context",
        "diagnostics": $diagnostic_json
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

# Function to check if there is enough disk space for Docker image
check_docker_space() {
    local required_space=2000000  # 2GB in KB
    local available_space
    available_space=$(df -k /var/lib/docker | awk 'NR==2 {print $4}')

    if [ "$available_space" -lt "$required_space" ]; then
        print_message "❌ Insufficient disk space for Docker image" "$RED"
        print_message "Required: 2GB, Available: $((available_space/1024))MB" "$YELLOW"
        exit 1
    fi
}

# Function to check data directory disk space requirements
check_data_directory_space() {
    local required_space=1048576  # 1GB in KB (1024*1024)
    local data_dir="${1:-$DATA_DIR}"

    # Ensure directory exists
    mkdir -p "$data_dir" 2>/dev/null || true

    # Get available space in KB with POSIX-compliant output
    local available_space
    available_space=$(df -Pk "$data_dir" 2>/dev/null | awk 'NR==2 {print $4}')

    # Check if df succeeded
    if [ -z "$available_space" ]; then
        print_message "❌ Unable to determine free space for $data_dir" "$RED"
        exit 1
    fi

    local available_mb=$((available_space/1024))

    if [ "$available_space" -lt "$required_space" ]; then
        print_message "❌ ERROR: Insufficient disk space for BirdNET-Go data directory" "$RED"
        print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$RED"
        print_message "Location:  $data_dir" "$YELLOW"
        print_message "Required:  1024 MB minimum" "$YELLOW"
        print_message "Available: ${available_mb} MB" "$RED"
        print_message "" "$NC"
        print_message "💡 To resolve this issue:" "$YELLOW"
        print_message "  1. Free up disk space on the volume" "$YELLOW"
        print_message "  2. Use a different location with more space" "$YELLOW"
        print_message "  3. Clean up old data: rm -rf $data_dir/clips/*" "$YELLOW"
        print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$RED"

        # Send telemetry if enabled (with PII redaction)
        # Redact path by hashing it to avoid exposing user's directory structure
        local path_hash
        path_hash=$(echo -n "$data_dir" | sha256sum | cut -d' ' -f1)
        local mount_point
        mount_point=$(df -P "$data_dir" 2>/dev/null | awk 'NR==2 {print $1}')

        # Split declaration and assignment to avoid SC2155
        local diagnostic_json
        diagnostic_json=$(cat <<EOF
{
    "data_directory_hash": "$path_hash",
    "required_mb": 1024,
    "available_mb": $available_mb,
    "mount_point": "$mount_point"
}
EOF
)
        send_telemetry_event "error" "Insufficient disk space for data directory" "error" "step=check_data_space" "$diagnostic_json"
        exit 1
    else
        log_message "INFO" "Data directory space check passed: ${available_mb}MB available (minimum: 1024MB)"
        print_message "✅ Data directory has sufficient space: ${available_mb}MB available" "$GREEN"
    fi
}

# Function to pull Docker image
pull_docker_image() {
    log_message "INFO" "Starting Docker image pull: $BIRDNET_GO_IMAGE"
    print_message "\n🐳 Pulling BirdNET-Go Docker image from GitHub Container Registry..." "$YELLOW"
    
    # Check if Docker can be used by the user
    if ! docker info &>/dev/null; then
        log_message "ERROR" "Docker not accessible by user $USER"
        print_message "❌ Docker cannot be accessed by user $USER. Please ensure you have the necessary permissions." "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- User $USER is not in the docker group" "$YELLOW"
        print_message "- Docker service is not running" "$YELLOW"
        print_message "- Insufficient privileges to access Docker socket" "$YELLOW"
        exit 1
    fi

    # Get current image hash before pull (if image exists locally)
    local pre_pull_hash=""
    if docker inspect "${BIRDNET_GO_IMAGE}" >/dev/null 2>&1; then
        pre_pull_hash=$(docker inspect --format='{{.Id}}' "${BIRDNET_GO_IMAGE}" 2>/dev/null || echo "")
        log_message "INFO" "Current image hash before pull: ${pre_pull_hash:0:20}..."
    else
        log_message "INFO" "Image not found locally, will be fresh pull"
    fi

    # Capture pull output and status
    local pull_output
    local pull_status
    pull_output=$(docker pull "${BIRDNET_GO_IMAGE}" 2>&1)
    pull_status=$?

    if [ $pull_status -eq 0 ]; then
        log_message "INFO" "Docker image pulled successfully: $BIRDNET_GO_IMAGE"

        # Get image hash after pull and compare
        local post_pull_hash=""
        post_pull_hash=$(docker inspect --format='{{.Id}}' "${BIRDNET_GO_IMAGE}" 2>/dev/null || echo "")

        if [ -n "$pre_pull_hash" ] && [ "$pre_pull_hash" = "$post_pull_hash" ]; then
            log_message "INFO" "No image update detected, same hash: ${pre_pull_hash:0:20}..."
            print_message "✅ Docker image is already up to date" "$GREEN"
            IMAGE_CHANGED="false"
        else
            if [ -n "$pre_pull_hash" ]; then
                log_message "INFO" "Image updated from ${pre_pull_hash:0:20}... to ${post_pull_hash:0:20}..."
                print_message "✅ Docker image updated successfully!" "$GREEN"
            else
                log_message "INFO" "Fresh image pulled: ${post_pull_hash:0:20}..."
                print_message "✅ Docker image pulled successfully" "$GREEN"
            fi
            IMAGE_CHANGED="true"
        fi
    else
        log_message "ERROR" "Docker image pull failed: $BIRDNET_GO_IMAGE"

        # Collect detailed diagnostics using helper function
        local diagnostic_json
        diagnostic_json=$(collect_docker_pull_diagnostics "$pull_output" "install")

        send_telemetry_event "error" "Docker image pull failed" "error" "step=pull_docker_image" "$diagnostic_json"
        print_message "❌ Failed to pull Docker image" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- No internet connection" "$YELLOW"
        print_message "- GitHub container registry being unreachable" "$YELLOW"
        print_message "- Invalid image name or tag" "$YELLOW"
        print_message "- Insufficient privileges to access Docker socket on local system" "$YELLOW"
        exit 1
    fi
}

# Helper function to check if BirdNET-Go systemd service exists
detect_birdnet_service() {
    # Check for service unit files on disk
    if [ -f "/etc/systemd/system/birdnet-go.service" ] || [ -f "/lib/systemd/system/birdnet-go.service" ]; then
        return 0
    fi
    return 1
}

# Function to check if BirdNET service exists
check_service_exists() {
    detect_birdnet_service
    return $?
}

# Function to safely execute docker commands, suppressing errors if Docker isn't installed
safe_docker() {
    if command_exists docker; then
        docker "$@" 2>/dev/null
        return $?
    fi
    return 1
}

# Function to check if BirdNET-Go is fully installed (service + container)
check_birdnet_installation() {
    local service_exists=false
    local image_exists=false
    local container_exists=false
    local container_running=false
    local debug_output=""

    # Check for systemd service
    if detect_birdnet_service; then
        service_exists=true
        debug_output="${debug_output}Systemd service detected. "
    fi
    
    # Only check Docker components if Docker is installed
    if command_exists docker; then
        # Streamlined Docker checks
        # Check for BirdNET-Go images
        if safe_docker images --format "{{.Repository}}:{{.Tag}}" | grep -q "birdnet-go"; then
            image_exists=true
            debug_output="${debug_output}Docker image exists. "
        fi
        
        # Check for any BirdNET-Go containers (running or stopped)
        container_count=$(safe_docker ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | wc -l)
        
        if [ "$container_count" -gt 0 ]; then
            container_exists=true
            debug_output="${debug_output}Container exists. "
            
            # Check if any of these containers are running
            running_count=$(safe_docker ps --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | wc -l)
            if [ "$running_count" -gt 0 ]; then
                container_running=true
                debug_output="${debug_output}Container running. "
            fi
        fi
        
        # Fallback check for containers with birdnet-go in the name
        if [ "$container_exists" = false ]; then
            if safe_docker ps -a | grep -q "birdnet-go"; then
                container_exists=true
                debug_output="${debug_output}Container with birdnet name exists. "
                
                # Check if any of these containers are running
                if safe_docker ps | grep -q "birdnet-go"; then
                    container_running=true
                    debug_output="${debug_output}Container with birdnet name running. "
                fi
            fi
        fi
    fi
    
    # Debug output - uncomment to debug installation check
    # print_message "DEBUG: $debug_output Service: $service_exists, Image: $image_exists, Container: $container_exists, Running: $container_running" "$YELLOW"
    
    # Check if Docker components exist (image or containers)
    local docker_components_exist
    if [ "$image_exists" = true ] || [ "$container_exists" = true ] || [ "$container_running" = true ]; then
        docker_components_exist=true
    else
        docker_components_exist=false
    fi    
    
    # Full installation: service AND Docker components
    if [ "$service_exists" = true ] && [ "$docker_components_exist" = true ]; then
        echo "full"  # Full installation with systemd
        return 0
    fi
    
    # Docker-only installation: Docker components but no service
    if [ "$service_exists" = false ] && [ "$docker_components_exist" = true ]; then
        echo "docker"  # Docker-only installation
        return 0
    fi
    
    echo "none"  # No installation
    return 1  # No installation
}

# Function to check if we have preserved data from previous installation
check_preserved_data() {
    # Only consider data preserved if the config file actually exists
    # Empty directories don't count as preserved data (they might be from incomplete install)
    if [ -f "$CONFIG_FILE" ]; then
        return 0  # Preserved data exists (actual config file)
    fi
    return 1  # No preserved data
}

# Function to convert only relative paths to absolute paths
convert_relative_to_absolute_path() {
    local config_file=$1
    local abs_path=$2
    local export_section_line # Declare separately

    # Look specifically for the audio export path in the export section
    export_section_line=$(grep -n "export:" "$config_file" | cut -d: -f1) # Assign separately
    if [ -z "$export_section_line" ]; then
        print_message "⚠️ Export section not found in config file" "$YELLOW"
        return 1
    fi

    # Find the path line within the export section (looking only at the next few lines after export:)
    local clip_path_line # Declare separately
    clip_path_line=$(tail -n +$export_section_line "$config_file" | grep -n "path:" | head -1 | cut -d: -f1) # Assign separately
    if [ -z "$clip_path_line" ]; then
        print_message "⚠️ Clip path setting not found in export section" "$YELLOW"
        return 1
    fi

    # Calculate the actual line number in the file
    clip_path_line=$((export_section_line + clip_path_line - 1))

    # Extract the current path value
    local current_path # Declare separately
    # Corrected sed command and assignment
    current_path=$(sed -n "${clip_path_line}s/^[[:space:]]*path:[[:space:]]*\([^#]*\).*/\1/p" "$config_file" | xargs)

    # Remove quotes if present
    current_path=${current_path#\"}
    current_path=${current_path%\"}

    # Only convert if path is relative (doesn't start with /)
    if [[ ! "$current_path" =~ ^/ ]]; then
        print_message "Converting relative path '${current_path}' to absolute path '${abs_path}'" "$YELLOW"
        local escaped_path
        escaped_path=$(sed_escape_replacement "$abs_path")
        sed -i "${clip_path_line}s|^\([[:space:]]*path:[[:space:]]*\).*|\1${escaped_path}        # path to audio clip export directory|" "$config_file"
        return 0
    else
        print_message "Path '${current_path}' is already absolute, skipping conversion" "$GREEN"
        return 1
    fi
}

# Function to handle all path migrations
update_paths_in_config() {
    if [ -f "$CONFIG_FILE" ]; then
        print_message "🔧 Updating paths in configuration file..." "$YELLOW"
        if convert_relative_to_absolute_path "$CONFIG_FILE" "/data/clips/"; then
            print_message "✅ Audio export path updated to absolute path" "$GREEN"
        else
            print_message "ℹ️ Audio export path already absolute; no changes made" "$YELLOW"
        fi
    fi
}

# Helper function to clean up HLS tmpfs mount
cleanup_hls_mount() {
    local hls_mount="${CONFIG_DIR}/hls"
    local mount_unit # Declare separately
    mount_unit=$(systemctl list-units --type=mount | grep -i "$hls_mount" | awk '{print $1}') # Assign separately
    
    print_message "🧹 Cleaning up tmpfs mounts..." "$YELLOW"
    
    # First check if the mount exists
    if mount | grep -q "$hls_mount" || [ -n "$mount_unit" ]; then
        if [ -n "$mount_unit" ]; then
            print_message "Found systemd mount unit: $mount_unit" "$YELLOW"
            
            # Try to stop the mount unit using systemctl
            print_message "Stopping systemd mount unit..." "$YELLOW"
            sudo systemctl stop "$mount_unit" 2>/dev/null
            
            # Check if it's still active
            if systemctl is-active --quiet "$mount_unit"; then
                print_message "⚠️ Failed to stop mount unit, trying manual unmount..." "$YELLOW"
            else
                print_message "✅ Successfully stopped systemd mount unit" "$GREEN"
                return 0
            fi
        else
            print_message "Found tmpfs mount at $hls_mount, attempting to unmount..." "$YELLOW"
        fi
        
        # Try regular unmount approaches as fallback
        # Try regular unmount first
        umount "$hls_mount" 2>/dev/null
        
        # If still mounted, try with force flag
        if mount | grep -q "$hls_mount"; then
            umount -f "$hls_mount" 2>/dev/null
        fi
        
        # If still mounted, try with sudo
        if mount | grep -q "$hls_mount"; then
            sudo umount "$hls_mount" 2>/dev/null
        fi
        
        # If still mounted, try sudo with force flag
        if mount | grep -q "$hls_mount"; then
            sudo umount -f "$hls_mount" 2>/dev/null
        fi
        
        # If still mounted, try with lazy unmount as last resort
        if mount | grep -q "$hls_mount"; then
            print_message "⚠️ Regular unmount failed, trying lazy unmount..." "$YELLOW"
            sudo umount -l "$hls_mount" 2>/dev/null
        fi
        
        # Final check
        if mount | grep -q "$hls_mount"; then
            print_message "❌ Failed to unmount $hls_mount" "$RED"
            print_message "You may need to reboot the system to fully remove it" "$YELLOW"
        else
            print_message "✅ Successfully unmounted $hls_mount" "$GREEN"
        fi
    else
        print_message "No tmpfs mount found at $hls_mount" "$GREEN"
    fi
}

# Function to download base config file
download_base_config() {
    # If config file already exists and we're not doing a fresh install, just use the existing config
    if [ -f "$CONFIG_FILE" ] && [ "$FRESH_INSTALL" != "true" ]; then
        print_message "✅ Using existing configuration file: " "$GREEN" "nonewline"
        print_message "$CONFIG_FILE" "$NC"
        return 0
    fi
    
    print_message "\n📥 Downloading base configuration file from GitHub to: " "$YELLOW" "nonewline"
    print_message "$CONFIG_FILE" "$NC"
    
    # Download new config to temporary file first
    local temp_config="/tmp/config.yaml.new"
    if ! curl -s --fail https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml > "$temp_config"; then
        # Collect diagnostic information about the download failure
        local curl_error=$(curl -v --fail https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml 2>&1 | tail -5 | tr '\n' ' ' | sed 's/"/\\"/g')
        local dns_test="unknown"
        local http_test="unknown"

        # Test DNS resolution
        if nslookup raw.githubusercontent.com >/dev/null 2>&1 || host raw.githubusercontent.com >/dev/null 2>&1; then
            dns_test="success"
        else
            dns_test="failed"
        fi

        # Test HTTP connectivity
        if curl -s --max-time 5 -I https://raw.githubusercontent.com >/dev/null 2>&1; then
            http_test="success"
        else
            http_test="failed"
        fi

        local diagnostic_json=$(cat <<EOF
{
    "url": "https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml",
    "curl_error": "$(echo "$curl_error" | head -c 500)",
    "dns_resolution": "$dns_test",
    "http_connectivity": "$http_test",
    "temp_file": "$temp_config"
}
EOF
)

        send_telemetry_event "error" "Configuration download failed" "error" "step=download_base_config" "$diagnostic_json"
        print_message "❌ Failed to download configuration template" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- No internet connection or DNS resolution failed" "$YELLOW"
        print_message "- Firewall blocking outgoing connections" "$YELLOW"
        print_message "- GitHub being unreachable" "$YELLOW"
        print_message "\nPlease check your internet connection and try again." "$YELLOW"
        rm -f "$temp_config"
        exit 1
    fi

    if [ -f "$CONFIG_FILE" ]; then
        if cmp -s "$CONFIG_FILE" "$temp_config"; then
            print_message "✅ Base configuration already exists" "$GREEN"
            rm -f "$temp_config"
        else
            print_message "⚠️ Existing configuration file found." "$YELLOW"
            print_message "❓ Do you want to overwrite it? Backup of current configuration will be created (y/n): " "$YELLOW" "nonewline"
            read -r response
            
            if [[ "$response" =~ ^[Yy]$ ]]; then
                # Create backup with timestamp
                local backup_file
                backup_file="${CONFIG_FILE}.$(date '+%Y%m%d_%H%M%S').backup"
                cp "$CONFIG_FILE" "$backup_file"
                print_message "✅ Backup created: " "$GREEN" "nonewline"
                print_message "$backup_file" "$NC"
                
                mv "$temp_config" "$CONFIG_FILE"
                print_message "✅ Configuration updated successfully" "$GREEN"
            else
                print_message "✅ Keeping existing configuration file" "$YELLOW"
                rm -f "$temp_config"
            fi
        fi
    else
        mv "$temp_config" "$CONFIG_FILE"
        print_message "✅ Base configuration downloaded successfully" "$GREEN"
    fi
    
    # Always ensure clips path is absolute, regardless of whether config was updated or existing
    print_message "\n🔧 Checking audio export path configuration..." "$YELLOW"
    if convert_relative_to_absolute_path "$CONFIG_FILE" "/data/clips/"; then
        print_message "✅ Audio export path updated to absolute path" "$GREEN"
    else
        print_message "ℹ️ Audio export path already absolute; no changes made" "$YELLOW"
    fi
}

# Function to test RTSP URL
test_rtsp_url() {
    local url=$1
    
    # Parse URL to get host and port
    if [[ $url =~ rtsp://([^@]+@)?([^:/]+)(:([0-9]+))? ]]; then
        local host="${BASH_REMATCH[2]}"
        local port="${BASH_REMATCH[4]:-554}"  # Default RTSP port is 554
        
        print_message "🧪 Testing connection to $host:$port..." "$YELLOW"
        
        # Test port using timeout and nc, redirect all output to /dev/null
        if ! timeout 5 nc -zv "$host" "$port" &>/dev/null; then
            print_message "❌ Could not connect to $host:$port" "$RED"
            print_message "❓ Do you want to use this URL anyway? (y/n): " "$YELLOW" "nonewline"
            read -r force_continue
            
            if [[ $force_continue == "y" ]]; then
                print_message "⚠️ Continuing with untested RTSP URL" "$YELLOW"
                return 0
            fi
            return 1
        fi
        
        # Skip RTSP stream test, assume connection is good if port is open
        print_message "✅ Port is accessible, continuing with RTSP URL" "$GREEN"
        return 0
    else
        print_message "❌ Invalid RTSP URL format" "$RED"
    fi
    return 1
}

# Function to configure audio input
configure_audio_input() {
    log_message "INFO" "Starting audio capture configuration"

    # Silent mode: use RTSP URL from env var or skip configuration
    if [ "$SILENT_MODE" = "true" ]; then
        if [ -n "$BIRDNET_RTSP_URL" ]; then
            log_message "INFO" "Silent mode: configuring RTSP stream from BIRDNET_RTSP_URL=$BIRDNET_RTSP_URL"
            print_message "🔇 Silent mode: using RTSP stream $BIRDNET_RTSP_URL" "$YELLOW"
            AUDIO_ENV=""
            configure_rtsp_stream_silent "$BIRDNET_RTSP_URL"
        else
            log_message "INFO" "Silent mode: no BIRDNET_RTSP_URL set, skipping audio configuration"
            print_message "🔇 Silent mode: no audio source configured (set BIRDNET_RTSP_URL to configure)" "$YELLOW"
            AUDIO_ENV="--device /dev/snd"
        fi
        log_message "INFO" "Audio capture configuration completed (silent)"
        return
    fi

    while true; do
        print_message "\n🎤 Audio Capture Configuration" "$GREEN"
        print_message "1) Use sound card"
        print_message "2) Use RTSP stream"
        print_message "3) Configure later in BirdNET-Go web interface"
        print_message "❓ Select audio input method (1/2/3): " "$YELLOW" "nonewline"
        read -r audio_choice

        case $audio_choice in
            1)
                log_message "INFO" "User selected sound card audio input"
                if configure_sound_card; then
                    break
                fi
                ;;
            2)
                log_message "INFO" "User selected RTSP stream audio input"
                if configure_rtsp_stream; then
                    break
                fi
                ;;
            3)
                log_message "INFO" "User skipped audio configuration, will configure later via web interface"
                print_message "⚠️ Skipping audio input configuration" "$YELLOW"
                print_message "⚠️ You can configure audio input later in BirdNET-Go web interface at Audio Capture Settings" "$YELLOW"
                # MODIFIED: Always include device mapping even when skipping configuration
                AUDIO_ENV="--device /dev/snd"
                break
                ;;
            *)
                log_message "WARN" "Invalid audio input selection: $audio_choice"
                print_message "❌ Invalid selection. Please try again." "$RED"
                ;;
        esac
    done
    log_message "INFO" "Audio capture configuration completed"
}

# Function to validate audio device
validate_audio_device() {
    local device="$1"
    
    # Check if user is in audio group
    if ! groups "$USER" | grep &>/dev/null "\baudio\b"; then
        print_message "⚠️ User $USER is not in the audio group" "$YELLOW"
        if sudo usermod -aG audio "$USER"; then
            print_message "✅ Added user $USER to audio group" "$GREEN"
            print_message "⚠️ Please log out and log back in for group changes to take effect" "$YELLOW"
            exit 0
        else
            print_message "❌ Failed to add user to audio group" "$RED"
            return 1
        fi
    fi

    # Probe the device for a usable capture format. Many USB mics only support S16_LE at
    # 48kHz, but some only offer S24_3LE/S32_LE; BirdNET-Go/ffmpeg convert formats
    # downstream, so do not hard-fail solely because S16_LE is unavailable (GH #357). Try a
    # few common formats (LC_ALL=C forces English output) before giving up.
    local probe_fmt
    local audio_probe_ok="false"
    local audio_working_fmt=""
    for probe_fmt in S16_LE S24_3LE S32_LE S24_LE; do
        if LC_ALL=C arecord -c 1 -f "$probe_fmt" -r 48000 -d 1 -D "$device" /dev/null 2>/dev/null; then
            audio_probe_ok="true"
            audio_working_fmt="$probe_fmt"
            break
        fi
    done

    if [ "$audio_probe_ok" != "true" ]; then
        # Collect detailed audio device diagnostics (raw output, will be safely encoded)
        local arecord_error
        local device_list
        local alsa_devices
        local user_groups

        arecord_error=$(LC_ALL=C arecord -c 1 -f S16_LE -r 48000 -d 1 -D "$device" /dev/null 2>&1 | head -c "$MAX_ERROR_LENGTH")
        device_list=$(arecord -l 2>&1 | head -c "$MAX_ERROR_LENGTH")
        alsa_devices=$(ls -la /dev/snd/ 2>&1 | head -c "$MAX_ERROR_LENGTH")
        user_groups=$(groups 2>&1 || echo "unknown")

        # Use jq to safely construct JSON (handles newlines, quotes, special characters)
        local diagnostic_json
        diagnostic_json=$(jq -n \
            --arg device "$device" \
            --arg error "$arecord_error" \
            --arg devices "$device_list" \
            --arg perms "$alsa_devices" \
            --arg user "$USER" \
            --arg groups "$user_groups" \
            '{
                device: $device,
                arecord_error: $error,
                available_devices: $devices,
                alsa_device_permissions: $perms,
                user: $user,
                user_groups: $groups,
                test_parameters: {
                    channels: 1,
                    format: "S16_LE",
                    rate: 48000,
                    duration: 1
                }
            }')

        send_telemetry_event "error" "Audio device validation failed" "error" "step=validate_audio_device" "$diagnostic_json"
        print_message "❌ Failed to access audio device" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "  • Device is busy" "$YELLOW"
        print_message "  • Insufficient permissions" "$YELLOW"
        print_message "  • Device is not properly connected" "$YELLOW"
        print_message "  • Device does not support 48kHz mono capture in a common format" "$YELLOW"
        return 1
    fi

    if [ "$audio_working_fmt" = "S16_LE" ]; then
        print_message "✅ Audio device validated successfully, tested 48kHz 16-bit mono capture" "$GREEN"
    else
        print_message "✅ Audio device validated using $audio_working_fmt (48kHz mono); BirdNET-Go will convert the format as needed" "$GREEN"
    fi

    return 0
}

# Function to configure sound card
configure_sound_card() {
    log_message "INFO" "Starting sound card configuration"
    while true; do
        print_message "\n🎤 Detected audio devices:" "$GREEN"

        # Create arrays to store device information
        # Reset the array to empty on each iteration to prevent accumulation
        devices=()
        local default_selection=0
        
        # Capture arecord output to a variable first, forcing English locale 
        local arecord_output
        arecord_output=$(LC_ALL=C arecord -l 2>/dev/null)
        
        if [ -z "$arecord_output" ]; then
            log_message "ERROR" "No audio capture devices found on system"
            print_message "❌ No audio capture devices found!" "$RED"
            return 1
        fi
        
        log_message "INFO" "Found audio devices, parsing arecord output"
        
        # Parse arecord output and create a numbered list
        while IFS= read -r line; do
            if [[ $line =~ ^card[[:space:]]+([0-9]+)[[:space:]]*:[[:space:]]*([^,]+),[[:space:]]*device[[:space:]]+([0-9]+)[[:space:]]*:[[:space:]]*([^[]+)[[:space:]]*\[(.*)\] ]]; then
                card_num="${BASH_REMATCH[1]}"
                card_name="${BASH_REMATCH[2]}"
                device_num="${BASH_REMATCH[3]}"
                device_name="${BASH_REMATCH[4]}"
                device_desc="${BASH_REMATCH[5]}"
                # Clean up names
                card_name=$(echo "$card_name" | sed 's/\[//g' | sed 's/\]//g' | xargs)
                device_name=$(echo "$device_name" | xargs)
                device_desc=$(echo "$device_desc" | xargs)
                
                devices+=("$device_desc")
                
                # Set first USB device as default
                if [[ "$card_name" =~ USB && $default_selection -eq 0 ]]; then
                    default_selection=${#devices[@]}
                fi
                
                echo "[$((${#devices[@]}))] Card $card_num: $card_name"
                echo "    Device $device_num: $device_name [$device_desc]"
            fi
        done <<< "$arecord_output"

        if [ ${#devices[@]} -eq 0 ]; then
            log_message "ERROR" "No valid audio capture devices parsed from arecord output"
            print_message "❌ No audio capture devices found!" "$RED"
            return 1
        fi

        log_message "INFO" "Found ${#devices[@]} audio capture devices"

        # If no USB device was found, use first device as default
        if [ "$default_selection" -eq 0 ]; then
            default_selection=1
        fi

        print_message "\nPlease select a device number from the list above (1-${#devices[@]}) [${default_selection}] or 'b' to go back: " "$YELLOW" "nonewline"
        read -r selection

        if [ "$selection" = "b" ]; then
            log_message "INFO" "User chose to go back from sound card configuration"
            return 1
        fi

        # If empty, use default selection
        if [ -z "$selection" ]; then
            selection=$default_selection
        fi

        if [[ "$selection" =~ ^[0-9]+$ ]] && [ "$selection" -ge 1 ] && [ "$selection" -le "${#devices[@]}" ]; then
            local friendly_name="${devices[$((selection-1))]}"
            
            # Parse the original arecord output to get the correct card and device numbers
            local card_num
            local device_num
            local index=1
            while IFS= read -r line; do
                if [[ "$line" =~ ^card[[:space:]]+([0-9]+)[[:space:]]*:[[:space:]]*([^,]+),[[:space:]]*device[[:space:]]+([0-9]+) ]]; then
                    if [ "$index" -eq "$selection" ]; then
                        card_num="${BASH_REMATCH[1]}"
                        device_num="${BASH_REMATCH[3]}"
                        break
                    fi
                    ((index++))
                fi
            done <<< "$(LC_ALL=C arecord -l)"
            
            ALSA_CARD="$friendly_name"
            log_message "INFO" "User selected audio device: card $card_num, device $device_num (${friendly_name})"
            print_message "✅ Selected capture device: " "$GREEN" "nonewline"
            print_message "$ALSA_CARD"

            # Update the first audio source's device and name. Re-run-safe so it also works
            # when reconfiguring an already-configured install, not just a pristine template
            # (Forgejo #729). set_first_audio_source handles sed escaping internally and
            # returns non-zero when there is no active source line to edit (e.g. the config
            # is currently RTSP-only with the sound-card source commented out).
            if set_first_audio_source "$ALSA_CARD" "$ALSA_CARD"; then
                log_command_result "audio device configuration" 0 "updating config file"
            else
                print_message "⚠️ No active sound-card source found in the configuration (it may currently use RTSP)." "$YELLOW"
                print_message "   The device selection was not applied; switch the audio source in config.yaml or reinstall." "$YELLOW"
                log_message "WARN" "set_first_audio_source made no change: no active source line in $CONFIG_FILE"
            fi

            AUDIO_ENV="--device /dev/snd"
            return 0
        else
            log_message "WARN" "Invalid audio device selection: $selection"
            print_message "❌ Invalid selection. Please try again." "$RED"
        fi
    done
}

# Add an RTSP stream to the rtsp.streams section and disable the default sound card source.
# Usage: configure_rtsp_in_config <url> [stream_name]
configure_rtsp_in_config() {
    local url="$1"
    local stream_name="${2:-RTSP Stream}"

    local escaped_url
    escaped_url=$(sed_escape_replacement "$url")
    local escaped_name
    escaped_name=$(sed_escape_replacement "$stream_name")

    # Add stream entry to the rtsp.streams section (replaces empty array)
    sed -i "s|    streams: \[\].*|    streams:\n      - name: \"${escaped_name}\"\n        url: \"${escaped_url}\"\n        enabled: true\n        type: rtsp\n        transport: tcp|" "$CONFIG_FILE"
    log_command_result "sed RTSP stream configuration" $? "adding RTSP stream to config"

    # Comment out default sound card source (RTSP replaces local capture)
    sed -i \
        -e '/^      - name: "Sound Card 1"/s/^/# /' \
        -e '/^        device: "sysdefault"/s/^/# /' \
        -e '/^        gain: 0/s/^/# /' \
        "$CONFIG_FILE"
    log_command_result "sed disable sound card source" $? "commenting out default audio source"
}

# Function to configure RTSP stream
# Silent RTSP configuration using provided URL (no prompts)
configure_rtsp_stream_silent() {
    local url="$1"
    log_message "INFO" "Silent RTSP configuration with URL (host extracted for log)"

    if [[ ! $url =~ ^rtsp:// ]]; then
        log_message "ERROR" "Invalid RTSP URL format in silent mode: does not start with rtsp://"
        print_message "❌ Invalid BIRDNET_RTSP_URL format (must start with rtsp://)" "$RED"
        exit 1
    fi

    configure_rtsp_in_config "$url"

    AUDIO_ENV="--device /dev/snd"
    log_message "INFO" "Silent RTSP configuration completed"
}

configure_rtsp_stream() {
    log_message "INFO" "Starting RTSP stream configuration"
    while true; do
        print_message "\n🎥 RTSP Stream Configuration" "$GREEN"
        print_message "Configure primary RTSP stream. Additional streams can be added later via web interface at Audio Capture Settings." "$YELLOW"
        print_message "Enter RTSP URL (format: rtsp://user:password@address:port/path) or 'b' to go back: " "$YELLOW" "nonewline"
        read -r RTSP_URL

        if [ "$RTSP_URL" = "b" ]; then
            log_message "INFO" "User chose to go back from RTSP configuration"
            return 1
        fi
        
        if [[ ! $RTSP_URL =~ ^rtsp:// ]]; then
            log_message "WARN" "Invalid RTSP URL format provided (not starting with rtsp://)"
            print_message "❌ Invalid RTSP URL format. Please try again." "$RED"
            continue
        fi
        
        # Extract host from URL for logging (without credentials)
        local rtsp_host=""
        if [[ $RTSP_URL =~ rtsp://([^@]+@)?([^:/]+) ]]; then
            rtsp_host="${BASH_REMATCH[2]}"
        fi
        log_message "INFO" "Testing RTSP connection to host: ${rtsp_host:-unknown}"
        
        if test_rtsp_url "$RTSP_URL"; then
            log_message "INFO" "RTSP connection test successful, configuring RTSP audio input"
            print_message "✅ RTSP connection successful!" "$GREEN"
            
            # Add RTSP stream to config and disable default sound card source
            configure_rtsp_in_config "$RTSP_URL"
            
            # MODIFIED: Always include device mapping even with RTSP
            AUDIO_ENV="--device /dev/snd"
            return 0
        else
            log_message "WARN" "RTSP connection test failed for host: ${rtsp_host:-unknown}"
            print_message "❌ Could not connect to RTSP stream. Do you want to:" "$RED"
            print_message "1) Try again"
            print_message "2) Go back to audio input selection"
            print_message "❓ Select option (1/2): " "$YELLOW" "nonewline"
            read -r retry
            if [ "$retry" = "2" ]; then
                log_message "INFO" "User chose to go back after RTSP connection failure"
                return 1
            fi
            log_message "INFO" "User chose to retry RTSP configuration"
        fi
    done
}

# Function to configure audio export format
configure_audio_format() {
    # Silent mode: use default AAC
    if [ "$SILENT_MODE" = "true" ]; then
        local format="${BIRDNET_AUDIO_FORMAT:-aac}"
        # Validate format against allowed values
        case "$format" in
            wav|flac|aac|mp3|opus) ;;
            *) log_message "WARN" "Invalid BIRDNET_AUDIO_FORMAT: $format, defaulting to aac"
               format="aac" ;;
        esac
        sed -i "s|type: wav|type: $format|" "$CONFIG_FILE"
        print_message "🔇 Silent mode: audio format set to $format" "$YELLOW"
        return
    fi

    print_message "\n🔊 Audio Export Configuration" "$GREEN"
    print_message "Select audio format for captured sounds:"
    print_message "1) WAV (Uncompressed, largest files)" 
    print_message "2) FLAC (Lossless compression)"
    print_message "3) AAC (High quality, smaller files) - default" 
    print_message "4) MP3 (For legacy use only)" 
    print_message "5) Opus (Best compression)" 
    
    while true; do
        print_message "❓ Select format (1-5) [3]: " "$YELLOW" "nonewline"
        read -r format_choice

        # If empty, use default (AAC)
        if [ -z "$format_choice" ]; then
            format_choice="3"
        fi

        case $format_choice in
            1) format="wav"; break;;
            2) format="flac"; break;;
            3) format="aac"; break;;
            4) format="mp3"; break;;
            5) format="opus"; break;;
            *) print_message "❌ Invalid selection. Please try again." "$RED";;
        esac
    done

    print_message "✅ Selected audio format: " "$GREEN" "nonewline"
    print_message "$format"

    # Update config file (format is from hardcoded case, safe)
    sed -i "s|type: wav|type: $format|" "$CONFIG_FILE"
}

# Function to configure locale
configure_locale() {
    # Silent mode: use env var or default to en-uk
    if [ "$SILENT_MODE" = "true" ]; then
        local locale="${BIRDNET_LOCALE:-en-uk}"
        # Validate locale contains only safe characters
        if [[ ! "$locale" =~ ^[a-zA-Z0-9_-]+$ ]]; then
            log_message "ERROR" "Invalid BIRDNET_LOCALE format: $locale"
            locale="en-uk"
        fi
        sed -i "s|locale: [a-zA-Z0-9_-]*|locale: ${locale}|" "$CONFIG_FILE"
        print_message "🔇 Silent mode: locale set to $locale" "$YELLOW"
        return
    fi

    print_message "\n🌐 Locale Configuration for bird species names" "$GREEN"
    print_message "Available languages:" "$YELLOW"
    
    # Create arrays for locales
    declare -a locale_codes=("en-uk" "en-us" "af" "ar" "bg" "ca" "cs" "zh" "hr" "da" "nl" "et" "fi" "fr" "de" "el" "he" "hi-in" "hu" "is" "id" "it" "ja" "ko" "lv" "lt" "ml" "no" "pl" "pt" "pt-br" "pt-pt" "ro" "ru" "sr" "sk" "sl" "es" "sv" "th" "tr" "uk" "vi-vn")
    declare -a locale_names=("English (UK)" "English (US)" "Afrikaans" "Arabic" "Bulgarian" "Catalan" "Czech" "Chinese" "Croatian" "Danish" "Dutch" "Estonian" "Finnish" "French" "German" "Greek" "Hebrew" "Hindi" "Hungarian" "Icelandic" "Indonesian" "Italian" "Japanese" "Korean" "Latvian" "Lithuanian" "Malayalam" "Norwegian" "Polish" "Portuguese" "Brazilian Portuguese" "Portuguese (Portugal)" "Romanian" "Russian" "Serbian" "Slovak" "Slovenian" "Spanish" "Swedish" "Thai" "Turkish" "Ukrainian" "Vietnamese")
    
    # Display available locales
    for i in "${!locale_codes[@]}"; do
        printf "%2d) %-30s" "$((i+1))" "${locale_names[i]}"
        if [ $((i % 2)) -eq 1 ]; then
            echo
        fi
    done
    echo
    # Add a final newline if the last row is incomplete
    if [ $((${#locale_codes[@]} % 2)) -eq 1 ]; then
        echo
    fi

    while true; do
        print_message "❓ Select your language (1-${#locale_codes[@]}): " "$YELLOW" "nonewline"
        read -r selection
        
        if [[ "$selection" =~ ^[0-9]+$ ]] && [ "$selection" -ge 1 ] && [ "$selection" -le "${#locale_codes[@]}" ]; then
            LOCALE_CODE="${locale_codes[$((selection-1))]}"
            print_message "✅ Selected language: " "$GREEN" "nonewline"
            print_message "${locale_names[$((selection-1))]}"
            # Update config file (LOCALE_CODE is from hardcoded array, safe)
            sed -i "s|locale: [a-zA-Z0-9_-]*|locale: ${LOCALE_CODE}|" "$CONFIG_FILE"
            break
        else
            print_message "❌ Invalid selection. Please try again." "$RED"
        fi
    done
}

# Function to get location from NordVPN and OpenStreetMap
get_ip_location() {
    # First try NordVPN's service for city/country
    local nordvpn_info
    if nordvpn_info=$(curl -s "https://nordvpn.com/wp-admin/admin-ajax.php" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        --data-urlencode "action=get_user_info_data" 2>/dev/null) && [ -n "$nordvpn_info" ]; then
        # Check if the response is valid JSON and contains the required fields
        if echo "$nordvpn_info" | jq -e '.city and .country' >/dev/null 2>&1; then
            local city
            local country
            city=$(echo "$nordvpn_info" | jq -r '.city')
            country=$(echo "$nordvpn_info" | jq -r '.country')
            
            if [ "$city" != "null" ] && [ "$country" != "null" ] && [ -n "$city" ] && [ -n "$country" ]; then
                # Use OpenStreetMap to get precise coordinates
                local coordinates
                coordinates=$(curl -s "https://nominatim.openstreetmap.org/search?city=${city}&country=${country}&format=json" | jq -r '.[0] | "\(.lat) \(.lon)"')
                
                if [ -n "$coordinates" ] && [ "$coordinates" != "null null" ]; then
                    local lat
                    local lon
                    lat=$(echo "$coordinates" | cut -d' ' -f1)
                    lon=$(echo "$coordinates" | cut -d' ' -f2)
                    # NordVPN doesn't provide timezone, so return empty timezone field
                    echo "$lat|$lon|$city|$country|"
                    return 0
                fi
            fi
        fi
    fi

    # If NordVPN fails, try ipapi.co as a fallback (includes timezone info)
    local ipapi_info
    if ipapi_info=$(curl -s "https://ipapi.co/json/" 2>/dev/null) && [ -n "$ipapi_info" ]; then
        # Check if the response is valid JSON and contains the required fields
        if echo "$ipapi_info" | jq -e '.city and .country_name and .latitude and .longitude' >/dev/null 2>&1; then
            local city
            local country
            local lat
            local lon
            local timezone
            city=$(echo "$ipapi_info" | jq -r '.city')
            country=$(echo "$ipapi_info" | jq -r '.country_name')
            lat=$(echo "$ipapi_info" | jq -r '.latitude')
            lon=$(echo "$ipapi_info" | jq -r '.longitude')
            timezone=$(echo "$ipapi_info" | jq -r '.timezone // empty')
            
            if [ "$city" != "null" ] && [ "$country" != "null" ] && \
               [ "$lat" != "null" ] && [ "$lon" != "null" ] && \
               [ -n "$city" ] && [ -n "$country" ] && \
               [ -n "$lat" ] && [ -n "$lon" ]; then
                # Include timezone if available, otherwise empty
                if [ -n "$timezone" ] && [ "$timezone" != "null" ]; then
                    echo "$lat|$lon|$city|$country|$timezone"
                else
                    echo "$lat|$lon|$city|$country|"
                fi
                return 0
            fi
        fi
    fi

    return 1
}

# Resolve the host timezone using a single, validated detection chain.
# Shared by configure_timezone() and generate_systemd_service_content() so the two
# cannot drift apart (Forgejo #877). Tries, in order: an optional preferred candidate
# (e.g. a previously configured zone), /etc/timezone, timedatectl, and the
# /etc/localtime symlink. The result is validated against the zoneinfo database and
# rejected if it contains path traversal. Echoes a valid IANA zone name, or an empty
# string if none could be resolved (callers decide the UTC fallback so they can warn).
resolve_host_timezone() {
    local candidate="${1:-}"
    local tz="$candidate"

    if [ -z "$tz" ] && [ -f /etc/timezone ]; then
        tz=$(cat /etc/timezone 2>/dev/null | tr -d '\n' | tr -d ' ')
    fi

    if [ -z "$tz" ] && command_exists timedatectl; then
        tz=$(timedatectl show --property=Timezone --value 2>/dev/null | tr -d '\n' | tr -d ' ')
    fi

    if [ -z "$tz" ] && [ -L /etc/localtime ]; then
        local tz_path
        tz_path=$(readlink -f /etc/localtime)
        tz=${tz_path#/usr/share/zoneinfo/}
    fi

    # Validate against the zoneinfo database before trusting it. timedatectl can report
    # "n/a" on unconfigured images and a non-standard /etc/localtime symlink can leave an
    # absolute path; neither is a valid zone identifier. A value containing ".." would let
    # the existence check escape /usr/share/zoneinfo, so reject those outright.
    if [ -n "$tz" ] && { [[ "$tz" == *..* ]] || [ ! -f "/usr/share/zoneinfo/$tz" ]; }; then
        tz=""
    fi

    printf '%s' "$tz"
}

# Function to configure timezone
configure_timezone() {
    # Silent mode: use system timezone
    if [ "$SILENT_MODE" = "true" ]; then
        if [ -f /etc/timezone ]; then
            CONFIGURED_TZ=$(cat /etc/timezone 2>/dev/null | tr -d '\n')
        elif command -v timedatectl &>/dev/null; then
            CONFIGURED_TZ=$(timedatectl show --property=Timezone --value 2>/dev/null)
        else
            CONFIGURED_TZ="UTC"
        fi
        print_message "🔇 Silent mode: timezone set to $CONFIGURED_TZ" "$YELLOW"
        return
    fi

    print_message "\n🕐 Timezone Configuration" "$GREEN"
    print_message "BirdNET-Go needs to know your timezone for accurate timestamps and scheduling" "$YELLOW"

    # Get current system timezone
    local system_tz=""
    local detected_tz=""
    
    # Detect and validate the system timezone via the shared resolver (Forgejo #877)
    system_tz=$(resolve_host_timezone "")

    # Default to UTC if we couldn't detect
    if [ -z "$system_tz" ]; then
        system_tz="UTC"
        print_message "⚠️ Could not detect system timezone, defaulting to UTC" "$YELLOW"
    else
        print_message "📍 System timezone detected: $system_tz" "$GREEN"
    fi
    
    # Prefer location-based timezone detection over system timezone
    if [ -n "$DETECTED_TZ" ] && [ "$DETECTED_TZ" != "null" ]; then
        if [ -f "/usr/share/zoneinfo/$DETECTED_TZ" ]; then
            detected_tz="$DETECTED_TZ"
            print_message "🌍 Using timezone from location detection: $DETECTED_TZ" "$GREEN"
        else
            print_message "⚠️ Location-based timezone '$DETECTED_TZ' could not be validated, falling back to system timezone" "$YELLOW"
            # Fall back to system timezone validation
            if [ -f "/usr/share/zoneinfo/$system_tz" ]; then
                detected_tz="$system_tz"
                print_message "✅ System timezone '$system_tz' is valid" "$GREEN"
            else
                print_message "⚠️ System timezone '$system_tz' could not be validated" "$YELLOW"
                detected_tz="UTC"
            fi
        fi
    else
        # No location-based timezone, validate system timezone
        if [ -f "/usr/share/zoneinfo/$system_tz" ]; then
            detected_tz="$system_tz"
            print_message "✅ System timezone '$system_tz' is valid" "$GREEN"
        else
            print_message "⚠️ System timezone '$system_tz' could not be validated" "$YELLOW"
            detected_tz="UTC"
        fi
    fi
    
    # Check for common timezone misconfigurations
    local system_time=$(date +"%Y-%m-%d %H:%M:%S %Z")
    print_message "🕐 Current system time: $system_time" "$YELLOW"
    
    # Ask user to confirm timezone - provide context about where it came from
    if [ -n "$DETECTED_TZ" ] && [ "$DETECTED_TZ" = "$detected_tz" ]; then
        print_message "\n❓ Do you want to use the timezone detected from your location '$detected_tz'? (y/n): " "$YELLOW" "nonewline"
    else
        print_message "\n❓ Do you want to use the detected timezone '$detected_tz'? (y/n): " "$YELLOW" "nonewline"
    fi
    read -r use_detected
    
    if [[ $use_detected != "y" ]]; then
        print_message "\n📋 Common timezone examples (canonical IANA format):" "$YELLOW"
        print_message "  Americas:" "$YELLOW"
        print_message "    • America/New_York (US Eastern)" "$NC"
        print_message "    • America/Chicago (US Central)" "$NC"
        print_message "    • America/Denver (US Mountain)" "$NC"
        print_message "    • America/Los_Angeles (US Pacific)" "$NC"
        print_message "  Europe:" "$YELLOW"
        print_message "    • Europe/London, Europe/Berlin, Europe/Paris" "$NC"
        print_message "  Asia:" "$YELLOW"
        print_message "    • Asia/Tokyo, Asia/Singapore, Asia/Dubai" "$NC"
        print_message "  Other:" "$YELLOW"
        print_message "    • Australia/Sydney, Pacific/Auckland, UTC" "$NC"
        print_message "" "$NC"
        print_message "⚠️  Note: Legacy formats like US/Mountain are deprecated" "$YELLOW"
        print_message "   Use canonical formats (e.g., America/Denver) for best compatibility" "$YELLOW"

        # Helper function to check and offer canonical timezone alternatives
        check_and_offer_canonical_tz() {
            local tz="$1"
            local tz_var_name="$2"  # Variable name to update

            if [[ "$tz" =~ ^US/ ]] || [[ "$tz" =~ ^Etc/ ]]; then
                print_message "" "$NC"
                print_message "⚠️  WARNING: '$tz' uses legacy timezone format" "$YELLOW"
                print_message "   This format was moved to tzdata-legacy in Debian 13 (Trixie)" "$YELLOW"

                # Suggest canonical alternative
                local canonical_alternative=""
                case "$tz" in
                    "US/Eastern") canonical_alternative="America/New_York" ;;
                    "US/Central") canonical_alternative="America/Chicago" ;;
                    "US/Mountain") canonical_alternative="America/Denver" ;;
                    "US/Pacific") canonical_alternative="America/Los_Angeles" ;;
                    "US/Alaska") canonical_alternative="America/Anchorage" ;;
                    "US/Hawaii") canonical_alternative="Pacific/Honolulu" ;;
                esac

                if [ -n "$canonical_alternative" ]; then
                    print_message "   💡 Recommended canonical format: $canonical_alternative" "$GREEN"
                    print_message "" "$NC"
                    print_message "❓ Would you like to use $canonical_alternative instead? (y/n): " "$YELLOW" "nonewline"
                    read -r use_canonical

                    if [[ $use_canonical == "y" ]]; then
                        eval "$tz_var_name=\"$canonical_alternative\""
                        detected_tz="$canonical_alternative"
                        print_message "✅ Using canonical timezone: $canonical_alternative" "$GREEN"
                    else
                        print_message "⚠️  Continuing with legacy format (requires tzdata-legacy package)" "$YELLOW"
                    fi
                else
                    print_message "   💡 Consider using the canonical IANA timezone format" "$YELLOW"
                    print_message "   See: https://en.wikipedia.org/wiki/List_of_tz_database_time_zones" "$NC"
                fi
            fi
        }

        while true; do
            print_message "\n❓ Enter your timezone (e.g., America/New_York, Europe/London): " "$YELLOW" "nonewline"
            read -r user_tz
            
            # Convert lowercase input to proper case format
            local normalized_tz="$user_tz"
            if [[ "$user_tz" =~ ^[a-z]+/[a-z_]+ ]]; then
                # Convert region/city format from lowercase to proper case
                local region=$(echo "$user_tz" | cut -d'/' -f1 | sed 's/./\U&/')
                local city=$(echo "$user_tz" | cut -d'/' -f2 | sed 's/_/ /g; s/\b\w/\U&/g; s/ /_/g')
                normalized_tz="${region}/${city}"
                print_message "📝 Converting '$user_tz' to proper format: '$normalized_tz'" "$YELLOW"
            fi
            
            # Validate the timezone (try both original and normalized)
            if [ -f "/usr/share/zoneinfo/$user_tz" ]; then
                detected_tz="$user_tz"
                print_message "✅ Timezone '$user_tz' is valid" "$GREEN"

                # Check if timezone uses legacy format and offer canonical alternative
                check_and_offer_canonical_tz "$user_tz" "user_tz"

                # Show what time it would be in that timezone
                local tz_time=$(TZ="$user_tz" date +"%Y-%m-%d %H:%M:%S %Z")
                print_message "🕐 Time in $user_tz: $tz_time" "$YELLOW"
                
                print_message "❓ Is this the correct time for your location? (y/n): " "$YELLOW" "nonewline"
                read -r confirm_time
                
                if [[ $confirm_time == "y" ]]; then
                    break
                else
                    print_message "Let's try again with a different timezone" "$YELLOW"
                fi
            elif [ -f "/usr/share/zoneinfo/$normalized_tz" ]; then
                detected_tz="$normalized_tz"
                print_message "✅ Timezone '$normalized_tz' is valid" "$GREEN"

                # Check if timezone uses legacy format and offer canonical alternative
                check_and_offer_canonical_tz "$normalized_tz" "normalized_tz"

                # Show what time it would be in that timezone
                local tz_time=$(TZ="$normalized_tz" date +"%Y-%m-%d %H:%M:%S %Z")
                print_message "🕐 Time in $normalized_tz: $tz_time" "$YELLOW"
                
                print_message "❓ Is this the correct time for your location? (y/n): " "$YELLOW" "nonewline"
                read -r confirm_time
                
                if [[ $confirm_time == "y" ]]; then
                    break
                else
                    print_message "Let's try again with a different timezone" "$YELLOW"
                fi
            else
                print_message "❌ Timezone '$user_tz' not found" "$RED"
                if [ "$user_tz" != "$normalized_tz" ]; then
                    print_message "   Also tried: '$normalized_tz'" "$RED"
                fi

                # Check if this is a known legacy name that requires tzdata-legacy
                if [[ "$user_tz" =~ ^US/ ]] || [[ "$user_tz" =~ ^Etc/ ]]; then
                    print_message "" "$NC"
                    print_message "⚠️  This appears to be a legacy timezone name" "$YELLOW"
                    print_message "   On Debian 13 (Trixie), legacy timezones require the tzdata-legacy package" "$YELLOW"
                    print_message "" "$NC"
                    print_message "💡 You have two options:" "$YELLOW"
                    print_message "   1. Use a canonical timezone format instead (recommended)" "$GREEN"
                    print_message "   2. Install tzdata-legacy package: sudo apt install tzdata-legacy" "$YELLOW"
                else
                    print_message "💡 Tip: You can list all available timezones with: timedatectl list-timezones" "$YELLOW"
                    print_message "   Or check /usr/share/zoneinfo/ directory" "$YELLOW"
                fi
            fi
        done
    fi
    
    # Store the validated timezone for use in systemd service
    CONFIGURED_TZ="$detected_tz"
    
    # Provide guidance on system timezone if it differs
    if [ "$system_tz" != "$detected_tz" ] && [ "$system_tz" != "UTC" ]; then
        print_message "\n⚠️ NOTE: Your system timezone ($system_tz) differs from the configured timezone ($detected_tz)" "$YELLOW"
        print_message "BirdNET-Go will use: $detected_tz" "$YELLOW"
        print_message "\nTo change your system timezone to match, you can run:" "$YELLOW"
        print_message "  sudo timedatectl set-timezone $detected_tz" "$NC"
        print_message "This ensures all system services use the same timezone" "$YELLOW"
    fi
    
    print_message "\n✅ Timezone configuration complete: $detected_tz" "$GREEN"
}

# Function to configure location
configure_location() {
    log_message "INFO" "Starting location configuration"

    # Silent mode: use env vars or defaults
    if [ "$SILENT_MODE" = "true" ]; then
        local lat="${BIRDNET_LATITUDE:-0.000}"
        local lon="${BIRDNET_LONGITUDE:-0.000}"
        if ! validate_numeric "$lat" || ! validate_numeric "$lon"; then
            log_message "ERROR" "Invalid coordinates: lat=$lat lon=$lon (must be numeric)"
            print_message "❌ Invalid BIRDNET_LATITUDE or BIRDNET_LONGITUDE (must be numeric)" "$RED"
            exit 1
        fi
        sed -i "s|latitude: 00.000|latitude: $lat|" "$CONFIG_FILE"
        sed -i "s|longitude: 00.000|longitude: $lon|" "$CONFIG_FILE"
        # Also handle configs where location was previously set
        sed -i -E "s|^([[:space:]]*latitude:[[:space:]]*)[0-9.-]+|\\1$lat|" -- "$CONFIG_FILE"
        sed -i -E "s|^([[:space:]]*longitude:[[:space:]]*)[0-9.-]+|\\1$lon|" -- "$CONFIG_FILE"
        print_message "🔇 Silent mode: location set to $lat, $lon" "$YELLOW"
        return
    fi

    print_message "\n🌍 Location Configuration, this is used to limit bird species present in your region" "$GREEN"

    # Try to get location from NordVPN/OpenStreetMap
    local ip_location
    if ip_location=$(get_ip_location); then
        local ip_lat
        local ip_lon
        local ip_city
        local ip_country
        local ip_timezone
        ip_lat=$(echo "$ip_location" | cut -d'|' -f1)
        ip_lon=$(echo "$ip_location" | cut -d'|' -f2)
        ip_city=$(echo "$ip_location" | cut -d'|' -f3)
        ip_country=$(echo "$ip_location" | cut -d'|' -f4)
        ip_timezone=$(echo "$ip_location" | cut -d'|' -f5)
        
        log_message "INFO" "IP-based location detection successful: $ip_city, $ip_country (timezone: ${ip_timezone:-none})"
        
        # Display timezone info if available
        local location_msg="$ip_city, $ip_country ($ip_lat, $ip_lon)"
        if [ -n "$ip_timezone" ] && [ "$ip_timezone" != "null" ]; then
            location_msg="$location_msg [Timezone: $ip_timezone]"
        fi
        
        print_message "📍 Based on your IP address, your location appears to be: " "$YELLOW" "nonewline"
        print_message "$location_msg" "$NC"
        print_message "❓ Would you like to use this location? (y/n): " "$YELLOW" "nonewline"
        read -r use_ip_location
        
        if [[ $use_ip_location == "y" ]]; then
            lat=$ip_lat
            lon=$ip_lon
            log_message "INFO" "User accepted IP-based location ($ip_city, $ip_country)"
            # Store detected timezone globally for timezone configuration
            if [ -n "$ip_timezone" ] && [ "$ip_timezone" != "null" ]; then
                DETECTED_TZ="$ip_timezone"
                log_message "INFO" "Using detected timezone: $ip_timezone"
                print_message "✅ Using IP-based location and detected timezone: $ip_timezone" "$GREEN"
            else
                print_message "✅ Using IP-based location" "$GREEN"
            fi
            # Update config file and return (IP-derived values are numeric, safe for sed)
            sed -i "s|latitude: 00.000|latitude: $lat|" "$CONFIG_FILE"
            local sed_result=$?
            sed -i "s|longitude: 00.000|longitude: $lon|" "$CONFIG_FILE"
            sed_result=$((sed_result + $?))
            # Also handle configs where location was previously set
            sed -i -E "s|^([[:space:]]*latitude:[[:space:]]*)[0-9.-]+|\\1$lat|" -- "$CONFIG_FILE"
            sed -i -E "s|^([[:space:]]*longitude:[[:space:]]*)[0-9.-]+|\\1$lon|" -- "$CONFIG_FILE"
            log_command_result "sed latitude/longitude update" "$sed_result" "updating location coordinates in config file"
            return
        else
            log_message "INFO" "User rejected IP-based location, will configure manually"
        fi
    else
        log_message "WARN" "IP-based location detection failed"
        print_message "⚠️ Could not automatically determine location" "$YELLOW"
    fi
    
    # If automatic location failed or was rejected, continue with manual input
    print_message "1) Enter coordinates manually" "$YELLOW"
    print_message "2) Enter city name for OpenStreetMap lookup" "$YELLOW"
    print_message "3) Skip location configuration (use default: 0.0, 0.0)" "$YELLOW"
    
    while true; do
        print_message "❓ Select location input method (1-3): " "$YELLOW" "nonewline"
        read -r location_choice

        case $location_choice in
            1)
                while true; do
                    print_message "Enter latitude (-90 to 90) or 'b' to go back: " "$YELLOW" "nonewline"
                    read -r lat
                    
                    if [ "$lat" = "b" ]; then
                        break  # Go back to method selection
                    fi
                    
                    print_message "Enter longitude (-180 to 180) or 'b' to go back: " "$YELLOW" "nonewline"
                    read -r lon
                    
                    if [ "$lon" = "b" ]; then
                        break  # Go back to method selection
                    fi
                    
                    if [[ "$lat" =~ ^-?[0-9]*\.?[0-9]+$ ]] && \
                       [[ "$lon" =~ ^-?[0-9]*\.?[0-9]+$ ]] && \
                       (( $(echo "$lat >= -90 && $lat <= 90" | bc -l) )) && \
                       (( $(echo "$lon >= -180 && $lon <= 180" | bc -l) )); then
                        log_message "INFO" "User entered coordinates manually: $lat, $lon"
                        break 2  # Exit both loops
                    else
                        print_message "❌ Invalid coordinates. Please try again." "$RED"
                    fi
                done
                # If we get here, user chose 'b', so continue outer loop
                ;;
            2)
                while true; do
                    print_message "Enter location (e.g., 'Helsinki, Finland', 'New York, US') or 'b' to go back: " "$YELLOW" "nonewline"
                    read -r location
                    
                    if [ "$location" = "b" ]; then
                        break  # Go back to method selection
                    fi
                    
                    # Split input into city and country
                    city_raw=$(echo "$location" | cut -d',' -f1 | xargs)
                    country_raw=$(echo "$location" | cut -d',' -f2 | xargs)
                    city=$(printf '%s' "$city_raw" | jq -Rs '@uri')
                    country=$(printf '%s' "$country_raw" | jq -Rs '@uri')
                    
                    if [ -z "$city" ] || [ -z "$country" ]; then
                        print_message "❌ Invalid format. Please use format: 'City, Country'" "$RED"
                        continue
                    fi
                    
                    # Use OpenStreetMap Nominatim API to get coordinates
                    coordinates=$(curl -s "https://nominatim.openstreetmap.org/search?city=${city}&country=${country}&format=json" | jq -r '.[0] | "\(.lat) \(.lon)"')
                    
                    if [ -n "$coordinates" ] && [ "$coordinates" != "null null" ]; then
                        lat=$(echo "$coordinates" | cut -d' ' -f1)
                        lon=$(echo "$coordinates" | cut -d' ' -f2)
                        log_message "INFO" "OpenStreetMap lookup successful for $city, $country"
                        print_message "✅ Found coordinates for $city, $country: " "$GREEN" "nonewline"
                        print_message "$lat, $lon"
                        break 2  # Exit both loops
                    else
                        log_message "WARN" "OpenStreetMap lookup failed for: $city, $country"
                        print_message "❌ Could not find coordinates. Please try again with format: 'City, Country'" "$RED"
                    fi
                done
                # If we get here, user chose 'b', so continue outer loop
                ;;
            3)
                log_message "INFO" "User skipped location configuration"
                print_message "⚠️ Skipping location configuration - using default coordinates (0.0, 0.0)" "$YELLOW"
                print_message "💡 You can configure location later in the BirdNET-Go web interface" "$YELLOW"
                lat="0.0"
                lon="0.0"
                break
                ;;
            *)
                print_message "❌ Invalid selection. Please try again." "$RED"
                ;;
        esac
    done

    # Update config file (lat/lon are already validated as numeric above)
    log_message "INFO" "Location configured manually, updating config file"
    sed -i "s|latitude: 00.000|latitude: $lat|" "$CONFIG_FILE"
    local sed_result=$?
    sed -i "s|longitude: 00.000|longitude: $lon|" "$CONFIG_FILE"
    sed_result=$((sed_result + $?))
    # Also handle configs where location was previously set
    sed -i -E "s|^([[:space:]]*latitude:[[:space:]]*)[0-9.-]+|\\1$lat|" -- "$CONFIG_FILE"
    sed -i -E "s|^([[:space:]]*longitude:[[:space:]]*)[0-9.-]+|\\1$lon|" -- "$CONFIG_FILE"
    log_command_result "sed latitude/longitude update" "$sed_result" "updating location coordinates in config file"
}

# Function to configure basic authentication
configure_auth() {
    log_message "INFO" "Starting authentication configuration"

    # Silent mode: use BIRDNET_PASSWORD env var if set
    if [ "$SILENT_MODE" = "true" ]; then
        if [ -n "$BIRDNET_PASSWORD" ]; then
            local password_hash
            password_hash=$(echo -n "$BIRDNET_PASSWORD" | htpasswd -niB "" | cut -d: -f2)
            local escaped_hash
            escaped_hash=$(sed_escape_replacement "$password_hash")
            sed -i "s|enabled: false    # true to enable basic auth|enabled: true    # true to enable basic auth|" "$CONFIG_FILE"
            sed -i "s|password: \"\"|password: \"${escaped_hash}\"|" "$CONFIG_FILE"
            unset BIRDNET_PASSWORD password_hash escaped_hash
            print_message "🔇 Silent mode: password protection enabled" "$YELLOW"
        else
            print_message "🔇 Silent mode: no password set (BIRDNET_PASSWORD not provided)" "$YELLOW"
        fi
        return
    fi

    print_message "\n🔒 Security Configuration" "$GREEN"
    print_message "Do you want to enable password protection for the settings interface?" "$YELLOW"
    print_message "This is highly recommended if BirdNET-Go will be accessible from the internet." "$YELLOW"
    print_message "❓ Enable password protection? (y/n): " "$YELLOW" "nonewline"
    read -r enable_auth

    if [[ $enable_auth == "y" ]]; then
        log_message "INFO" "User enabled password protection"
        while true; do
            read -s -r -p "Enter password: " password
            printf '\n'
            read -s -r -p "Confirm password: " password2
            printf '\n'
            
            if [ "$password" = "$password2" ]; then
                log_message "INFO" "Password confirmed, generating hash and updating config"
                # Generate password hash (using bcrypt)
                password_hash=$(echo -n "$password" | htpasswd -niB "" | cut -d: -f2)
                local escaped_hash
                escaped_hash=$(sed_escape_replacement "$password_hash")

                # Update config file
                sed -i "s|enabled: false    # true to enable basic auth|enabled: true    # true to enable basic auth|" "$CONFIG_FILE"
                log_command_result "sed enable auth" $? "enabling authentication"
                sed -i "s|password: \"\"|password: \"${escaped_hash}\"|" "$CONFIG_FILE"
                log_command_result "sed password hash" $? "setting password hash"

                # Clear sensitive variables from shell memory
                unset password password2 password_hash escaped_hash

                log_message "INFO" "Password protection configured successfully"
                print_message "✅ Password protection enabled successfully!" "$GREEN"
                print_message "If you forget your password, you can reset it by editing:" "$YELLOW"
                print_message "$CONFIG_FILE" "$YELLOW"
                sleep 3
                break
            else
                log_message "WARN" "Password confirmation mismatch, retrying"
                print_message "❌ Passwords don't match. Please try again." "$RED"
            fi
        done
    else
        log_message "INFO" "User disabled password protection"
    fi
}

# Function to check if a port is in use
check_port_availability() {
    local port="$1"
    
    # Try multiple methods to ensure portability
    # Each method is independent - nc only short-circuits on positive detection
    
    # 1) Quick nc probe (IPv4 and IPv6); only short-circuit on positive detection
    if command_exists nc; then
        if nc -z -w1 127.0.0.1 "$port" 2>/dev/null || nc -z -w1 ::1 "$port" 2>/dev/null; then
            return 1 # Port is in use
        fi
    fi
    
    # 2) ss with sport filter (covers IPv4/IPv6 listeners)
    if command_exists ss; then
        if [ -n "$(ss -H -ltn "sport = :$port" 2>/dev/null)" ]; then
            return 1 # Port is in use
        else
            return 0 # Port is available
        fi
    fi
    
    # 3) lsof (explicit LISTEN)
    if command_exists lsof; then
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
            return 1 # Port is in use
        else
            return 0 # Port is available
        fi
    fi
    
    # 4) /dev/tcp fallback with timeout
    if timeout 1 bash -c "echo > /dev/tcp/127.0.0.1/$port" 2>/dev/null; then
        return 1 # Port is in use
    else
        return 0 # Port is available
    fi
}

# Function to get process information using a port
get_port_process_info() {
    local port="$1"
    local process_info
    local ss_output
    local proc_name
    
    # Try using ss first with headerless output and sport filter
    if command_exists ss; then
        # Use ss with headerless flag and sport filter
        ss_output=$(ss -H -tlnp "sport = :$port" 2>/dev/null)
        
        if [ -n "$ss_output" ]; then
            # Parse with awk instead of grep -P to avoid PCRE dependency
            # Extract process name from users field using awk
            proc_name=$(echo "$ss_output" | awk -F'"' '/users:/{print $2}' | head -1)
            
            # If no quotes, try alternative parsing
            if [ -z "$proc_name" ]; then
                proc_name=$(echo "$ss_output" | awk '/users:/{gsub(/.*users:\(\(/, ""); gsub(/,.*/, ""); gsub(/"/, ""); print}' | head -1)
            fi
            
            if [ -n "$proc_name" ]; then
                process_info="$proc_name"
            else
                # Check if port is listening but no process info available
                if echo "$ss_output" | grep -q "LISTEN"; then
                    process_info="(permission denied to get process name)"
                fi
            fi
        else
            # When ss -p produces no output (unprivileged), re-check without -p flag
            # to detect if port is actually listening
            ss_output=$(ss -H -tln "sport = :$port" 2>/dev/null)
            if [ -n "$ss_output" ]; then
                process_info="(permission denied to get process name)"
            fi
        fi
    fi
    
    # If ss didn't work, try lsof with explicit flags for safety
    if [ -z "$process_info" ] && command_exists lsof; then
        # Try without sudo first with explicit flags
        proc_name=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $1}' | head -1)
        
        if [ -z "$proc_name" ] && command_exists sudo; then
            # Only try with sudo if first attempt failed
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


# Repair configs from older installs that wrote a custom value into webserver.port. The
# container always listens on 8080 internally (the Docker image EXPOSEs 8080 and the
# entrypoint does not remap it), so the only valid internal value is 8080; the user's
# chosen port lives in the host-side mapping (-p WEB_PORT:8080) instead. Idempotent: only
# rewrites when the current value is not already 8080.
ensure_internal_port_8080() {
    [ -f "$CONFIG_FILE" ] || return 0
    local cur
    cur=$(sed -n -E '/^webserver:/,/^[A-Za-z0-9_]/ s/^[[:space:]]*port:[[:space:]]*"?([0-9]+)"?.*/\1/p' "$CONFIG_FILE" | head -1)
    if [ -n "$cur" ] && [ "$cur" != "8080" ]; then
        log_message "INFO" "Normalizing webserver.port from $cur to 8080 (container-internal port)"
        set_config_value webserver port '"8080"'
    fi
}

# Function to configure the web interface port (host-side mapping only)
configure_web_port() {
    # WEB_PORT is the HOST port that maps to the container's fixed internal 8080 port
    # (-p WEB_PORT:8080). We deliberately never change webserver.port in config so the
    # mapping target stays valid (older versions wrote the custom port into config, which
    # broke custom ports entirely, GH #3485). BIRDNET_WEB_PORT overrides / pre-seeds.
    WEB_PORT="${BIRDNET_WEB_PORT:-8080}"
    if ! [[ "$WEB_PORT" =~ ^[0-9]+$ ]] || [ "$WEB_PORT" -lt 1 ] || [ "$WEB_PORT" -gt 65535 ]; then
        log_message "WARN" "Invalid BIRDNET_WEB_PORT: $WEB_PORT, defaulting to 8080"
        WEB_PORT="8080"
    fi

    if [ "$SILENT_MODE" = "true" ]; then
        if ! check_port_availability "$WEB_PORT"; then
            local proc_info
            proc_info=$(get_port_process_info "$WEB_PORT")
            log_message "WARN" "Silent mode: web port $WEB_PORT is in use by $proc_info"
            print_message "⚠️ Silent mode: web port $WEB_PORT appears to be in use by: $proc_info" "$YELLOW"
        fi
        print_message "🔇 Silent mode: web interface port set to $WEB_PORT" "$YELLOW"
    else
        print_message "\n🌐 Web Interface Port" "$GREEN"
        print_message "BirdNET-Go's dashboard will be reachable on this host port (default 8080)." "$YELLOW"
        while true; do
            print_message "❓ Web interface port [${WEB_PORT}]: " "$YELLOW" "nonewline"
            if ! read -r port_input; then
                # EOF (stdin exhausted): keep the current default rather than looping forever.
                print_message "\nNo input; using port $WEB_PORT." "$YELLOW"
                break
            fi
            [ -z "$port_input" ] && port_input="$WEB_PORT"
            if ! [[ "$port_input" =~ ^[0-9]+$ ]] || [ "$port_input" -lt 1 ] || [ "$port_input" -gt 65535 ]; then
                print_message "❌ Invalid port. Enter a number between 1 and 65535." "$RED"
                continue
            fi
            if ! check_port_availability "$port_input"; then
                local proc_info
                proc_info=$(get_port_process_info "$port_input")
                print_message "⚠️ Port $port_input is already in use by: $proc_info" "$YELLOW"
                print_message "   Please choose a different port." "$YELLOW"
                continue
            fi
            WEB_PORT="$port_input"
            break
        done
        print_message "✅ Web interface port: $WEB_PORT" "$GREEN"
    fi

    # Repair configs from older versions that wrote a custom internal port.
    ensure_internal_port_8080
}

# Function to configure TLS / external-access mode for a fresh install. Decides whether the
# generated unit binds ports 80/443 (Let's Encrypt AutoTLS) and seeds the matching security
# settings (host/baseurl/autoTls). Three modes: direct access (default, no 80/443), Let's
# Encrypt AutoTLS, or behind a reverse proxy. Silent mode is driven by BIRDNET_ENABLE_AUTOTLS
# and BIRDNET_HOST.
configure_tls_access() {
    if [ "$SILENT_MODE" = "true" ]; then
        local want_autotls="${BIRDNET_ENABLE_AUTOTLS:-false}"
        local silent_host="${BIRDNET_HOST:-}"
        local silent_url="${BIRDNET_URL:-}"
        # Reject values containing characters that would produce a malformed YAML scalar
        # (the values are already sed-escaped, so this is robustness, not a security gate).
        if [ -n "$silent_host" ] && ! [[ "$silent_host" =~ ^[A-Za-z0-9.:/_-]+$ ]]; then
            log_message "WARN" "Silent mode: ignoring BIRDNET_HOST with invalid characters"
            print_message "⚠️ Silent mode: BIRDNET_HOST contains invalid characters; ignoring it" "$YELLOW"
            silent_host=""
        fi
        if [ -n "$silent_url" ] && ! [[ "$silent_url" =~ ^[A-Za-z0-9.:/_-]+$ ]]; then
            log_message "WARN" "Silent mode: ignoring BIRDNET_URL with invalid characters"
            silent_url=""
        fi
        if [ "$want_autotls" = "true" ]; then
            if [ -z "$silent_host" ]; then
                log_message "WARN" "Silent mode: BIRDNET_ENABLE_AUTOTLS=true but BIRDNET_HOST is unset/invalid; AutoTLS not enabled"
                print_message "⚠️ Silent mode: AutoTLS requires a valid BIRDNET_HOST; continuing without AutoTLS" "$YELLOW"
                BIND_TLS_PORTS="false"
                apply_tls_settings "direct" "" ""
            else
                BIND_TLS_PORTS="true"
                CONFIGURED_HOST="$silent_host"
                apply_tls_settings "autotls" "$CONFIGURED_HOST" ""
                print_message "🔇 Silent mode: AutoTLS enabled for host $CONFIGURED_HOST" "$YELLOW"
            fi
        elif [ -n "$silent_host" ]; then
            # Hostname set without AutoTLS implies a reverse-proxy / external hostname setup.
            BIND_TLS_PORTS="false"
            CONFIGURED_HOST="$silent_host"
            apply_tls_settings "proxy" "$CONFIGURED_HOST" "$silent_url"
            print_message "🔇 Silent mode: external host set to $CONFIGURED_HOST (no AutoTLS)" "$YELLOW"
        else
            BIND_TLS_PORTS="false"
            apply_tls_settings "direct" "" ""
        fi
        return 0
    fi

    print_message "\n🔐 Web Access / TLS Mode" "$GREEN"
    print_message "How will you reach the BirdNET-Go web interface?" "$YELLOW"
    print_message "  1) Direct access on the port above (default, recommended)" "$NC"
    print_message "  2) Let's Encrypt HTTPS (needs a public domain and ports 80+443)" "$NC"
    print_message "  3) Behind a reverse proxy (proxy handles HTTPS)" "$NC"
    local tls_choice
    while true; do
        print_message "❓ Select an option (1-3) [1]: " "$YELLOW" "nonewline"
        read -r tls_choice
        [ -z "$tls_choice" ] && tls_choice="1"
        case "$tls_choice" in
            1)
                BIND_TLS_PORTS="false"
                CONFIGURED_HOST=""
                apply_tls_settings "direct" "" ""
                print_message "✅ Direct access on port $WEB_PORT" "$GREEN"
                break
                ;;
            2)
                local host_input
                prompt_public_hostname "domain name (e.g. birdnet.example.com)"
                host_input="$PROMPTED_HOSTNAME"
                if [ -z "$host_input" ]; then
                    print_message "⚠️ A domain is required for Let's Encrypt; falling back to direct access" "$YELLOW"
                    BIND_TLS_PORTS="false"
                    CONFIGURED_HOST=""
                    apply_tls_settings "direct" "" ""
                    break
                fi
                BIND_TLS_PORTS="true"
                CONFIGURED_HOST="$host_input"
                apply_tls_settings "autotls" "$CONFIGURED_HOST" ""
                print_message "✅ Let's Encrypt AutoTLS enabled for $CONFIGURED_HOST (ports 80 and 443 will be published)" "$GREEN"
                print_message "   The domain must resolve to this machine and ports 80/443 must be reachable from the internet." "$YELLOW"
                break
                ;;
            3)
                local host_input
                prompt_public_hostname "external hostname or full URL (e.g. birdnet.example.com or https://birdnet.example.com)"
                host_input="$PROMPTED_HOSTNAME"
                BIND_TLS_PORTS="false"
                if [ -n "$host_input" ]; then
                    CONFIGURED_HOST="$host_input"
                    if [[ "$host_input" =~ ^https?:// ]]; then
                        apply_tls_settings "proxy" "" "$host_input"
                    else
                        apply_tls_settings "proxy" "$host_input" ""
                    fi
                    print_message "✅ Reverse-proxy mode: external address $host_input (BirdNET-Go will not bind 80/443)" "$GREEN"
                else
                    print_message "✅ Reverse-proxy mode (no external hostname set)" "$GREEN"
                fi
                break
                ;;
            *)
                print_message "❌ Invalid selection. Please choose 1, 2, or 3." "$RED"
                ;;
        esac
    done
}

# Prompt for and validate a hostname or URL. Echoes the validated value, or empty if the
# user entered nothing. Rejects values containing shell/sed metacharacters or whitespace.
prompt_public_hostname() {
    local what="$1"
    # Result is returned via the global PROMPTED_HOSTNAME rather than stdout, because the
    # prompts use print_message (which writes to stdout) and command substitution would
    # otherwise swallow the prompt text and capture it into the value.
    PROMPTED_HOSTNAME=""
    local value
    while true; do
        print_message "❓ Enter your $what: " "$YELLOW" "nonewline"
        read -r value
        if [ -z "$value" ]; then
            return 0
        fi
        # Allow letters, digits, dot, hyphen, colon, slash (for URLs); reject everything
        # else (spaces, quotes, $, &, |, backticks, etc.) to keep config edits safe.
        if [[ "$value" =~ ^[A-Za-z0-9.:/_-]+$ ]]; then
            PROMPTED_HOSTNAME="$value"
            return 0
        fi
        print_message "❌ Invalid characters. Use only letters, digits, dot, hyphen, and (for URLs) ://." "$RED"
    done
}

# Write the security settings for the chosen TLS mode. MODE is autotls, proxy, or direct.
# HOST sets security.host; URL (optional) sets security.baseurl. Uses block-scoped,
# escaped edits. autoTls (legacy) is migrated to tlsMode by the app on load; tlsMode is
# cleared defensively when present so reverse-proxy/direct modes are not overridden.
apply_tls_settings() {
    local mode="$1"
    local host="$2"
    local url="$3"
    # Each mode writes a complete, consistent set of security keys so switching modes never
    # leaves a stale value behind (a stale security.host breaks OAuth redirects and
    # notification URLs even when baseurl is set, and a stale autoTls regenerates a unit out
    # of sync with the app config).
    case "$mode" in
        autotls)
            set_config_value security host "$host"
            set_config_value security baseurl ""
            set_config_value security autoTls "true"
            set_config_value security redirecttohttps "true"
            set_config_value security tlsMode ""
            ;;
        proxy)
            # Behind a reverse proxy: no AutoTLS. When only a full URL was given, derive the
            # host from it so security.host (used independently of baseurl for OAuth and
            # notification URLs) is set rather than left stale.
            if [ -z "$host" ] && [ -n "$url" ]; then
                host="${url#*://}"
                host="${host%%/*}"
                host="${host%%:*}"
            fi
            set_config_value security host "$host"
            set_config_value security baseurl "$url"
            set_config_value security autoTls "false"
            set_config_value security redirecttohttps "false"
            set_config_value security tlsMode ""
            ;;
        direct)
            # Direct access: clear any stale AutoTLS / reverse-proxy settings so the app
            # config matches a unit that publishes only the web port.
            set_config_value security host ""
            set_config_value security baseurl ""
            set_config_value security autoTls "false"
            set_config_value security redirecttohttps "false"
            set_config_value security tlsMode ""
            ;;
    esac
}

# Ask whether to publish the Prometheus metrics endpoint on host port 8090. Off by default
# for fresh installs to avoid conflicts; BIRDNET_ENABLE_METRICS overrides in silent mode.
configure_metrics_exposure() {
    if [ "$SILENT_MODE" = "true" ]; then
        if [ "${BIRDNET_ENABLE_METRICS:-false}" = "true" ]; then
            BIND_METRICS_PORT="true"
            print_message "🔇 Silent mode: Prometheus metrics published on port 8090" "$YELLOW"
        else
            BIND_METRICS_PORT="false"
        fi
        return 0
    fi
    print_message "\n📊 Prometheus Metrics Endpoint" "$GREEN"
    print_message "Publish the metrics endpoint on host port 8090 for Prometheus scraping?" "$YELLOW"
    print_message "❓ Expose metrics on port 8090? (y/N): " "$YELLOW" "nonewline"
    local reply
    read -r reply
    if [[ "$reply" =~ ^[Yy]$ ]]; then
        if ! check_port_availability 8090; then
            local proc_info
            proc_info=$(get_port_process_info 8090)
            print_message "⚠️ Port 8090 is in use by: $proc_info. Metrics will not be published." "$YELLOW"
            BIND_METRICS_PORT="false"
        else
            BIND_METRICS_PORT="true"
            print_message "✅ Metrics endpoint will be published on port 8090" "$GREEN"
        fi
    else
        BIND_METRICS_PORT="false"
        print_message "✅ Metrics endpoint not published (still available inside the container)" "$GREEN"
    fi
}

# Generate systemd service content
generate_systemd_service_content() {
    # Use configured timezone if available, otherwise fall back to system timezone.
    # Mirror the multi-method detection from configure_timezone() so newer
    # systemd distributions without /etc/timezone (e.g. Debian 13) still resolve
    # the host zone instead of silently defaulting to UTC.
    # Resolve the host timezone via the shared resolver (Forgejo #877), preferring any
    # zone the user already configured. Falls back to UTC only when nothing valid can be
    # detected, so newer systemd distributions without /etc/timezone (e.g. Debian 13)
    # still resolve the host zone instead of silently defaulting to UTC.
    local TZ
    TZ=$(resolve_host_timezone "$CONFIGURED_TZ")
    if [ -z "$TZ" ]; then
        TZ="UTC"
    fi

    # Determine host UID/GID even when executed with sudo
    local HOST_UID=${SUDO_UID:-$(id -u)}
    local HOST_GID=${SUDO_GID:-$(id -g)}

    # Check for /dev/snd/
    local audio_env_line=""
    if check_directory_exists "/dev/snd/"; then
        audio_env_line="--device /dev/snd"
    fi

    # Check for /sys/class/thermal, used for Raspberry Pi temperature reporting in system dashboard
    local thermal_volume_line=""
    if check_directory_exists "/sys/class/thermal"; then
        thermal_volume_line="-v /sys/class/thermal:/sys/class/thermal"
    fi

    # External media mount: host /mnt/birdnet-go/external -> container /external
    # Uses rslave propagation; read-write is the Docker default and not specified explicitly.
    # Enables hot-plug of USB/SD/fileshare media mounted under the host directory.
    local external_media_line="-v /mnt/birdnet-go/external:/external:rslave"

    # Optional host-side port bindings. The web interface (WEB_PORT -> container 8080) is
    # always published. Ports 80/443 are only needed for Let's Encrypt AutoTLS and 8090
    # only for the Prometheus metrics endpoint; both are opt-in so a fresh install does not
    # collide with an existing web server. Existing installs keep whatever they already had
    # because load_existing_service_config() restores these flags from the current unit.
    # Any restored host bind address is reapplied so a localhost-only mapping survives a
    # regenerate (default is no address: published on all interfaces).
    local tls_ports_line=""
    if [ "$BIND_TLS_PORTS" = "true" ]; then
        tls_ports_line="-p ${TLS_BIND_ADDR:+${TLS_BIND_ADDR}:}80:80 \\
    -p ${TLS_BIND_ADDR:+${TLS_BIND_ADDR}:}443:443"
    fi
    local metrics_port_line=""
    if [ "$BIND_METRICS_PORT" = "true" ]; then
        metrics_port_line="-p ${METRICS_BIND_ADDR:+${METRICS_BIND_ADDR}:}8090:8090"
    fi

    # Check if running on Raspberry Pi and add WiFi power save disable script
    local wifi_power_save_script=""
    if is_raspberry_pi; then
        # Create the script that will be executed
        wifi_power_save_script="# Disable WiFi power saving on Raspberry Pi to prevent connection drops
ExecStartPre=/bin/bash -c 'for interface in /sys/class/net/wlan* /sys/class/net/wlp*; do if [ -d \"\$interface\" ]; then iface=\$(basename \"\$interface\"); (command -v iwconfig >/dev/null 2>&1 && iwconfig \"\$iface\" power off 2>/dev/null) || (command -v iw >/dev/null 2>&1 && iw dev \"\$iface\" set power_save off 2>/dev/null) || true; fi; done'"
    fi

    cat << EOF
[Unit]
Description=BirdNET-Go
After=docker.service
Requires=docker.service
RequiresMountsFor=${CONFIG_DIR}/hls

[Service]
Restart=always
# Remove any existing birdnet-go container to prevent name conflicts
ExecStartPre=-/usr/bin/docker rm -f birdnet-go
# Create tmpfs mount for HLS segments
ExecStartPre=/bin/mkdir -p ${CONFIG_DIR}/hls
# Mount tmpfs, the '|| true' ensures it doesn't fail if already mounted
ExecStartPre=/bin/sh -c 'mount -t tmpfs -o size=50M,mode=0755,uid=${HOST_UID},gid=${HOST_GID},noexec,nosuid,nodev tmpfs ${CONFIG_DIR}/hls || true'
# Prepare external media mount point and ensure shared propagation for hot-plug.
# These steps are best-effort (prefixed with '-'): the service still starts if
# they fail; only hot-plug sub-mount propagation is affected.
# The external mount is always added to the docker run command so the app can
# detect and guide the user when external media is present or absent.
ExecStartPre=-/bin/mkdir -p /mnt/birdnet-go/external
ExecStartPre=-/bin/sh -c 'mountpoint -q /mnt/birdnet-go/external || mount --bind /mnt/birdnet-go/external /mnt/birdnet-go/external'
ExecStartPre=-/bin/sh -c 'mount --make-rshared /mnt/birdnet-go/external'
# Make the mount point writable by the container user so the app and the
# upcoming backup feature can write to external media. -h avoids dereferencing
# a symlink. Best-effort like the steps above.
ExecStartPre=-/bin/chown -h ${HOST_UID}:${HOST_GID} /mnt/birdnet-go/external
${wifi_power_save_script:+${wifi_power_save_script}
}ExecStart=/usr/bin/docker run --rm \\
    --name birdnet-go \\
    -p ${WEB_PORT_BIND_ADDR:+${WEB_PORT_BIND_ADDR}:}${WEB_PORT}:8080 \\
${tls_ports_line:+    ${tls_ports_line} \\
}${metrics_port_line:+    ${metrics_port_line} \\
}
    --env TZ="${TZ}" \\
    --env BIRDNET_UID=${HOST_UID} \\
    --env BIRDNET_GID=${HOST_GID} \\
${audio_env_line:+    ${audio_env_line} \\
}    -v ${CONFIG_DIR}:/config \\
    -v ${DATA_DIR}:/data \\
${thermal_volume_line:+    ${thermal_volume_line} \\
}    ${external_media_line} \\
    ${BIRDNET_GO_IMAGE}
# Cleanup tasks on stop
ExecStopPost=/bin/sh -c 'umount -f ${CONFIG_DIR}/hls || true'
ExecStopPost=-/usr/bin/docker rm -f birdnet-go

[Install]
WantedBy=multi-user.target
EOF
}

# Extract the optional host bind address from a "-p [addr:]PORT:PORT" docker mapping (as
# produced by grep -oE). Echoes the address (e.g. 127.0.0.1 or [::1]) or an empty string.
_extract_bind_addr() {
    local map="$1"
    local portpair="$2"   # e.g. "443:443"
    local spec="${map#-p }"
    spec="${spec%"$portpair"}"
    spec="${spec%:}"
    printf '%s' "$spec"
}

# Restore the web port, AutoTLS/metrics port bindings, and timezone from an existing
# systemd unit so updates and reconfiguration preserve the user's prior choices instead of
# resetting to fresh-install defaults. This is the backward-compatibility guarantee: an
# unchanged update regenerates a byte-identical unit (check_systemd_service then reports no
# change). It must be called before check_systemd_service / add_systemd_config on the
# update and reconfigure paths. Sets globals WEB_PORT, BIND_TLS_PORTS, BIND_METRICS_PORT,
# and CONFIGURED_TZ.
# shellcheck disable=SC2120  # optional $1 (unit path) is intentional, used by tests
load_existing_service_config() {
    # Optional explicit unit path (used by tests); defaults to the installed locations.
    local service_file="${1:-}"
    if [ -z "$service_file" ]; then
        if [ -f "/etc/systemd/system/birdnet-go.service" ]; then
            service_file="/etc/systemd/system/birdnet-go.service"
        elif [ -f "/lib/systemd/system/birdnet-go.service" ]; then
            service_file="/lib/systemd/system/birdnet-go.service"
        fi
    fi
    [ -z "$service_file" ] && return 0

    # Web interface mapping: the one whose container side is 8080 (-p <host>:8080). The host
    # side may carry an optional bind address (e.g. 127.0.0.1:9000:8080 when a user manually
    # bound to localhost behind a same-host reverse proxy). Preserve the bind address as well
    # as the port so an update does not silently re-expose a localhost-only binding to all
    # interfaces. Each -p is on its own continuation line, so a per-line match is sufficient.
    local web_map
    web_map=$(grep -oE '\-p (\[[0-9a-fA-F:]+\]:|[0-9.]+:)?[0-9]+:8080' "$service_file" 2>/dev/null | head -1)
    if [ -n "$web_map" ]; then
        # Strip the leading "-p " and trailing ":8080", leaving "<addr>:<port>" or "<port>".
        local host_spec
        host_spec=$(printf '%s' "$web_map" | sed -E 's/^-p[[:space:]]+//; s/:8080$//')
        local host_port="$host_spec"
        local host_addr=""
        if [[ "$host_spec" == *:* ]]; then
            host_addr="${host_spec%:*}"
            host_port="${host_spec##*:}"
        fi
        if [[ "$host_port" =~ ^[0-9]+$ ]] && [ "$host_port" -ge 1 ] && [ "$host_port" -le 65535 ]; then
            WEB_PORT="$host_port"
            WEB_PORT_BIND_ADDR="$host_addr"
            if [ -n "$host_addr" ]; then
                log_message "INFO" "Restored web port mapping from existing service: ${host_addr}:${WEB_PORT}"
            else
                log_message "INFO" "Restored web port from existing service: $WEB_PORT"
            fi
        fi
    fi

    # Preserve the AutoTLS (80/443) and Prometheus metrics (8090) bindings if the existing
    # unit currently maps them, including any host bind address (e.g. 127.0.0.1) so a
    # localhost-only mapping is not silently re-exposed on all interfaces after an update.
    # NOTE: this is binding preservation, not feature detection, so an unchanged update keeps
    # exactly what the user already ran.
    local tls_map
    tls_map=$(grep -oE '\-p (\[[0-9a-fA-F:]+\]:|[0-9.]+:)?443:443' "$service_file" 2>/dev/null | head -1)
    if [ -n "$tls_map" ]; then
        BIND_TLS_PORTS="true"
        TLS_BIND_ADDR=$(_extract_bind_addr "$tls_map" "443:443")
    fi
    local metrics_map
    metrics_map=$(grep -oE '\-p (\[[0-9a-fA-F:]+\]:|[0-9.]+:)?8090:8090' "$service_file" 2>/dev/null | head -1)
    if [ -n "$metrics_map" ]; then
        BIND_METRICS_PORT="true"
        METRICS_BIND_ADDR=$(_extract_bind_addr "$metrics_map" "8090:8090")
    fi

    # Timezone, only if not already chosen this run.
    if [ -z "$CONFIGURED_TZ" ]; then
        local existing_tz
        existing_tz=$(sed -n 's/.*--env TZ="\([^"]*\)".*/\1/p' "$service_file" 2>/dev/null | head -1)
        if [ -n "$existing_tz" ]; then
            CONFIGURED_TZ="$existing_tz"
            log_message "INFO" "Restored timezone from existing service: $CONFIGURED_TZ"
        fi
    fi
}

# Function to check Cockpit installation status
check_cockpit_status() {
    local cockpit_status_file="$CONFIG_DIR/cockpit.txt"
    
    if [ -f "$cockpit_status_file" ]; then
        cat "$cockpit_status_file"
        return 0
    fi
    
    return 1
}

# Function to save Cockpit status
save_cockpit_status() {
    local status="$1"
    local cockpit_status_file="$CONFIG_DIR/cockpit.txt"
    
    echo "$status" > "$cockpit_status_file"
    log_message "INFO" "Cockpit status saved: $status"
}

# Function to check if Cockpit is already installed
is_cockpit_installed() {
    # Method 1: Check if cockpit packages are actually installed (not just config files remaining)
    # Check multiple common cockpit packages to be thorough
    local cockpit_packages=("cockpit" "cockpit-ws" "cockpit-bridge" "cockpit-system")
    for package in "${cockpit_packages[@]}"; do
        if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q "install ok installed"; then
            return 0
        fi
    done
    
    # Method 2: Check if cockpit-ws command exists and is executable
    if command_exists cockpit-ws && [ -x "$(command -v cockpit-ws)" ]; then
        return 0
    fi
    
    # Method 3: Check if cockpit bridge exists and is executable
    if command_exists cockpit-bridge && [ -x "$(command -v cockpit-bridge)" ]; then
        return 0
    fi
    
    # Method 4: Check if cockpit systemd unit files exist and are not masked
    if systemctl list-unit-files cockpit.socket 2>/dev/null | grep -E "cockpit\.socket\s+(enabled|disabled|static)" >/dev/null 2>&1; then
        # Double check that the unit file actually exists and is not just a leftover
        if [ -f "/lib/systemd/system/cockpit.socket" ] || [ -f "/etc/systemd/system/cockpit.socket" ]; then
            return 0
        fi
    fi
    
    return 1
}

# Function to check if Cockpit service is enabled and running
is_cockpit_running() {
    # First check if cockpit is actually installed before checking if it's running
    if ! is_cockpit_installed; then
        return 1
    fi
    
    # Check cockpit.socket first (preferred method) - ensure it's not masked
    if systemctl is-active --quiet cockpit.socket 2>/dev/null; then
        return 0
    fi
    
    # Check if cockpit.socket is masked (which means it was disabled/removed)
    if systemctl is-masked --quiet cockpit.socket 2>/dev/null; then
        return 1
    fi
    
    # Check cockpit.service as fallback 
    if systemctl is-active --quiet cockpit.service 2>/dev/null; then
        return 0
    fi
    
    # Check if cockpit is listening on port ${COCKPIT_PORT}
    if command_exists ss && ss -tlnp 2>/dev/null | grep -q ":${COCKPIT_PORT} "; then
        return 0
    fi
    
    # Fallback check with netstat
    if command_exists netstat && netstat -tln 2>/dev/null | grep -q ":${COCKPIT_PORT} "; then
        return 0
    fi
    
    return 1
}

# Function to clean up leftover cockpit systemd units
cleanup_cockpit_systemd() {
    log_message "INFO" "Cleaning up leftover Cockpit systemd units"
    
    # Unmask and disable any masked cockpit services
    if systemctl is-masked --quiet cockpit.socket 2>/dev/null; then
        log_message "INFO" "Unmasking cockpit.socket"
        sudo systemctl unmask cockpit.socket >/dev/null 2>&1 || true
    fi
    
    if systemctl is-masked --quiet cockpit.service 2>/dev/null; then
        log_message "INFO" "Unmasking cockpit.service" 
        sudo systemctl unmask cockpit.service >/dev/null 2>&1 || true
    fi
    
    # Reset any failed states
    sudo systemctl reset-failed cockpit.socket >/dev/null 2>&1 || true
    sudo systemctl reset-failed cockpit.service >/dev/null 2>&1 || true
    
    # Reload systemd to pick up any changes
    sudo systemctl daemon-reload >/dev/null 2>&1 || true
}

# Function to install cockpit with backports support
install_cockpit_with_backports() {
    local codename distro_id
    
    # Get distribution info from /etc/os-release
    if [ -f "/etc/os-release" ]; then
        distro_id=$(grep "^ID=" /etc/os-release 2>/dev/null | cut -d'=' -f2 | tr -d '"')
        codename=$(grep "^VERSION_CODENAME=" /etc/os-release 2>/dev/null | cut -d'=' -f2 | tr -d '"')
        # Fallback for Ubuntu
        [ -z "$codename" ] && codename=$(grep "^UBUNTU_CODENAME=" /etc/os-release 2>/dev/null | cut -d'=' -f2 | tr -d '"')
    fi
    
    if [ -z "$codename" ] || [ -z "$distro_id" ]; then
        log_message "WARN" "Could not detect distribution or codename, installing cockpit from main repository"
        sudo apt install -qq -y cockpit >/dev/null 2>&1
        return $?
    fi
    
    log_message "INFO" "Detected $distro_id with codename: $codename"
    
    case "$distro_id" in
        "debian")
            # Add Debian backports repository if not present
            local backports_file="/etc/apt/sources.list.d/backports.list"
            local backports_line="deb http://deb.debian.org/debian ${codename}-backports main"
            
            if [ ! -f "$backports_file" ] || ! grep -q "${codename}-backports" "$backports_file" 2>/dev/null; then
                log_message "INFO" "Adding Debian backports repository for $codename"
                echo "$backports_line" | sudo tee "$backports_file" >/dev/null 2>&1
                if [ $? -eq 0 ]; then
                    log_message "INFO" "Backports repository added, updating package lists"
                    sudo apt update -qq >/dev/null 2>&1
                else
                    log_message "ERROR" "Failed to add backports repository"
                fi
            else
                log_message "INFO" "Debian backports repository already configured"
            fi
            
            # Try installing from backports
            log_message "INFO" "Attempting to install cockpit from Debian ${codename}-backports"
            if sudo apt install -qq -y -t "${codename}-backports" cockpit; then
                log_message "INFO" "Cockpit installed successfully from Debian ${codename}-backports"
                return 0
            else
                log_message "WARN" "Backports installation failed for Debian, trying main repository"
            fi
            ;;
            
        "ubuntu")
            # Ubuntu has backports enabled by default
            log_message "INFO" "Attempting to install cockpit from Ubuntu ${codename}-backports"
            if sudo apt install -qq -y -t "${codename}-backports" cockpit; then
                log_message "INFO" "Cockpit installed successfully from Ubuntu ${codename}-backports"
                return 0
            else
                log_message "WARN" "Backports installation failed for Ubuntu, trying main repository"
            fi
            ;;
            
        *)
            log_message "INFO" "Unsupported distribution for backports: $distro_id, using main repository"
            ;;
    esac
    
    # Fallback to main repository
    log_message "INFO" "Installing cockpit from main repository"
    sudo apt install -qq -y cockpit
    return $?
}

# Function to configure Cockpit installation
configure_cockpit() {
    # Silent mode: skip cockpit installation
    if [ "$SILENT_MODE" = "true" ]; then
        log_message "INFO" "Silent mode: skipping Cockpit configuration"
        return
    fi

    log_message "INFO" "Starting Cockpit configuration check"
    
    # Debug: Log detection results for troubleshooting
    log_message "INFO" "Cockpit detection debug: installed=$(is_cockpit_installed && echo 'true' || echo 'false'), running=$(is_cockpit_running && echo 'true' || echo 'false')"
    
    # Clean up any leftover systemd state before proceeding
    if ! is_cockpit_installed && (systemctl is-masked --quiet cockpit.socket 2>/dev/null || systemctl is-masked --quiet cockpit.service 2>/dev/null); then
        log_message "INFO" "Cockpit not installed but systemd units are masked, cleaning up"
        cleanup_cockpit_systemd
    fi
    
    # STEP 1: Check if Cockpit is already installed on the system
    if is_cockpit_installed; then
        log_message "INFO" "Cockpit is already installed on system"
        
        # Check if it's running
        if is_cockpit_running; then
            print_message "✅ Cockpit system management interface is already installed and available at https://${IP_ADDR}:${COCKPIT_PORT}" "$GREEN"
            log_message "INFO" "Cockpit is installed and running, updating status file"
            save_cockpit_status "installed"
            return 0
        else
            # Cockpit is installed but not running
            print_message "📊 Cockpit is installed but not currently enabled" "$YELLOW"
            print_message "❓ Would you like to enable and start Cockpit? (y/n): " "$YELLOW" "nonewline"
            read -r enable_cockpit
            
            if [[ "$enable_cockpit" =~ ^[Yy]$ ]]; then
                log_message "INFO" "User chose to enable existing Cockpit installation"
                
                # Clean up any masked state first
                cleanup_cockpit_systemd
                
                if sudo systemctl enable --now cockpit.socket >/dev/null 2>&1; then
                    print_message "✅ Cockpit system management interface enabled and available at https://${IP_ADDR}:${COCKPIT_PORT}!" "$GREEN"
                    log_message "INFO" "Cockpit service enabled and started"
                    save_cockpit_status "installed"
                    return 0
                else
                    log_message "ERROR" "Failed to enable existing Cockpit service, may need reinstallation"
                    print_message "❌ Failed to enable Cockpit service - it may need to be reinstalled" "$RED"
                    print_message "💡 Try: sudo apt purge cockpit* && sudo apt autoremove" "$YELLOW"
                    print_message "   Then rerun this installer to install Cockpit fresh" "$YELLOW"
                    save_cockpit_status "install_failed"
                    return 1
                fi
            else
                print_message "ℹ️ Cockpit remains disabled" "$YELLOW"
                print_message "💡 To enable later, run: sudo systemctl enable --now cockpit.socket" "$YELLOW"
                log_message "INFO" "User declined to enable existing Cockpit installation"
                save_cockpit_status "declined"
                return 1
            fi
        fi
    fi
    
    # STEP 2: Cockpit is not installed - check user preferences from previous runs
    local cockpit_status
    if cockpit_status=$(check_cockpit_status); then
        case "$cockpit_status" in
            "declined")
                log_message "INFO" "User previously declined Cockpit installation, skipping prompt"
                print_message "📊 Cockpit installation was previously declined" "$YELLOW"
                return 1
                ;;
            "install_failed")
                log_message "INFO" "Previous Cockpit installation failed, asking user again"
                print_message "⚠️ Previous Cockpit installation failed, would you like to try again?" "$YELLOW"
                ;;
        esac
    fi
    
    # STEP 3: Ask user if they want to install Cockpit
    print_message "\n🖥️ System Management with Cockpit" "$GREEN"
    print_message "Cockpit is a web-based server management interface that provides:" "$YELLOW"
    print_message "  • System monitoring (CPU, memory, disk usage)" "$YELLOW"
    print_message "  • Service management" "$YELLOW"
    print_message "  • Log viewing" "$YELLOW"
    print_message "  • Terminal access" "$YELLOW"
    print_message "  • Network configuration" "$YELLOW"
    print_message "  • System package updates" "$YELLOW"
    print_message "  • Reboot/shutdown control" "$YELLOW"
    print_message "\nMore information: https://cockpit-project.org/" "$YELLOW"
    
    print_message "\n❓ Would you like to install Cockpit for easy system management? (y/n): " "$YELLOW" "nonewline"
    read -r install_cockpit
    
    if [[ "$install_cockpit" =~ ^[Yy]$ ]]; then
        log_message "INFO" "User chose to install Cockpit"
        print_message "\n📦 Installing Cockpit..." "$YELLOW"
        
        if install_cockpit_with_backports; then
            log_message "INFO" "Cockpit installation successful"
            
            # Enable and start Cockpit socket
            if sudo systemctl enable --now cockpit.socket; then
                log_message "INFO" "Cockpit service enabled and started"
                print_message "✅ Cockpit system management interface installed successfully!" "$GREEN"
                save_cockpit_status "installed"
                return 0
            else
                log_message "ERROR" "Failed to enable Cockpit service"
                print_message "❌ Failed to enable Cockpit service" "$RED"
                save_cockpit_status "install_failed"
                return 1
            fi
        else
            log_message "ERROR" "Cockpit installation failed"
            print_message "❌ Failed to install Cockpit" "$RED"
            print_message "💡 To install Cockpit manually, try: sudo apt install cockpit" "$YELLOW"
            print_message "   Then enable it with: sudo systemctl enable --now cockpit.socket" "$YELLOW"
            save_cockpit_status "install_failed"
            return 1
        fi
    else
        log_message "INFO" "User declined Cockpit installation"
        print_message "ℹ️ Cockpit installation skipped" "$YELLOW"
        print_message "💡 To install Cockpit later, try: sudo apt install cockpit" "$YELLOW"
        print_message "   Then enable it with: sudo systemctl enable --now cockpit.socket" "$YELLOW"
        save_cockpit_status "declined"
        return 1
    fi
}

# Function to add systemd service configuration
add_systemd_config() {
    # Create systemd service
    print_message "\n🚀 Creating systemd service..." "$GREEN"
    sudo tee /etc/systemd/system/birdnet-go.service << EOF
$(generate_systemd_service_content)
EOF

    # Reload systemd and enable service
    sudo systemctl daemon-reload

    # Validate the generated unit before enabling it so a malformed unit surfaces clearly
    # here instead of failing silently at start time (GH #1832). Only a non-zero exit
    # (an actual parse error) is treated as a problem; benign advisory warnings are logged.
    if command_exists systemd-analyze; then
        local verify_out verify_rc
        verify_out=$(systemd-analyze verify /etc/systemd/system/birdnet-go.service 2>&1)
        verify_rc=$?
        # In minimal chroots/containers systemd-analyze cannot reach the system bus and exits
        # non-zero with a bus-connection error even for a valid unit; do not warn on that.
        if [ "$verify_rc" -ne 0 ] && ! echo "$verify_out" | grep -qiE "Failed to connect to bus|Connection refused|No such file or directory.*bus"; then
            log_message "WARN" "systemd-analyze verify reported errors (rc=$verify_rc): $verify_out"
            print_message "⚠️ systemd reported problems with the generated service unit:" "$YELLOW"
            print_message "$verify_out" "$YELLOW"
            print_message "   Continuing, but this is the likely cause if the service fails to start." "$YELLOW"
        else
            log_message "INFO" "systemd-analyze verify passed for birdnet-go.service"
        fi
    fi

    sudo systemctl enable birdnet-go.service
}

# Function to check if systemd service file needs update
check_systemd_service() {
    local service_file="/etc/systemd/system/birdnet-go.service"
    local temp_service_file="/tmp/birdnet-go.service.new"
    local needs_update=false
    
    # Create temporary service file with current configuration
    generate_systemd_service_content > "$temp_service_file"

    # Check if service file exists and compare
    if [ -f "$service_file" ]; then
        if ! cmp -s "$service_file" "$temp_service_file"; then
            needs_update=true
        fi
    else
        needs_update=true
    fi
    
    rm -f "$temp_service_file"
    echo "$needs_update"
}

# Function to check if BirdNET container is running
check_container_running() {
    if command_exists docker && safe_docker ps | grep -q "birdnet-go"; then
        return 0  # Container is running
    else
        return 1  # Container is not running
    fi
}

# Function to show service diagnostics
show_service_diagnostics() {
    print_message "\n📋 BirdNET-Go Service Diagnostics" "$GREEN"
    print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$GRAY"

    # Service status (only if systemd is available)
    if command_exists systemctl; then
        if systemctl is-active --quiet birdnet-go.service 2>/dev/null; then
            print_message "✅ Service Status: Running" "$GREEN"
        else
            print_message "❌ Service Status: Not Running" "$RED"

            # Only show logs if journalctl is available
            if command_exists journalctl; then
                print_message "\n📄 Last 30 log lines:" "$YELLOW"
                journalctl -u birdnet-go.service -n 30 --no-pager 2>/dev/null || echo "Unable to retrieve logs"

                print_message "\n💡 To view live logs, run:" "$YELLOW"
                print_message "   journalctl -u birdnet-go.service -f" "$NC"
            fi
        fi
    else
        print_message "⚠️  systemd not available - cannot check service status" "$YELLOW"
    fi

    # Container status (only if Docker is available)
    if command_exists docker; then
        print_message "\n🐳 Docker Container Status:" "$YELLOW"
        safe_docker ps -a --filter "name=birdnet-go" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || echo "Unable to retrieve container status"
    else
        print_message "\n⚠️  Docker not available - cannot check container status" "$YELLOW"
    fi

    # Disk space (only if DATA_DIR is set)
    if [ -n "$DATA_DIR" ]; then
        print_message "\n💾 Disk Space:" "$YELLOW"
        print_message "Data directory: $DATA_DIR" "$NC"
        df -h "$DATA_DIR" 2>/dev/null | tail -1 || echo "Unable to check disk space"
    else
        print_message "\n⚠️  Data directory not configured - cannot check disk space" "$YELLOW"
    fi

    # If service failed, show prominent error (only if systemd is available)
    if command_exists systemctl && systemctl is-failed --quiet birdnet-go.service 2>/dev/null; then
        print_message "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$RED"
        print_message "⚠️  SERVICE FAILED TO START" "$RED"
        print_message "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" "$RED"
        print_message "\nTo restart: sudo systemctl restart birdnet-go.service" "$YELLOW"
        if command_exists journalctl; then
            print_message "View logs:  sudo journalctl -u birdnet-go.service -n 50" "$YELLOW"
        fi
    fi
}

# Function to get all BirdNET containers (including stopped ones)
get_all_containers() {
    if command_exists docker; then
        safe_docker ps -a --filter name=birdnet-go -q
    else
        echo ""
    fi
}

# Function to stop BirdNET service and container
stop_birdnet_service() {
    local wait_for_stop=${1:-true}
    local max_wait=${2:-30}
    
    print_message "🛑 Stopping BirdNET-Go service..." "$YELLOW"
    sudo systemctl stop birdnet-go.service
    
    # Wait for container to stop if requested
    if [ "$wait_for_stop" = true ] && check_container_running; then
        local waited=0
        while check_container_running && [ "$waited" -lt "$max_wait" ]; do
            sleep 1
            ((waited++))
        done
        
        if check_container_running; then
            print_message "⚠️ Container still running after $max_wait seconds, forcing stop..." "$YELLOW"
            get_all_containers | xargs -r docker stop
        fi
    fi
}

# Function to handle container update process
handle_container_update() {
    log_message "INFO" "=== Starting Container Update Process ==="
    
    # Log comprehensive pre-update state
    log_message "INFO" "=== Pre-Update System State ==="
    log_system_resources "pre-update"
    log_docker_state "pre-update"
    log_service_state "pre-update"
    log_network_state "pre-update"
    
    # Store pre-update config hash
    local pre_update_config_hash=""
    if [ -f "$CONFIG_FILE" ]; then
        pre_update_config_hash=$(log_config_hash "pre-update")
        log_message "INFO" "Pre-update config file backup hash recorded"
    fi
    
    # Restore the user's existing port/TLS/metrics/timezone choices from the current unit
    # BEFORE comparing or regenerating it, so the update preserves them instead of resetting
    # to fresh-install defaults (GH #3485 ports clobbered on update, #3238 timezone).
    load_existing_service_config
    if [ -n "$CONFIGURED_TZ" ]; then
        print_message "📍 Using existing timezone configuration: $CONFIGURED_TZ" "$GREEN"
    fi

    local service_needs_update
    service_needs_update=$(check_systemd_service)

    log_message "INFO" "Systemd service update needed: $service_needs_update"
    print_message "🔄 Checking for updates..." "$YELLOW"

    # Stop the service and container
    log_message "INFO" "Stopping BirdNET-Go service for update"
    stop_birdnet_service
    
    # Clean up existing tmpfs mounts
    log_message "INFO" "Cleaning up tmpfs mounts"
    cleanup_hls_mount
    
    # Update configuration paths
    log_message "INFO" "Updating configuration paths"
    update_paths_in_config

    # Repair a stale custom webserver.port from an older broken install so the host-side
    # mapping (always :8080) keeps working after the update.
    ensure_internal_port_8080

    # Capture current version before update
    log_message "INFO" "Capturing current image hash before update"
    print_message "📸 Capturing current version for rollback..." "$YELLOW"
    local current_image_hash
    current_image_hash=$(capture_current_image_hash "pre-update")
    
    # Create config backup with current version
    if [ -f "$CONFIG_FILE" ] && [ -n "$current_image_hash" ]; then
        log_message "INFO" "Creating config backup before update"
        backup_config_with_version "pre-update" "$current_image_hash"
    fi
    
    # Pull new image
    log_message "INFO" "Pulling Docker image: $BIRDNET_GO_IMAGE"
    print_message "📥 Pulling image: $BIRDNET_GO_VERSION..." "$YELLOW"
    if ! docker pull "${BIRDNET_GO_IMAGE}"; then
        log_message "ERROR" "Failed to pull Docker image during update: $BIRDNET_GO_IMAGE"
        print_message "❌ Failed to pull image: $BIRDNET_GO_VERSION" "$RED"
        return 1
    fi
    log_message "INFO" "Docker image pull completed successfully"
    
    # MODIFIED: Always ensure AUDIO_ENV is set during updates
    if [ -z "$AUDIO_ENV" ]; then
        AUDIO_ENV="--device /dev/snd"
    fi
    
    # Update systemd service if needed
    if [ "$service_needs_update" = "true" ]; then
        log_message "INFO" "Updating systemd service configuration"
        print_message "📝 Updating systemd service..." "$YELLOW"
        add_systemd_config
    else
        log_message "INFO" "Systemd service configuration up to date, no changes needed"
    fi
    
    # Start the service
    log_message "INFO" "Starting BirdNET-Go service after update"
    print_message "🚀 Starting BirdNET-Go service..." "$YELLOW"
    sudo systemctl daemon-reload
    log_command_result "systemctl daemon-reload" $? "reloading systemd configuration"
    if ! sudo systemctl start birdnet-go.service; then
        log_message "ERROR" "Failed to start BirdNET-Go service after update"
        print_message "❌ Failed to start service" "$RED"
        return 1
    fi
    log_message "INFO" "BirdNET-Go service started successfully after update"
    
    # Post-update validation and logging
    log_message "INFO" "=== Post-Update Validation ==="
    
    # Verify config file integrity
    if [ -f "$CONFIG_FILE" ]; then
        local post_update_config_hash
        post_update_config_hash=$(log_config_hash "post-update")
        
        if [ -n "$pre_update_config_hash" ] && [ "$pre_update_config_hash" = "$post_update_config_hash" ]; then
            log_message "INFO" "Config file integrity verified: hash unchanged"
        elif [ -n "$pre_update_config_hash" ] && [ "$pre_update_config_hash" != "$post_update_config_hash" ]; then
            log_message "WARN" "Config file hash changed during update (expected for some updates)"
            log_message "INFO" "Pre-update hash: $pre_update_config_hash"
            log_message "INFO" "Post-update hash: $post_update_config_hash"
        else
            log_message "INFO" "Config file hash recorded post-update"
        fi
    fi
    
    # Log post-update system state
    log_system_resources "post-update"
    log_docker_state "post-update"
    log_service_state "post-update"
    
    # Verify service is responding
    local service_responsive="false"
    if systemctl is-active --quiet birdnet-go.service; then
        # Give service a moment to fully initialize
        sleep 2
        # Check if web interface is responding
        if curl -s -f --connect-timeout 5 "http://localhost:${WEB_PORT:-8080}" >/dev/null 2>&1; then
            service_responsive="true"
            log_message "INFO" "Web interface responding on port ${WEB_PORT:-8080}"
        else
            log_message "WARN" "Web interface not responding on port ${WEB_PORT:-8080} (may still be starting)"
        fi
    else
        log_message "ERROR" "Service not active after update"
    fi
    
    log_message "INFO" "Update validation completed - service responsive: $service_responsive"

    # Confirm BirdNET-Go is actually answering (and AutoTLS ports if previously enabled)
    verify_post_start

    log_message "INFO" "Container update process completed successfully"
    print_message "✅ Update completed successfully" "$GREEN"
    
    # Send upgrade completion telemetry with context
    local system_info
    system_info=$(collect_system_info)
    local os_name=$(echo "$system_info" | jq -r '.os_name' 2>/dev/null || echo "unknown")
    local pi_model=$(echo "$system_info" | jq -r '.pi_model' 2>/dev/null || echo "none")
    local cpu_arch=$(echo "$system_info" | jq -r '.cpu_arch' 2>/dev/null || echo "unknown")
    
    send_telemetry_event "info" "Upgrade completed successfully" "info" "step=handle_container_update,type=upgrade,os=${os_name},pi_model=${pi_model},arch=${cpu_arch},service_updated=${service_needs_update}"
    
    return 0
}

# Function to clean existing installation but preserve user data
disable_birdnet_service_and_remove_containers() {
    # Stop and disable the service fully, then remove any unit files and drop-ins
    sudo systemctl stop birdnet-go.service 2>/dev/null || true
    sudo systemctl disable --now birdnet-go.service 2>/dev/null || true
    # Remove unit file and any leftover symlinks
    sudo rm -f /etc/systemd/system/birdnet-go.service
    sudo rm -f /etc/systemd/system/multi-user.target.wants/birdnet-go.service
    # Also remove any system-installed unit and its drop-in directory
    sudo rm -f /lib/systemd/system/birdnet-go.service
    sudo rm -rf /etc/systemd/system/birdnet-go.service.d
    # Reload systemd and clear any failed state
    sudo systemctl daemon-reload
    sudo systemctl reset-failed birdnet-go.service 2>/dev/null || true
    print_message "✅ Removed systemd service" "$GREEN"

    # Stop and remove containers
    if docker ps -a | grep -q "birdnet-go"; then
        print_message "🛑 Stopping and removing BirdNET-Go containers..." "$YELLOW"
        get_all_containers | xargs -r docker stop
        get_all_containers | xargs -r docker rm
        print_message "✅ Removed containers" "$GREEN"
    fi

    # Remove images
    # Remove images by repository base name (including untagged)
    image_base="${BIRDNET_GO_IMAGE%:*}"
    images_to_remove=$(docker images "${image_base}" -q)
    if [ -n "${images_to_remove}" ]; then
        print_message "🗑️ Removing BirdNET-Go images..." "$YELLOW"
        echo "${images_to_remove}" | xargs -r docker rmi -f
        print_message "✅ Removed images" "$GREEN"
    fi
}

clean_installation_preserve_data() {
    print_message "🧹 Cleaning BirdNET-Go installation (preserving user data)..." "$YELLOW"
    # First ensure any service is stopped
    stop_birdnet_service false
    # Clean up tmpfs mounts before removing service
    cleanup_hls_mount
    # Remove service and containers
    disable_birdnet_service_and_remove_containers
    print_message "✅ BirdNET-Go uninstalled, user data preserved in $CONFIG_DIR and $DATA_DIR" "$GREEN"
    return 0
}

# Function to clean existing installation
clean_installation() {
    print_message "🧹 Cleaning existing installation..." "$YELLOW"
    
    # First ensure any service is stopped
    stop_birdnet_service false
    # Clean up tmpfs mounts before attempting to remove directories
    cleanup_hls_mount
    # Remove service and containers
    disable_birdnet_service_and_remove_containers
    
    # Unified directory removal with simplified error handling
    if [ -d "$CONFIG_DIR" ] || [ -d "$DATA_DIR" ]; then
        print_message "📁 Removing data directories..." "$YELLOW"
        
        # Create a list of errors
        local error_list=""
        
        # Try to remove directories with regular permissions first
        rm -rf "$CONFIG_DIR" "$DATA_DIR" 2>/dev/null || {
            # If that fails, try with sudo
            print_message "⚠️ Some files require elevated permissions to remove, trying with sudo..." "$YELLOW"
            sudo rm -rf "$CONFIG_DIR" "$DATA_DIR" 2>/dev/null || {
                # If sudo also fails, collect error information
                print_message "❌ Some files could not be removed even with sudo" "$RED"
                
                # Check which directories still exist and list problematic files
                for dir in "$CONFIG_DIR" "$DATA_DIR"; do
                    if [ -d "$dir" ]; then
                        error_list="${error_list}Files in $dir:\n"
                        while IFS= read -r file; do
                            error_list="${error_list}  • $file\n"
                        done < <(find "$dir" -type f 2>/dev/null)
                    fi
                done
            }
        }
        
        # Show error list if there were problems
        if [ -n "$error_list" ]; then
            print_message "The following files could not be removed:" "$RED"
            printf '%b' "$error_list"
            print_message "\n⚠️ Some cleanup operations failed" "$RED"
            print_message "You may need to manually remove remaining files" "$YELLOW"
            return 1
        else
            print_message "✅ Removed data directories" "$GREEN"

            # Remove parent directory if empty
            local parent_dir="$HOME/birdnet-go-app"
            if [ -d "$parent_dir" ]; then
                if [ -z "$(ls -A "$parent_dir" 2>/dev/null)" ]; then
                    rm -rf "$parent_dir" 2>/dev/null || sudo rm -rf "$parent_dir"
                    print_message "✅ Removed parent directory" "$GREEN"
                fi
            fi
        fi
    fi
    
    print_message "✅ Cleanup completed successfully" "$GREEN"
    return 0
}

# Function to start BirdNET-Go
start_birdnet_go() {   
    log_message "INFO" "Starting BirdNET-Go service"
    print_message "\n🚀 Starting BirdNET-Go..." "$GREEN"
    
    # Check if we need to restart due to image change or just start
    local action="start"
    local action_msg="Starting"
    
    if check_container_running; then
        if [ "$IMAGE_CHANGED" = "true" ]; then
            log_message "INFO" "Container running but Docker image changed, restarting to use new image"
            print_message "🔄 Docker image updated - restarting container to use new version..." "$YELLOW"
            action="restart"
            action_msg="Restarting"
        else
            log_message "INFO" "BirdNET-Go container already running, no image changes detected"
            print_message "✅ BirdNET-Go container is already running" "$GREEN"
            return 0
        fi
    else
        log_message "INFO" "Container not running, starting service"
    fi
    
    # Start or restart the service
    log_message "INFO" "Executing systemctl $action birdnet-go.service"
    local systemctl_exit_code=0
    sudo systemctl $action birdnet-go.service
    systemctl_exit_code=$?
    log_command_result "systemctl $action birdnet-go.service" $systemctl_exit_code "${action_msg} BirdNET-Go service"

    # Check if service started
    if ! sudo systemctl is-active --quiet birdnet-go.service; then
        log_message "ERROR" "BirdNET-Go service failed to start"

        # Collect comprehensive service diagnostics
        local service_status=$(systemctl status birdnet-go.service 2>&1 | sed 's/"/\\"/g' | tr '\n' ';')
        local service_logs=$(journalctl -u birdnet-go.service -n 50 --no-pager 2>&1 | sed 's/"/\\"/g' | tr '\n' ';')
        local service_enabled=$(systemctl is-enabled birdnet-go.service 2>&1 || echo "unknown")
        local docker_running=$(docker ps --format "{{.Names}}: {{.Status}}" 2>&1 | sed 's/"/\\"/g' | tr '\n' ';')
        local docker_errors=$(docker ps -a --filter "status=exited" --format "{{.Names}}: {{.Status}}" 2>&1 | sed 's/"/\\"/g' | tr '\n' ';')

        # Extract structured error information
        local error_type="unknown"
        local error_detail=""

        # Check for common failure patterns in logs
        if echo "$service_logs" | grep -qi "bind: address already in use"; then
            error_type="port_conflict"
            error_detail=$(echo "$service_logs" | grep -oP "port \d+" | head -1 || echo "port conflict detected")
        elif echo "$service_logs" | grep -qi "permission denied"; then
            error_type="permission_denied"
            error_detail=$(echo "$service_logs" | grep -i "permission denied" | head -1 | sed 's/"/\\"/g' | head -c 200)
        elif echo "$service_logs" | grep -qi "No such image\|image not found"; then
            error_type="image_missing"
            error_detail="$BIRDNET_GO_IMAGE"
        elif echo "$service_logs" | grep -qi "OOMKilled\|Out of memory"; then
            error_type="out_of_memory"
            error_detail="Container killed due to memory exhaustion"
        elif echo "$service_logs" | grep -qi "container .* is already in use\|name.*already in use"; then
            error_type="container_name_conflict"
            error_detail="Container name 'birdnet-go' already exists"
        elif echo "$service_logs" | grep -qi "timeout\|timed out"; then
            error_type="timeout"
            error_detail="Service startup timeout"
        elif echo "$service_logs" | grep -qi "failed to create endpoint\|network"; then
            error_type="network_error"
            error_detail=$(echo "$service_logs" | grep -i "network\|endpoint" | head -1 | sed 's/"/\\"/g' | head -c 200)
        elif echo "$service_logs" | grep -qi "acme\|certificate\|tls\|let.?s.?encrypt"; then
            error_type="tls_error"
            error_detail=$(echo "$service_logs" | grep -iE "acme|certificate|tls" | head -1 | sed 's/"/\\"/g' | head -c 200)
        fi

        # If no known pattern matched, still capture the most relevant log line so the
        # report is actionable instead of a bare "unknown" (Forgejo #350).
        if [ "$error_type" = "unknown" ]; then
            error_detail=$(echo "$service_logs" | grep -oiE '(error|fatal|panic|failed)[^;]*' | tail -1 | sed 's/"/\\"/g' | head -c 200)
            [ -z "$error_detail" ] && error_detail="No recognized error pattern; see the service logs below"
        fi

        # Get container-specific logs if container exists (even if exited)
        local container_logs="none"
        local container_exit_code="unknown"
        local container_id
        container_id=$(docker ps -a --filter "name=birdnet-go" --format "{{.ID}}" 2>/dev/null | head -1)

        if [ -n "$container_id" ]; then
            container_logs=$(docker logs --tail 30 "$container_id" 2>&1 | sed 's/"/\\"/g' | tr '\n' ';' | tail -c "$MAX_LOG_LENGTH")
            container_exit_code=$(docker inspect "$container_id" --format='{{.State.ExitCode}}' 2>/dev/null || echo "unknown")
        fi

        # Check for resource constraints
        local disk_full="false"
        local memory_available="unknown"
        local docker_space="unknown"

        # Check disk space on critical paths
        if [ -d "$CONFIG_DIR" ] && [ "$(df --output=pcent "$CONFIG_DIR" 2>/dev/null | tail -1 | tr -d '% ' || echo 0)" -gt 95 ]; then
            disk_full="config_dir"
        elif [ -d "/var/lib/docker" ] && [ "$(df --output=pcent /var/lib/docker 2>/dev/null | tail -1 | tr -d '% ' || echo 0)" -gt 95 ]; then
            disk_full="docker_dir"
        fi

        memory_available=$(free -m 2>/dev/null | awk 'NR==2 {print $7}' || echo "unknown")
        docker_space=$(df -h /var/lib/docker 2>/dev/null | awk 'NR==2 {print $4}' || echo "unknown")

        # Verify the Docker image
        local image_exists="false"
        local image_size="unknown"
        if docker inspect "$BIRDNET_GO_IMAGE" >/dev/null 2>&1; then
            image_exists="true"
            image_size=$(docker inspect "$BIRDNET_GO_IMAGE" --format='{{.Size}}' 2>/dev/null | awk '{printf "%.1f MB", $1/1024/1024}' || echo "unknown")
        fi

        # Check config file validity
        local config_valid="unknown"
        local config_error="none"
        local config_exists="false"

        if [ -f "$CONFIG_FILE" ]; then
            config_exists="true"
            # Basic YAML syntax check if available
            if command -v yamllint >/dev/null 2>&1; then
                config_error=$(yamllint "$CONFIG_FILE" 2>&1 | head -c 300)
                [ $? -eq 0 ] && config_valid="true" || config_valid="false"
            fi
        fi

        # Check for port conflicts
        local port_conflicts=()
        for port in 80 443 "${WEB_PORT:-8080}" 8090; do
            if ! check_port_availability "$port" 2>/dev/null; then
                local proc_info
                proc_info=$(get_port_process_info "$port" 2>/dev/null)
                port_conflicts+=("\"$port:$(echo "$proc_info" | sed 's/"/\\"/g')\"")
            fi
        done

        # Build comprehensive diagnostic JSON using jq for safety
        local diagnostic_json
        diagnostic_json=$(jq -n \
            --arg exit_code "$systemctl_exit_code" \
            --arg error_type "$error_type" \
            --arg error_detail "$error_detail" \
            --arg service_status "$(echo "$service_status" | tail -c 500)" \
            --arg service_enabled "$service_enabled" \
            --arg service_logs "$(echo "$service_logs" | tail -c 800)" \
            --arg action "$action" \
            --arg container_id "${container_id:-none}" \
            --arg container_logs "$container_logs" \
            --arg container_exit_code "$container_exit_code" \
            --arg docker_running "$(echo "$docker_running" | head -c 300)" \
            --arg docker_errors "$(echo "$docker_errors" | head -c 300)" \
            --arg image_tag "${BIRDNET_GO_IMAGE}" \
            --arg image_exists "$image_exists" \
            --arg image_size "$image_size" \
            --arg image_changed "${IMAGE_CHANGED}" \
            --arg disk_full "$disk_full" \
            --arg memory_available "$memory_available" \
            --arg docker_space "$docker_space" \
            --arg config_exists "$config_exists" \
            --arg config_valid "$config_valid" \
            --arg config_error "$config_error" \
            --arg web_port "${WEB_PORT:-8080}" \
            --argjson port_conflicts "[$(IFS=,; echo "${port_conflicts[*]}")]" \
            '{
                error_analysis: {
                    exit_code: ($exit_code | tonumber),
                    error_type: $error_type,
                    error_detail: $error_detail
                },
                service: {
                    status: $service_status,
                    enabled: $service_enabled,
                    logs: $service_logs,
                    action_attempted: $action
                },
                container: {
                    id: $container_id,
                    logs: $container_logs,
                    exit_code: $container_exit_code,
                    running_containers: $docker_running,
                    exited_containers: $docker_errors
                },
                image: {
                    tag: $image_tag,
                    exists: $image_exists,
                    size: $image_size,
                    changed: $image_changed
                },
                resources: {
                    disk_full: $disk_full,
                    memory_available_mb: $memory_available,
                    docker_space: $docker_space
                },
                config: {
                    file_exists: $config_exists,
                    valid: $config_valid,
                    error: $config_error
                },
                ports: {
                    web_port: $web_port,
                    conflicts: $port_conflicts
                }
            }')

        send_telemetry_event "error" "Service startup failed: $error_type" "error" "step=start_birdnet_go,error_type=$error_type" "$diagnostic_json"
        print_message "❌ Failed to start BirdNET-Go service" "$RED"

        # Surface a concrete summary even when the error type is unknown (Forgejo #350): the
        # detected cause and the container exit code are the most actionable details and
        # should never be omitted just because no known pattern matched.
        print_message "   Detected cause: $error_type" "$YELLOW"
        if [ -n "$error_detail" ]; then
            print_message "   Detail: $error_detail" "$YELLOW"
        fi
        if [ -n "$container_exit_code" ] && [ "$container_exit_code" != "unknown" ]; then
            print_message "   Container exit code: $container_exit_code" "$YELLOW"
        fi

        # Get and display journald logs for troubleshooting
        log_message "INFO" "Retrieving service logs for troubleshooting"
        print_message "\n📋 Service logs (last 20 entries):" "$YELLOW"
        journalctl -u birdnet-go.service -n 20 --no-pager

        print_message "\n❗ If you need help with this issue:" "$RED"
        print_message "1. Check port availability and permissions" "$YELLOW"
        print_message "2. Verify your audio device is properly connected and accessible" "$YELLOW"
        print_message "3. If the issue persists, please open a ticket at:" "$YELLOW"
        print_message "   https://github.com/tphakala/birdnet-go/issues" "$GREEN"
        print_message "   Include the logs above in your issue report for faster troubleshooting" "$YELLOW"

        exit 1
    fi
    log_message "INFO" "BirdNET-Go service started successfully"
    print_message "✅ BirdNET-Go service started successfully!" "$GREEN"
    # Determine if this is a fresh install or an upgrade
    local install_type="installation"
    if [ "$FRESH_INSTALL" = "true" ]; then
        install_type="installation"
    else
        install_type="upgrade"
    fi
    
    # Send appropriate telemetry event with more context
    local system_info
    system_info=$(collect_system_info)
    local os_name=$(echo "$system_info" | jq -r '.os_name' 2>/dev/null || echo "unknown")
    local pi_model=$(echo "$system_info" | jq -r '.pi_model' 2>/dev/null || echo "none")
    local cpu_arch=$(echo "$system_info" | jq -r '.cpu_arch' 2>/dev/null || echo "unknown")
    
    send_telemetry_event "info" "${install_type^} completed successfully" "info" "step=start_birdnet_go,type=${install_type},os=${os_name},pi_model=${pi_model},arch=${cpu_arch},port=${WEB_PORT}"

    print_message "\n🐳 Waiting for container to start..." "$YELLOW"
    
    # Wait for container to appear and be running (max 30 seconds)
    local max_attempts=30
    local attempt=1
    local container_id=""
    
    while [ "$attempt" -le "$max_attempts" ]; do
        container_id=$(docker ps --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}")
        if [ -n "$container_id" ]; then
            print_message "✅ Container started successfully!" "$GREEN"
            break
        fi
        
        # Check if service is still running
        if ! sudo systemctl is-active --quiet birdnet-go.service; then
            print_message "❌ Service stopped unexpectedly" "$RED"
            print_message "Checking service logs:" "$YELLOW"
            journalctl -u birdnet-go.service -n 50 --no-pager
            
            print_message "\n❗ If you need help with this issue:" "$RED"
            print_message "1. The service started but then crashed" "$YELLOW"
            print_message "2. Please open a ticket at:" "$YELLOW"
            print_message "   https://github.com/tphakala/birdnet-go/issues" "$GREEN"
            print_message "   Include the logs above in your issue report for faster troubleshooting" "$YELLOW"
            
            exit 1
        fi
        
        print_message "⏳ Waiting for container to start (attempt $attempt/$max_attempts)..." "$YELLOW"
        sleep 1
        ((attempt++))
    done

    if [ -z "$container_id" ]; then
        print_message "❌ Container failed to start within ${max_attempts} seconds" "$RED"
        print_message "Service logs:" "$YELLOW"
        journalctl -u birdnet-go.service -n 50 --no-pager
        
        print_message "\nDocker logs:" "$YELLOW"
        docker ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | xargs -r docker logs
        
        print_message "\n❗ If you need help with this issue:" "$RED"
        print_message "1. The service started but container didn't initialize properly" "$YELLOW"
        print_message "2. Please open a ticket at:" "$YELLOW"
        print_message "   https://github.com/tphakala/birdnet-go/issues" "$GREEN"
        print_message "   Include the logs above in your issue report for faster troubleshooting" "$YELLOW"
        
        exit 1
    fi

    # Wait additional time for application to initialize
    print_message "⏳ Waiting for application to initialize..." "$YELLOW"
    sleep 5

    # Show logs from systemd service instead of container
    print_message "\n📝 Service logs:" "$GREEN"
    journalctl -u birdnet-go.service -n 20 --no-pager
    
    print_message "\nTo follow logs in real-time, use:" "$YELLOW"
    print_message "journalctl -fu birdnet-go.service" "$NC"
}

# Verify after (re)start that BirdNET-Go itself is answering on the web port (not some other
# server that happens to occupy it) and, when AutoTLS was selected, that ports 80 and 443
# actually bound. Best-effort: warns with actionable guidance, never fails the install.
# The /health endpoint returns a JSON body with a "status" field that a generic web server
# on the same port would not, so it doubles as an identity check (GH #1722, #3527).
verify_post_start() {
    local base="http://localhost:${WEB_PORT:-8080}"
    local attempts=10
    local ok="false"
    local i=1
    while [ "$i" -le "$attempts" ]; do
        local body
        body=$(curl -s --connect-timeout 3 --max-time 5 "${base}/health" 2>/dev/null)
        if [ -n "$body" ] && echo "$body" | jq -e 'has("status")' >/dev/null 2>&1; then
            ok="true"
            break
        fi
        sleep 2
        ((i++))
    done

    if [ "$ok" = "true" ]; then
        print_message "✅ Verified BirdNET-Go is responding on port ${WEB_PORT:-8080}" "$GREEN"
        log_message "INFO" "Post-start verification passed on port ${WEB_PORT:-8080}"
    else
        # Distinguish "nothing is there" from "something else answered on this port".
        if curl -s --connect-timeout 3 --max-time 5 "$base" >/dev/null 2>&1; then
            print_message "⚠️ Port ${WEB_PORT:-8080} answered, but the BirdNET-Go health check did not pass." "$YELLOW"
            print_message "   Another service may be using this port, or the app is still starting." "$YELLOW"
        else
            print_message "⚠️ BirdNET-Go did not respond on port ${WEB_PORT:-8080} yet (it may still be initializing)." "$YELLOW"
        fi
        print_message "   Check logs: sudo journalctl -u birdnet-go.service -n 50" "$YELLOW"
        log_message "WARN" "Post-start verification could not confirm BirdNET-Go on port ${WEB_PORT:-8080}"
    fi

    # AutoTLS needs ports 80 and 443 actually listening; a bound listener reads as "not
    # available" from check_port_availability.
    if [ "$BIND_TLS_PORTS" = "true" ]; then
        local tls_ok="true"
        local p
        for p in 80 443; do
            if check_port_availability "$p"; then
                tls_ok="false"
                print_message "⚠️ AutoTLS port $p is not listening; Let's Encrypt certificates may not be issued." "$YELLOW"
            fi
        done
        if [ "$tls_ok" = "true" ]; then
            print_message "✅ AutoTLS ports 80 and 443 are bound" "$GREEN"
        else
            print_message "   AutoTLS needs your domain to resolve to this host with ports 80/443 reachable from the internet." "$YELLOW"
        fi
    fi
}

# Function to check if system is a Raspberry Pi
is_raspberry_pi() {
    if [ -f /proc/device-tree/model ]; then
        local model
        model=$(tr -d '\0' < /proc/device-tree/model)
        if [[ "$model" == *"Raspberry Pi"* ]]; then
            return 0  # True - is a Raspberry Pi
        fi
    fi
    return 1  # False - not a Raspberry Pi
}

# Function to disable WiFi power saving for a specific interface
disable_wifi_power_save_interface() {
    local interface="$1"
    
    # Check if iwconfig is available
    if command -v iwconfig >/dev/null 2>&1; then
        # Try to disable power management using iwconfig
        iwconfig "$interface" power off 2>/dev/null
        if [ $? -eq 0 ]; then
            echo "Disabled WiFi power saving on $interface (iwconfig)"
            return 0
        fi
    fi
    
    # Check if iw is available (modern tool)
    if command -v iw >/dev/null 2>&1; then
        # Try to disable power management using iw
        iw dev "$interface" set power_save off 2>/dev/null
        if [ $? -eq 0 ]; then
            echo "Disabled WiFi power saving on $interface (iw)"
            return 0
        fi
    fi
    
    # Also try to set it via sysfs if available
    local power_save_path="/sys/class/net/$interface/device/power/control"
    if [ -f "$power_save_path" ]; then
        echo "on" > "$power_save_path" 2>/dev/null
        if [ $? -eq 0 ]; then
            echo "Disabled WiFi power saving on $interface (sysfs)"
            return 0
        fi
    fi
    
    return 1
}

# Function to disable WiFi power saving on all WLAN interfaces
disable_wifi_power_save() {
    local success=false
    
    # Find all wireless interfaces
    for interface in /sys/class/net/wlan*; do
        if [ -d "$interface" ]; then
            interface_name=$(basename "$interface")
            if disable_wifi_power_save_interface "$interface_name"; then
                success=true
            fi
        fi
    done
    
    # Also check for interfaces with different naming (e.g., wlp*)
    for interface in /sys/class/net/wlp*; do
        if [ -d "$interface" ]; then
            interface_name=$(basename "$interface")
            # Check if it's actually a wireless interface
            if [ -d "$interface/wireless" ] || [ -d "$interface/phy80211" ]; then
                if disable_wifi_power_save_interface "$interface_name"; then
                    success=true
                fi
            fi
        fi
    done
    
    if [ "$success" = true ]; then
        return 0
    else
        return 1
    fi
}

# Function to configure performance settings
optimize_settings() {
    print_message "\n⏱️ Optimizing settings based on system performance" "$GREEN"
    # enable XNNPACK delegate for inference acceleration
    sed -i 's/usexnnpack: false/usexnnpack: true/' "$CONFIG_FILE"
    print_message "✅ Enabled XNNPACK delegate for inference acceleration" "$GREEN"

    # Check if system is Raspberry Pi and inform about WiFi power saving
    if is_raspberry_pi; then
        print_message "🔧 WiFi power saving will be disabled on startup to prevent connection drops" "$YELLOW"
    fi
}

# Function to validate installation
validate_installation() {
    print_message "\n🔍 Validating installation..." "$YELLOW"
    local checks=0
    
    # Check Docker container
    if check_container_running; then
        ((checks++))
    fi
    
    # Check service status
    if systemctl is-active --quiet birdnet-go.service; then
        ((checks++))
    fi
    
    # Check web interface
    if curl -s "http://localhost:${WEB_PORT}" >/dev/null; then
        ((checks++))
    fi
    
    if [ "$checks" -eq 3 ]; then
        print_message "✅ Installation validated successfully" "$GREEN"
        return 0
    fi
    print_message "⚠️ Installation validation failed" "$RED"
    return 1
}

# Function to get current container version
get_container_version() {
    local image_name="$1"
    local current_version=""
    
    if ! command_exists docker; then
        echo ""
        return
    fi
    
    # Try to get the version from the running container first
    current_version=$(safe_docker ps --format "{{.Image}}" | grep "birdnet-go" | cut -d: -f2)
    
    # If no running container, check if image exists locally
    if [ -z "$current_version" ]; then
        current_version=$(safe_docker images --format "{{.Tag}}" "$image_name" | head -n1)
    fi
    
    echo "$current_version"
}

# Function to show usage information
show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo "Install or update BirdNET-Go with configurable Docker image version"
    echo ""
    echo "OPTIONS:"
    echo "  -v, --version VERSION    Specify container image version (tag or hash)"
    echo "                          Default: nightly"
    echo "                          Examples: latest, v1.2.3, nightly, sha256:abc123..."
    echo "  --silent                Non-interactive install using environment variables"
    echo "  --force-root            Allow running as root (not recommended)"
    echo "  -h, --help              Show this help message"
    echo ""
    echo "SILENT MODE ENVIRONMENT VARIABLES:"
    echo "  BIRDNET_RTSP_URL        RTSP stream URL (if set, uses RTSP audio input)"
    echo "  BIRDNET_LATITUDE        Latitude for location (-90 to 90)"
    echo "  BIRDNET_LONGITUDE       Longitude for location (-180 to 180)"
    echo "  BIRDNET_LOCALE          BirdNET locale (default: en)"
    echo "  BIRDNET_PASSWORD        Web interface password (default: no auth)"
    echo "  BIRDNET_TELEMETRY       Enable telemetry: true/false (default: false)"
    echo "  BIRDNET_WEB_PORT        Web interface host port (default: 8080)"
    echo "  BIRDNET_ENABLE_AUTOTLS  Enable Let's Encrypt AutoTLS, binds 80/443: true/false (default: false; requires BIRDNET_HOST)"
    echo "  BIRDNET_HOST            Public hostname for AutoTLS / reverse proxy (sets security.host)"
    echo "  BIRDNET_URL             Full external URL behind a reverse proxy (sets security.baseurl)"
    echo "  BIRDNET_ENABLE_METRICS  Publish Prometheus metrics on port 8090: true/false (default: false)"
    echo ""
    echo "EXAMPLES:"
    echo "  $0                      # Install using nightly version (default)"
    echo "  $0 -v latest           # Install using latest stable version"
    echo "  $0 -v v1.2.3           # Install specific version tag"
    echo "  $0 --version nightly   # Explicitly use nightly version"
    echo "  BIRDNET_RTSP_URL=rtsp://cam:8554/live BIRDNET_LATITUDE=60.17 \\"
    echo "    BIRDNET_LONGITUDE=24.94 $0 --silent  # Silent RTSP install"
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
            --silent)
                SILENT_MODE="true"
                shift
                ;;
            --force-root)
                FORCE_ROOT="true"
                shift
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
    
    # Set the Docker image URL after parsing arguments
    BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:${BIRDNET_GO_VERSION}"
    
    # Log the version being used
    echo "🐳 Using BirdNET-Go version: $BIRDNET_GO_VERSION"
}

# Parse command line arguments first
parse_arguments "$@"

# Warn if running as root; allow bypass with --force-root (must be before $HOME-dependent path setup)
check_not_root

# When running as root without sudo installed (common in containers), provide a
# shim so the script's 100+ sudo calls work without modification.
if [ "$(id -u)" -eq 0 ] && ! command_exists sudo; then
    sudo() {
        # Strip sudo-specific flags before executing the actual command
        while [ $# -gt 0 ]; do
            case "$1" in
                -n|-S|-E|-H|-P|-K|-k|-b) shift ;;
                -u|-g|-C) shift 2 ;;
                --) shift; break ;;
                *) break ;;
            esac
        done
        "$@"
    }
fi

# Default paths
CONFIG_DIR="$HOME/birdnet-go-app/config"
DATA_DIR="$HOME/birdnet-go-app/data"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
WEB_PORT=8080  # Default web port
COCKPIT_PORT=9090  # Default Cockpit port
# MODIFIED: Set default AUDIO_ENV to always include device mapping
AUDIO_ENV="--device /dev/snd"
# Flag for fresh installation
FRESH_INSTALL="false"
# Flag for completed migration (prevents silent mode from overwriting migrated config)
MIGRATION_DONE="false"
# Configured timezone (will be set during configuration)
CONFIGURED_TZ=""
# Host-side port binding flags for the docker run command in the systemd unit.
# The web interface (-p WEB_PORT:8080) is always bound. Ports 80/443 (Let's Encrypt
# AutoTLS) and 8090 (Prometheus metrics) are opt-in for fresh installs to avoid
# conflicts with existing web servers, and are preserved across updates by parsing the
# existing unit (see load_existing_service_config). Defaults can be overridden in silent
# mode via BIRDNET_ENABLE_AUTOTLS / BIRDNET_ENABLE_METRICS.
BIND_TLS_PORTS="false"
BIND_METRICS_PORT="false"
# Optional host bind address for the web port publish (e.g. 127.0.0.1 when the service is
# meant to sit behind a same-host reverse proxy). Empty means publish on all interfaces.
# The installer never sets this itself, but it is preserved from a hand-edited unit across
# updates so a localhost-only binding is not silently re-exposed to all interfaces.
WEB_PORT_BIND_ADDR=""
# Same idea for the AutoTLS (80/443) and metrics (8090) port bindings: preserve any host
# bind address found on the existing unit so a localhost-only mapping is not re-exposed.
TLS_BIND_ADDR=""
METRICS_BIND_ADDR=""
# Public hostname for AutoTLS / reverse-proxy setups (written to security.host).
CONFIGURED_HOST=""


# Load telemetry configuration before cross-user check so silent-mode
# telemetry events can fire with the user's existing preferences
load_telemetry_config

# Check for existing installations under a different user
check_existing_installation_owner

# Installation status check
INSTALLATION_TYPE=$(check_birdnet_installation)
PRESERVED_DATA=false

# Add debug output to understand detection results
if [ "$INSTALLATION_TYPE" = "full" ]; then
    print_message "DEBUG: Detected full installation (service + Docker)" "$YELLOW" > /dev/null
elif [ "$INSTALLATION_TYPE" = "docker" ]; then
    print_message "DEBUG: Detected Docker-only installation" "$YELLOW" > /dev/null
else
    print_message "DEBUG: No installation detected" "$YELLOW" > /dev/null
fi

if check_preserved_data; then
    PRESERVED_DATA=true
fi

# Log comprehensive session information now that we know the installation state
log_enhanced_session_info "$INSTALLATION_TYPE" "$PRESERVED_DATA" "$FRESH_INSTALL"

# Apply pending reconfiguration: regenerate the systemd unit from the current globals
# (web port, AutoTLS/metrics bindings, timezone), restart the service, and verify. Used by
# reconfigure_menu after the user chooses "Apply".
# Restore the config and systemd unit backed up at the start of reconfiguration, then
# restart the service so BirdNET-Go returns to its previous working state. The port, TLS,
# and metrics settings live in the unit, so restoring only config.yaml is not enough.
# Args: config_backup, service_backup, has_service_backup.
_reconfigure_rollback() {
    local cfg_backup="$1"
    local svc_backup="$2"
    local has_svc="$3"
    cp "$cfg_backup" "$CONFIG_FILE" 2>/dev/null
    if [ "$has_svc" = "true" ] && [ -f "$svc_backup" ]; then
        sudo cp "$svc_backup" "/etc/systemd/system/birdnet-go.service" 2>/dev/null
        sudo systemctl daemon-reload
    fi
    sudo systemctl start birdnet-go.service 2>/dev/null
}

# Apply pending reconfiguration: regenerate the unit from the current globals, restart, and
# verify. On restart failure, automatically roll back to the backed-up config AND unit and
# restart the previous working state. Args: config_backup, service_backup, has_service_backup.
apply_reconfiguration() {
    local cfg_backup="$1"
    local svc_backup="$2"
    local has_svc="$3"
    print_message "\n💾 Applying configuration and restarting BirdNET-Go..." "$YELLOW"
    ensure_internal_port_8080
    add_systemd_config
    sudo systemctl daemon-reload
    if sudo systemctl restart birdnet-go.service; then
        print_message "✅ BirdNET-Go restarted with the new configuration" "$GREEN"
        verify_post_start
        return 0
    fi
    print_message "❌ Failed to start with the new configuration; rolling back to the previous working setup..." "$RED"
    _reconfigure_rollback "$cfg_backup" "$svc_backup" "$has_svc"
    if sudo systemctl is-active --quiet birdnet-go.service; then
        print_message "↩️ Restored the previous configuration; BirdNET-Go is running again." "$GREEN"
    else
        print_message "⚠️ Rollback restart did not come up; check: sudo journalctl -u birdnet-go.service -n 50" "$RED"
        show_service_diagnostics
    fi
    return 1
}

# Interactive reconfiguration of an existing systemd install: lets the user change the web
# port, TLS/access mode, metrics exposure, audio device, timezone, and locale, then
# regenerates the unit and restarts. Edits the existing config in place (a backup is taken
# so "discard" fully reverts). The service is stopped during reconfiguration so the chosen
# web port is not reported as "in use" by BirdNET-Go itself. Returns 1 to return to the
# caller's menu loop.
reconfigure_menu() {
    if [ ! -f "$CONFIG_FILE" ]; then
        print_message "❌ No configuration file found at $CONFIG_FILE" "$RED"
        return 1
    fi

    # Reconfiguration is interactive and stops the running service; refuse to run it without
    # a terminal so a piped or non-interactive invocation (e.g. `echo 6 | ./install.sh`)
    # cannot stop the service and then spin forever on EOF.
    if [ ! -t 0 ]; then
        print_message "⚠️ Reconfiguration requires an interactive terminal; skipping." "$YELLOW"
        return 1
    fi

    # Restore the current port/TLS/metrics/timezone so untouched settings are preserved when
    # the unit is regenerated.
    load_existing_service_config

    print_message "\n🔧 Reconfigure BirdNET-Go" "$GREEN"
    print_message "The service will be stopped while you reconfigure, then restarted when you apply." "$YELLOW"

    local rc_backup="${CONFIG_FILE}.reconfigure.bak"
    if ! cp "$CONFIG_FILE" "$rc_backup"; then
        print_message "❌ Could not create a configuration backup; aborting reconfiguration to avoid risking your config." "$RED"
        return 1
    fi
    # Back up the systemd unit too: the port, TLS, and metrics settings live there, so a
    # rollback that restores only config.yaml would leave a broken unit. Best-effort (the
    # unit is usually world-readable); if it cannot be copied, rollback restores config only.
    local service_backup="${CONFIG_FILE}.service.reconfigure.bak"
    local has_service_backup="false"
    if [ -f "/etc/systemd/system/birdnet-go.service" ] && cp "/etc/systemd/system/birdnet-go.service" "$service_backup" 2>/dev/null; then
        has_service_backup="true"
    fi
    stop_birdnet_service

    # If the user aborts (Ctrl-C / TERM) while the service is stopped, restore the previous
    # config and unit and bring the service back up so reconfiguration never leaves
    # BirdNET-Go down. The global EXIT trap (cleanup_temp_files) still runs after this exits.
    trap '_reconfigure_rollback "$rc_backup" "$service_backup" "$has_service_backup"; rm -f "$rc_backup" "$service_backup"; exit 130' INT TERM

    local changed="false"
    while true; do
        local tls_state="off"; [ "$BIND_TLS_PORTS" = "true" ] && tls_state="on"
        local metrics_state="off"; [ "$BIND_METRICS_PORT" = "true" ] && metrics_state="on"
        print_message "\nCurrent: web port ${WEB_PORT}, AutoTLS ${tls_state}, metrics ${metrics_state}, timezone ${CONFIGURED_TZ:-auto}" "$NC"
        print_message "  1) Web interface port" "$YELLOW"
        print_message "  2) TLS / external access mode" "$YELLOW"
        print_message "  3) Prometheus metrics endpoint" "$YELLOW"
        print_message "  4) Audio capture device (sound card)" "$YELLOW"
        print_message "  5) Timezone" "$YELLOW"
        print_message "  6) Locale (species name language)" "$YELLOW"
        print_message "  7) Apply changes and restart" "$YELLOW"
        print_message "  8) Discard changes and go back" "$YELLOW"
        print_message "❓ Select an option (1-8): " "$YELLOW" "nonewline"
        local rc_choice
        if ! read -r rc_choice; then
            trap - INT TERM
            print_message "\n↩️ No input; restoring previous configuration and restarting..." "$YELLOW"
            _reconfigure_rollback "$rc_backup" "$service_backup" "$has_service_backup"
            rm -f "$rc_backup" "$service_backup"
            return 1
        fi
        case "$rc_choice" in
            1) configure_web_port; changed="true" ;;
            2) configure_tls_access; changed="true" ;;
            3) configure_metrics_exposure; changed="true" ;;
            4) if configure_sound_card; then changed="true"; fi ;;
            5) configure_timezone; changed="true" ;;
            6) configure_locale; changed="true" ;;
            7)
                trap - INT TERM
                if [ "$changed" != "true" ]; then
                    print_message "ℹ️ No changes made; restarting with the existing configuration." "$YELLOW"
                    rm -f "$rc_backup" "$service_backup"
                    sudo systemctl start birdnet-go.service
                    verify_post_start
                    return 1
                fi
                # apply_reconfiguration rolls back to the backed-up config + unit on failure.
                apply_reconfiguration "$rc_backup" "$service_backup" "$has_service_backup"
                rm -f "$rc_backup" "$service_backup"
                return 1
                ;;
            8)
                trap - INT TERM
                print_message "↩️ Discarding changes and restarting with the previous configuration..." "$YELLOW"
                _reconfigure_rollback "$rc_backup" "$service_backup" "$has_service_backup"
                rm -f "$rc_backup" "$service_backup"
                verify_post_start
                return 1
                ;;
            *)
                print_message "❌ Invalid selection. Please choose a number between 1 and 8." "$RED"
                ;;
        esac
    done
}

# Function to display menu options based on installation type
display_menu() {
    local installation_type="$1"

    if [ "$installation_type" = "full" ]; then
        print_message "🔍 Found existing BirdNET-Go installation (systemd service)" "$YELLOW"
        if [ "$BIRDNET_GO_VERSION" != "nightly" ]; then
            print_message "1) Install/update to version: $BIRDNET_GO_VERSION" "$YELLOW"
        else
            print_message "1) Check for updates" "$YELLOW"
        fi
        if has_previous_versions; then
            print_message "2) Revert to previous version" "$YELLOW"
        else
            print_message "2) Revert to previous version (no versions available)" "$GRAY"
        fi
        print_message "3) Fresh installation" "$YELLOW"
        print_message "4) Uninstall BirdNET-Go, remove data" "$YELLOW"
        print_message "5) Uninstall BirdNET-Go, preserve data" "$YELLOW"
        print_message "6) Reconfigure settings (port, TLS, metrics, audio, timezone, locale)" "$YELLOW"
        print_message "7) Exit" "$YELLOW"
        print_message "❓ Select an option (1-7): " "$YELLOW" "nonewline"
        return 7  # Return number of options
    elif [ "$installation_type" = "docker" ]; then
        print_message "🔍 Found existing BirdNET-Go Docker container/image" "$YELLOW"
        if [ "$BIRDNET_GO_VERSION" != "nightly" ]; then
            print_message "1) Install/update to version: $BIRDNET_GO_VERSION" "$YELLOW"
        else
            print_message "1) Check for updates" "$YELLOW"
        fi
        if has_previous_versions; then
            print_message "2) Revert to previous version" "$YELLOW"
        else
            print_message "2) Revert to previous version (no versions available)" "$GRAY"
        fi
        print_message "3) Install as systemd service" "$YELLOW"
        print_message "4) Fresh installation" "$YELLOW"
        print_message "5) Remove Docker container/image" "$YELLOW"
        print_message "6) Exit" "$YELLOW"
        print_message "❓ Select an option (1-6): " "$YELLOW" "nonewline"
        return 6  # Return number of options
    else
        print_message "🔍 Found BirdNET-Go data from previous installation" "$YELLOW"
        if [ "$BIRDNET_GO_VERSION" != "nightly" ]; then
            print_message "1) Install version $BIRDNET_GO_VERSION using existing data and configuration" "$YELLOW"
        else
            print_message "1) Install using existing data and configuration" "$YELLOW"
        fi
        if has_previous_versions; then
            print_message "2) Revert to previous version" "$YELLOW"
        else
            print_message "2) Revert to previous version (no versions available)" "$GRAY"
        fi
        print_message "3) Fresh installation (remove existing data and configuration)" "$YELLOW"
        print_message "4) Remove existing data without installing" "$YELLOW"
        print_message "5) Exit" "$YELLOW"
        print_message "❓ Select an option (1-5): " "$YELLOW" "nonewline"
        return 5  # Return number of options
    fi
}

# Modularized menu action handlers
handle_full_install_menu() {
    local selection="$1"
    case $selection in
        1)
            check_network
            if handle_container_update; then
                exit 0
            else
                print_message "⚠️ Update failed" "$RED"
                print_message "❓ Do you want to proceed with fresh installation? (y/n): " "$YELLOW" "nonewline"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    print_message "❌ Installation cancelled" "$RED"
                    exit 1
                fi
                FRESH_INSTALL="true"
            fi
            ;;
        2)
            # Revert to previous version / Version management
            if ! has_previous_versions; then
                print_message "\n❌ No previous versions available for rollback" "$RED"
                print_message "💡 Previous versions will be available after your first update" "$YELLOW"
                print_message "Press any key to return to menu..."
                read -n 1
                return 1
            fi
            
            while true; do
                print_message "\n🔄 Version Management" "$GREEN"
                print_message "1) Revert to previous version" "$YELLOW"
                print_message "2) Show complete version history" "$YELLOW"
                print_message "3) Back to main menu" "$YELLOW"
                print_message "❓ Select an option (1-3): " "$YELLOW" "nonewline"
                read -r version_menu_choice
                
                case "$version_menu_choice" in
                    1)
                        print_message "\n🔄 Available versions for rollback:" "$YELLOW"
                        if list_available_versions; then
                            print_message "\n❓ Enter version number to revert to (or 'c' to cancel): " "$YELLOW" "nonewline"
                            read -r version_choice
                            
                            if [ "$version_choice" = "c" ]; then
                                print_message "❌ Revert cancelled" "$RED"
                                continue
                            fi
                            
                            if revert_to_version "$version_choice" "ask"; then
                                print_message "✅ Successfully reverted to previous version" "$GREEN"
                                exit 0
                            else
                                print_message "❌ Revert failed" "$RED"
                                print_message "Press any key to return to menu..."
                                read -n 1
                            fi
                        else
                            print_message "Press any key to return to menu..."
                            read -n 1
                        fi
                        ;;
                    2)
                        show_version_history
                        print_message "\nPress any key to return to menu..."
                        read -n 1
                        ;;
                    3|*)
                        return 1
                        ;;
                esac
            done
            ;;
        3)
            print_message "\n⚠️  WARNING: Fresh installation will:" "$RED"
            print_message "  • Remove all BirdNET-Go containers and images" "$RED"
            print_message "  • Delete all configuration and data in $CONFIG_DIR" "$RED"
            print_message "  • Delete all recordings and database in $DATA_DIR" "$RED"
            print_message "  • Remove systemd service configuration" "$RED"
            print_message "\n❓ Type 'yes' to proceed with fresh installation: " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                clean_installation
                FRESH_INSTALL="true"
            else
                print_message "❌ Installation cancelled" "$RED"
                exit 1
            fi
            ;;
        4)
            print_message "\n⚠️  WARNING: Uninstalling BirdNET-Go will:" "$RED"
            print_message "  • Remove all BirdNET-Go containers and images" "$RED"
            print_message "  • Delete all configuration and data in $CONFIG_DIR" "$RED"
            print_message "  • Delete all recordings and database in $DATA_DIR" "$RED"
            print_message "  • Remove systemd service configuration" "$RED"
            print_message "\n❓ Type 'yes' to proceed with uninstallation: " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                if clean_installation; then
                    print_message "✅ BirdNET-Go has been successfully uninstalled" "$GREEN"
                else
                    print_message "⚠️ Some components could not be removed" "$RED"
                    print_message "Please check the messages above for details" "$YELLOW"
                fi
                exit 0
            else
                print_message "❌ Uninstallation cancelled" "$RED"
                exit 1
            fi
            ;;
        5)
            print_message "\nℹ️ NOTE: This option will uninstall BirdNET-Go but preserve your data:" "$YELLOW"
            print_message "  • BirdNET-Go containers and images will be removed" "$YELLOW"
            print_message "  • Systemd service will be disabled and removed" "$YELLOW"
            print_message "  • All your data and configuration in $CONFIG_DIR and $DATA_DIR will be preserved" "$GREEN"
            print_message "\n❓ Type 'yes' to proceed with uninstallation (preserve data): " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                if clean_installation_preserve_data; then
                    print_message "✅ BirdNET-Go has been successfully uninstalled (user data preserved)" "$GREEN"
                else
                    print_message "⚠️ Some components could not be removed" "$RED"
                    print_message "Please check the messages above for details" "$YELLOW"
                fi
                exit 0
            else
                print_message "❌ Uninstallation cancelled" "$RED"
                exit 1
            fi
            ;;
        6)
            reconfigure_menu
            return $?
            ;;
        7)
            print_message "👋 Goodbye!" "$GREEN"
            exit 0
            ;;
        *)
            print_message "❌ Invalid option" "$RED"
            return 1
            ;;
    esac
}

handle_docker_install_menu() {
    local selection="$1"
    case $selection in
        1)
            log_message "INFO" "=== Starting Docker Image Update Process ==="
            
            # Log pre-update state
            log_message "INFO" "=== Pre-Update System State ==="
            log_system_resources "docker-update-pre"
            log_docker_state "docker-update-pre"
            log_network_state "docker-update-pre"
            
            # Capture current image hash before update
            local pre_update_image_hash
            pre_update_image_hash=$(capture_current_image_hash "docker-pre-update")
            
            check_network
            
            log_message "INFO" "Starting Docker image pull: $BIRDNET_GO_IMAGE"
            print_message "\n🔄 Installing BirdNET-Go Docker image: $BIRDNET_GO_VERSION..." "$YELLOW"

            # Capture pull output and status
            local pull_output
            local pull_status
            pull_output=$(docker pull "${BIRDNET_GO_IMAGE}" 2>&1)
            pull_status=$?

            if [ $pull_status -eq 0 ]; then
                log_message "INFO" "Docker image pull completed successfully"

                # Capture new image hash after update
                local post_update_image_hash
                post_update_image_hash=$(capture_current_image_hash "docker-post-update")

                # Log post-update state
                log_message "INFO" "=== Post-Update System State ==="
                log_docker_state "docker-update-post"
                log_system_resources "docker-update-post"

                # Check if the image actually changed
                if [ "$pre_update_image_hash" = "$post_update_image_hash" ]; then
                    log_message "INFO" "Image hash unchanged - already on latest version"
                    print_message "✅ Already on latest version" "$GREEN"
                else
                    log_message "INFO" "Image updated from ${pre_update_image_hash:0:12} to ${post_update_image_hash:0:12}"
                    print_message "✅ Successfully updated to latest image" "$GREEN"
                fi

                print_message "⚠️ Note: You will need to restart your container to use the updated image" "$YELLOW"
                log_message "INFO" "Docker image update process completed successfully"

                # Send telemetry
                send_telemetry_event "info" "Docker image update completed" "info" "step=docker_update,updated=$([[ "$pre_update_image_hash" != "$post_update_image_hash" ]] && echo "true" || echo "false")"

                exit 0
            else
                log_message "ERROR" "Failed to pull Docker image: $BIRDNET_GO_IMAGE"
                log_command_result "docker pull ${BIRDNET_GO_IMAGE}" 1 "docker image pull"
                print_message "❌ Failed to update Docker image" "$RED"

                # Collect diagnostics using helper function, then add update-specific data
                local diagnostic_json
                local current_images
                diagnostic_json=$(collect_docker_pull_diagnostics "$pull_output" "update")
                current_images=$(docker images --format "{{.Repository}}:{{.Tag}} {{.ID}}" 2>/dev/null | grep birdnet-go | sed 's/"/\\"/g' | tr '\n' ';' | head -c "$MAX_FLAGS_LENGTH" || echo "unavailable")

                # Merge with update-specific fields
                diagnostic_json=$(echo "$diagnostic_json" | jq --arg images "$current_images" --arg hash "${pre_update_image_hash:0:20}" \
                    '. + {current_images: $images, pre_update_hash: $hash}' 2>/dev/null || echo "$diagnostic_json")

                # Send telemetry for failure
                send_telemetry_event "error" "Docker image update failed during pull" "error" "step=docker_update" "$diagnostic_json"

                exit 1
            fi
            ;;
        2)
            # Revert to previous version
            if ! has_previous_versions; then
                print_message "\n❌ No previous versions available for rollback" "$RED"
                print_message "💡 Previous versions will be available after your first update" "$YELLOW"
                print_message "Press any key to return to menu..."
                read -n 1
                return 1
            fi
            
            print_message "\n🔄 Reverting to previous version..." "$YELLOW"
            list_available_versions
            
            print_message "\n❓ Enter version number to revert to (or 'c' to cancel): " "$YELLOW" "nonewline"
            read -r version_choice
            
            if [ "$version_choice" = "c" ]; then
                print_message "❌ Revert cancelled" "$RED"
                return 1
            fi
            
            if revert_to_version "$version_choice" "ask"; then
                print_message "✅ Successfully reverted to previous version" "$GREEN"
                print_message "⚠️ Note: You will need to restart your container to use the reverted image" "$YELLOW"
                exit 0
            else
                print_message "❌ Revert failed" "$RED"
                print_message "Press any key to return to menu..."
                read -n 1
                return 1
            fi
            ;;
        3)
            print_message "\n🔧 Installing BirdNET-Go as systemd service..." "$GREEN"
            ;;
        4)
            print_message "\n⚠️  WARNING: Fresh installation will:" "$RED"
            print_message "  • Remove all BirdNET-Go containers and images" "$RED"
            print_message "  • Delete all configuration and data in $CONFIG_DIR" "$RED"
            print_message "  • Delete all recordings and database in $DATA_DIR" "$RED"
            print_message "\n❓ Type 'yes' to proceed with fresh installation: " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                if docker ps -a | grep -q "birdnet-go"; then
                    print_message "🛑 Stopping and removing BirdNET-Go containers..." "$YELLOW"
                    docker ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | xargs -r docker stop
                    docker ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | xargs -r docker rm
                    print_message "✅ Removed containers" "$GREEN"
                fi
                image_base="${BIRDNET_GO_IMAGE%:*}"
                images_to_remove=$(docker images "${image_base}" -q)
                if [ -n "${images_to_remove}" ]; then
                    print_message "🗑️ Removing BirdNET-Go images..." "$YELLOW"
                    echo "${images_to_remove}" | xargs -r docker rmi -f
                    print_message "✅ Removed images" "$GREEN"
                fi
                if [ -d "$CONFIG_DIR" ] || [ -d "$DATA_DIR" ]; then
                    print_message "📁 Removing data directories..." "$YELLOW"
                    rm -rf "$CONFIG_DIR" "$DATA_DIR" 2>/dev/null || sudo rm -rf "$CONFIG_DIR" "$DATA_DIR"
                    print_message "✅ Removed data directories" "$GREEN"

                    # Remove parent directory if empty
                    local parent_dir="$HOME/birdnet-go-app"
                    if [ -d "$parent_dir" ] && [ -z "$(ls -A "$parent_dir" 2>/dev/null)" ]; then
                        rm -rf "$parent_dir" 2>/dev/null || sudo rm -rf "$parent_dir"
                    fi
                fi
                FRESH_INSTALL="true"
            else
                print_message "❌ Installation cancelled" "$RED"
                exit 1
            fi
            ;;
        5)
            print_message "\n⚠️  WARNING: This will remove BirdNET-Go Docker components:" "$RED"
            print_message "  • Stop and remove all BirdNET-Go containers" "$RED"
            print_message "  • Remove all BirdNET-Go Docker images" "$RED"
            print_message "  • Configuration and data will remain in $CONFIG_DIR and $DATA_DIR" "$GREEN"
            print_message "\n❓ Type 'yes' to proceed with removal: " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                if docker ps -a | grep -q "birdnet-go"; then
                    print_message "🛑 Stopping and removing BirdNET-Go containers..." "$YELLOW"
                    docker ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | xargs -r docker stop
                    docker ps -a --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}" | xargs -r docker rm
                    print_message "✅ Removed containers" "$GREEN"
                fi
                image_base="${BIRDNET_GO_IMAGE%:*}"
                images_to_remove=$(docker images "${image_base}" -q)
                if [ -n "${images_to_remove}" ]; then
                    print_message "🗑️ Removing BirdNET-Go images..." "$YELLOW"
                    echo "${images_to_remove}" | xargs -r docker rmi -f
                    print_message "✅ Removed images" "$GREEN"
                fi
                print_message "✅ BirdNET-Go Docker components removed successfully" "$GREEN"
                exit 0
            else
                print_message "❌ Operation cancelled" "$RED"
                exit 1
            fi
            ;;
        6)
            print_message "👋 Goodbye!" "$GREEN"
            exit 0
            ;;
        *)
            print_message "❌ Invalid option" "$RED"
            exit 1
            ;;
    esac
}

handle_preserved_data_menu() {
    local selection="$1"
    case $selection in
        1)
            print_message "\n📝 Installing BirdNET-Go using existing data..." "$GREEN"
            ;;
        2)
            # Revert to previous version
            if ! has_previous_versions; then
                print_message "\n❌ No previous versions available for rollback" "$RED"
                print_message "💡 Previous versions will be available after your first update" "$YELLOW"
                print_message "Press any key to return to menu..."
                read -n 1
                return 1
            fi
            
            print_message "\n🔄 Reverting to previous version..." "$YELLOW"
            list_available_versions
            
            print_message "\n❓ Enter version number to revert to (or 'c' to cancel): " "$YELLOW" "nonewline"
            read -r version_choice
            
            if [ "$version_choice" = "c" ]; then
                print_message "❌ Revert cancelled" "$RED"
                return 1
            fi
            
            if revert_to_version "$version_choice" "ask"; then
                print_message "✅ Successfully reverted to previous version" "$GREEN"
                exit 0
            else
                print_message "❌ Revert failed" "$RED"
                print_message "Press any key to return to menu..."
                read -n 1
                return 1
            fi
            ;;
        3)
            print_message "\n⚠️  WARNING: Fresh installation will remove existing data:" "$RED"
            print_message "  • Delete all configuration and data in $CONFIG_DIR" "$RED"
            print_message "  • Delete all recordings and database in $DATA_DIR" "$RED"
            print_message "\n❓ Type 'yes' to proceed with fresh installation: " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                if [ -d "$CONFIG_DIR" ] || [ -d "$DATA_DIR" ]; then
                    print_message "📁 Removing data directories..." "$YELLOW"
                    rm -rf "$CONFIG_DIR" "$DATA_DIR" 2>/dev/null || sudo rm -rf "$CONFIG_DIR" "$DATA_DIR"
                    print_message "✅ Removed existing data directories" "$GREEN"

                    # Remove parent directory if empty
                    local parent_dir="$HOME/birdnet-go-app"
                    if [ -d "$parent_dir" ] && [ -z "$(ls -A "$parent_dir" 2>/dev/null)" ]; then
                        rm -rf "$parent_dir" 2>/dev/null || sudo rm -rf "$parent_dir"
                    fi
                fi
                FRESH_INSTALL="true"
            else
                print_message "❌ Installation cancelled" "$RED"
                exit 1
            fi
            ;;
        4)
            print_message "\n⚠️  WARNING: This will permanently delete:" "$RED"
            print_message "  • All configuration and data in $CONFIG_DIR" "$RED"
            print_message "  • All recordings and database in $DATA_DIR" "$RED"
            print_message "\n❓ Type 'yes' to proceed with data removal: " "$YELLOW" "nonewline"
            read -r response
            if [ "$response" = "yes" ]; then
                if [ -d "$CONFIG_DIR" ] || [ -d "$DATA_DIR" ]; then
                    print_message "📁 Removing data directories..." "$YELLOW"
                    if ! rm -rf "$CONFIG_DIR" "$DATA_DIR" 2>/dev/null; then
                        sudo rm -rf "$CONFIG_DIR" "$DATA_DIR"
                    fi
                    print_message "✅ All data has been successfully removed" "$GREEN"

                    # Remove parent directory if empty
                    local parent_dir="$HOME/birdnet-go-app"
                    if [ -d "$parent_dir" ] && [ -z "$(ls -A "$parent_dir" 2>/dev/null)" ]; then
                        rm -rf "$parent_dir" 2>/dev/null || sudo rm -rf "$parent_dir"
                    fi
                fi
                exit 0
            else
                print_message "❌ Operation cancelled" "$RED"
                exit 1
            fi
            ;;
        5)
            print_message "👋 Goodbye!" "$GREEN"
            exit 0
            ;;
        *)
            print_message "❌ Invalid option" "$RED"
            exit 1
            ;;
    esac
}

# Simplified dispatcher
handle_menu_selection() {
    local installation_type="$1"
    local selection="$2"
    if [ "$installation_type" = "full" ]; then
        handle_full_install_menu "$selection"
    elif [ "$installation_type" = "docker" ]; then
        handle_docker_install_menu "$selection"
    else
        handle_preserved_data_menu "$selection"
    fi
}

# Silent mode skips the menu and forces fresh install
if [ "$SILENT_MODE" = "true" ] && { [ "$INSTALLATION_TYPE" != "none" ] || [ "$PRESERVED_DATA" = true ]; }; then
    print_message "🔇 Silent mode: performing update on existing installation" "$YELLOW"
    if [ "$INSTALLATION_TYPE" = "full" ] || [ "$INSTALLATION_TYPE" = "docker" ]; then
        check_network
        if handle_container_update; then
            exit 0
        fi
        print_message "⚠️ Update failed, proceeding with fresh installation" "$YELLOW"
    fi
    # After migration, preserve the migrated config by skipping fresh-install overwrite
    if [ "$MIGRATION_DONE" = "true" ]; then
        FRESH_INSTALL="false"
    else
        FRESH_INSTALL="true"
    fi
fi

# Menu loop for existing installations (skipped in silent mode and after migration)
if [ "$SILENT_MODE" != "true" ] && [ "$MIGRATION_DONE" != "true" ] && { [ "$INSTALLATION_TYPE" != "none" ] || [ "$PRESERVED_DATA" = true ]; }; then
    while true; do
        # Display menu based on installation type
        print_message ""  # Add spacing
        display_menu "$INSTALLATION_TYPE"
        max_options=$?
        
        # Read user selection. On EOF (piped/non-interactive stdin) exit cleanly instead of
        # looping forever on an empty read.
        if ! read -r response; then
            print_message "\nNo input received; exiting." "$YELLOW"
            exit 0
        fi

        # Validate user selection
        if [[ "$response" =~ ^[0-9]+$ ]] && [ "$response" -ge 1 ] && [ "$response" -le "$max_options" ]; then
            # Handle menu selection
            handle_menu_selection "$INSTALLATION_TYPE" "$response"
            menu_result=$?

            # If menu action succeeded (returned 0), operation is complete
            # Most menu actions exit directly, but if they return 0, we should also exit
            if [ $menu_result -eq 0 ]; then
                # Check if this was a fresh install request (option 3 typically)
                # In that case, break to continue with installation
                if [ "$FRESH_INSTALL" = "true" ]; then
                    break
                fi
                # Otherwise, the menu action completed successfully, exit
                exit 0
            fi
            # If menu action failed/cancelled (returned 1), continue loop to show menu again
        else
            print_message "❌ Invalid option. Please select a number between 1 and $max_options." "$RED"
            # Continue loop to show menu again
        fi
    done
fi

# Show version being installed for fresh installations  
if [ "$BIRDNET_GO_VERSION" != "nightly" ]; then
    print_message "🚀 Installing BirdNET-Go version: $BIRDNET_GO_VERSION" "$GREEN"
fi

print_message "Note: Root privileges will be required for:" "$YELLOW"
print_message "  - Installing system packages (alsa-utils, curl, bc, jq, apache2-utils)" "$YELLOW"
print_message "  - Installing Docker" "$YELLOW"
print_message "  - Creating systemd service" "$YELLOW"
print_message ""

# Initialize logging system 
setup_logging

# Display welcome message
print_message "\n🐦 BirdNET-Go Installation Script" "$GREEN"
print_message "This script will install BirdNET-Go and its dependencies." "$YELLOW"

# First check basic network connectivity and ensure curl is available
check_network

# Check prerequisites before proceeding
check_prerequisites

# Check if systemd is the init system
check_systemd

# Now proceed with rest of package installation
print_message "\n🔧 Updating package list..." "$YELLOW"
sudo apt -qq update

# Install required packages
print_message "\n🔧 Checking and installing required packages..." "$YELLOW"

# Check which packages need to be installed
REQUIRED_PACKAGES=("alsa-utils" "curl" "bc" "jq" "apache2-utils" "netcat-openbsd" "iproute2" "lsof" "avahi-daemon" "libnss-mdns")
TO_INSTALL=()

for pkg in "${REQUIRED_PACKAGES[@]}"; do
    if ! dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null | grep -q "install ok installed"; then
        TO_INSTALL+=("$pkg")
    else
        print_message "✅ $pkg found" "$GREEN"
    fi
done

# Install missing packages
if [ ${#TO_INSTALL[@]} -gt 0 ]; then
    print_message "🔧 Installing missing packages: ${TO_INSTALL[*]}" "$YELLOW"
    sudo apt clean
    sudo apt update -q
    if sudo apt install -q -y "${TO_INSTALL[@]}"; then
        print_message "✅ All packages installed successfully" "$GREEN"
    else
        print_message "⚠️ Package installation failed, retrying with new apt update and install..." "$YELLOW"
        # Retry with apt update first
        if sudo apt update && sudo apt install -q -y "${TO_INSTALL[@]}"; then
            print_message "✅ All packages installed successfully after update" "$GREEN"
        else
            print_message "❌ Failed to install some packages even after apt update" "$RED"
            exit 1
        fi
    fi
fi

# Pull Docker image
pull_docker_image

# Check if directories can be created
check_directory "$CONFIG_DIR"
check_directory "$DATA_DIR"

# Create directories
print_message "\n🔧 Creating config and data directories..." "$YELLOW"
print_message "📁 Config directory: " "$GREEN" "nonewline"
print_message "$CONFIG_DIR" "$NC"
print_message "📁 Data directory: " "$GREEN" "nonewline"
print_message "$DATA_DIR" "$NC"
mkdir -p "$CONFIG_DIR"
mkdir -p "$DATA_DIR"
mkdir -p "$DATA_DIR/clips"
print_message "✅ Created data directory and clips subdirectory" "$GREEN"

# Check data directory has sufficient space
print_message "\n💾 Checking data directory disk space..." "$YELLOW"
check_data_directory_space "$DATA_DIR"

# Download base config file
download_base_config

# Skip configuration steps after migration (config already has user's settings)
if [ "$MIGRATION_DONE" = "true" ]; then
    print_message "\n✅ Using migrated configuration settings" "$GREEN"
else
    # Now lets query user for configuration
    print_message "\n🔧 Now lets configure some basic settings" "$YELLOW"

    # Configure web port
    configure_web_port

    # Configure audio input
    configure_audio_input

    # Configure audio format
    configure_audio_format

    # Configure location (this will also detect timezone)
    configure_location

    # Configure timezone (now with smart detection from location)
    configure_timezone

    # Configure locale
    configure_locale

    # Configure security
    configure_auth

    # Configure TLS / external access mode (decides whether 80/443 are published)
    configure_tls_access

    # Configure optional Prometheus metrics endpoint (port 8090)
    configure_metrics_exposure
fi

# Configure telemetry (only if not already configured or fresh install)
if [ "$FRESH_INSTALL" = "true" ] || [ "$TELEMETRY_CONFIGURED" = "false" ]; then
    configure_telemetry
else
    print_message "\n📊 Using existing telemetry configuration: $([ "$TELEMETRY_ENABLED" = "true" ] && echo "enabled" || echo "disabled")" "$GREEN"
    # Save telemetry config to ensure install ID is preserved
    save_telemetry_config
fi

# Optimize settings
optimize_settings

# Add systemd service configuration
add_systemd_config

# Start BirdNET-Go
start_birdnet_go

# Verify BirdNET-Go is actually answering on the configured port (and AutoTLS ports if set)
verify_post_start

# Validate installation
validate_installation

log_message "INFO" "=== Installation Completed - Final Validation ==="

# Log final system state  
log_system_resources "post-install"
log_docker_state "post-install"  
log_service_state "post-install"

# Log final config file hash
if [ -f "$CONFIG_FILE" ]; then
    log_config_hash "final"
fi

# Verify service is responding
final_service_responsive="false" 
if systemctl is-active --quiet birdnet-go.service; then
    # Check if web interface is responding
    if curl -s -f --connect-timeout 5 "http://localhost:${WEB_PORT:-8080}" >/dev/null 2>&1; then
        final_service_responsive="true"
        log_message "INFO" "Final validation: Web interface responding on port ${WEB_PORT:-8080}"
    else
        log_message "WARN" "Final validation: Web interface not responding on port ${WEB_PORT:-8080}"
    fi
else
    log_message "ERROR" "Final validation: Service not active"
fi

log_message "INFO" "=== Installation Summary ==="
log_message "INFO" "Process type: $(detect_process_type "$INSTALLATION_TYPE" "$PRESERVED_DATA" "$FRESH_INSTALL")"
log_message "INFO" "Configuration directory: $CONFIG_DIR"
log_message "INFO" "Data directory: $DATA_DIR"  
log_message "INFO" "Web interface port: ${WEB_PORT:-8080}"
log_message "INFO" "Service responsive: $final_service_responsive"

# Configure Cockpit installation before completion message
configure_cockpit

# Get IP address for final output
IP_ADDR=$(get_ip_address)

print_message ""
print_message "✅ Installation completed!" "$GREEN"
print_message "📁 Configuration directory: " "$GREEN" "nonewline"
print_message "$CONFIG_DIR"
print_message "📁 Data directory: " "$GREEN" "nonewline"
print_message "$DATA_DIR"

# Display Cockpit URL if installed
if [ "$(check_cockpit_status 2>/dev/null)" = "installed" ] && is_cockpit_installed; then
    if [ -n "$IP_ADDR" ]; then
        log_message "INFO" "Cockpit web interface accessible at: https://${IP_ADDR}:${COCKPIT_PORT}"
        print_message "🖥️ Cockpit system management interface enabled and available at https://${IP_ADDR}:${COCKPIT_PORT}" "$GREEN"
    else
        print_message "🖥️ Cockpit system management interface enabled and available at https://localhost:${COCKPIT_PORT}" "$GREEN"
    fi
    
    if check_mdns; then
        HOSTNAME=$(hostname)
        print_message "🖥️ Cockpit also available at: https://${HOSTNAME}.local:${COCKPIT_PORT}" "$GREEN"
    fi
fi

# Display BirdNET-Go URLs prominently at the end
if [ -n "$IP_ADDR" ]; then
    log_message "INFO" "Web interface accessible at: http://${IP_ADDR}:${WEB_PORT}"
    print_message "🐦 BirdNET-Go web interface is available at http://${IP_ADDR}:${WEB_PORT}" "$GREEN"
else
    log_message "WARN" "Could not determine IP address for web interface access"
    print_message "⚠️ Could not determine IP address - you may access BirdNET-Go at http://localhost:${WEB_PORT}" "$YELLOW"
    print_message "To find your IP address manually, run: ip addr show or nmcli device show" "$YELLOW"
fi

# Check if mDNS is available
if check_mdns; then
    HOSTNAME=$(hostname)
    log_message "INFO" "mDNS available, accessible at: http://${HOSTNAME}.local:${WEB_PORT}"
    print_message "🐦 Also available at http://${HOSTNAME}.local:${WEB_PORT}" "$GREEN"
else
    log_message "INFO" "mDNS not available"
fi

# Show service diagnostics
show_service_diagnostics

# Display helpful commands
print_message "\n📚 Helpful Commands:" "$GREEN"
print_message "  Check status:    sudo systemctl status birdnet-go" "$NC"
print_message "  View logs:       sudo journalctl -u birdnet-go.service -f" "$NC"
print_message "  Check disk:      df -h $DATA_DIR" "$NC"
print_message "  Restart service: sudo systemctl restart birdnet-go" "$NC"
print_message "  Container logs:  docker logs birdnet-go" "$NC"
print_message "  Health status:   docker inspect --format '{{json .State.Health}}' birdnet-go | jq" "$NC"

log_message "INFO" "Install.sh script execution completed successfully"
log_message "INFO" "=== End of BirdNET-Go Installation/Update Session ==="

