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


BIRDNET_GO_VERSION="nightly"
BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:${BIRDNET_GO_VERSION}"

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
    
    # Start the service
    log_message "INFO" "Starting service with reverted image"
    sudo systemctl daemon-reload
    log_command_result "systemctl daemon-reload" $? "reloading systemd after revert"
    
    if sudo systemctl start birdnet-go.service; then
        log_command_result "systemctl start birdnet-go.service" $? "starting reverted service"
        log_message "INFO" "Service started successfully with reverted image"
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

    # Check port availability early in prerequisites
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
    
    for port in "${unique_ports[@]}"; do
        if ! check_port_availability "$port"; then
            failed_ports+=("$port")
            process_info=$(get_port_process_info "$port")
            port_processes+=("$process_info")
            print_message "❌ Port $port is already in use by: $process_info" "$RED"
        else
            print_message "✅ Port $port is available" "$GREEN"
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
        
        print_message "\n2. Or use Docker with different port mappings (advanced users):" "$YELLOW"
        print_message "   Modify the systemd service file after installation to use different ports" "$NC"
        
        print_message "\n3. Or uninstall conflicting software if not needed:" "$YELLOW"
        print_message "   sudo apt remove <package-name>" "$NC"
        
        print_message "\n⚠️  Note: BirdNET-Go requires ports 80 and 443 for:" "$YELLOW"
        print_message "  • HTTP web interface access" "$YELLOW"
        print_message "  • HTTPS web interface (if SSL is configured)" "$YELLOW"
        print_message "  • Proper web interface functionality" "$YELLOW"
        
        send_telemetry_event "error" "Port availability check failed" "error" "step=check_prerequisites,failed_ports=${failed_ports[*]}"
        exit 1
    fi
    
    print_message "✅ All required ports are available" "$GREEN"

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

# Telemetry Configuration
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
    "release": "install-script@1.0.0",
    "tags": {
        "event_type": "$event_type",
        "script_version": "1.0.0"
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
        "context": "$context"
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

    if docker pull "${BIRDNET_GO_IMAGE}"; then
        log_message "INFO" "Docker image pulled successfully: $BIRDNET_GO_IMAGE"
        print_message "✅ Docker image pulled successfully" "$GREEN"
    else
        log_message "ERROR" "Docker image pull failed: $BIRDNET_GO_IMAGE"
        send_telemetry_event "error" "Docker image pull failed" "error" "step=pull_docker_image,image=${BIRDNET_GO_IMAGE}"
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
    if [ -f "$CONFIG_FILE" ] || [ -d "$DATA_DIR" ]; then
        return 0  # Preserved data exists
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
        # Use line-specific sed to replace just the clips path line
        # Corrected sed command for replacement
        sed -i "${clip_path_line}s|^\([[:space:]]*path:[[:space:]]*\).*|\1${abs_path}        # path to audio clip export directory|" "$config_file"
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
        send_telemetry_event "error" "Configuration download failed" "error" "step=download_base_config"
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

    # Test audio device access - using LC_ALL=C to force English output
    if ! LC_ALL=C arecord -c 1 -f S16_LE -r 48000 -d 1 -D "$device" /dev/null 2>/dev/null; then
        send_telemetry_event "error" "Audio device validation failed" "error" "step=validate_audio_device,device=$device"
        print_message "❌ Failed to access audio device" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "  • Device is busy" "$YELLOW"
        print_message "  • Insufficient permissions" "$YELLOW"
        print_message "  • Device is not properly connected" "$YELLOW"
        return 1
    else
        print_message "✅ Audio device validated successfully, tested 48kHz 16-bit mono capture" "$GREEN"
    fi
    
    return 0
}

# Function to configure sound card
configure_sound_card() {
    log_message "INFO" "Starting sound card configuration"
    while true; do
        print_message "\n🎤 Detected audio devices:" "$GREEN"
        
        # Create arrays to store device information
        declare -a devices
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

            # Update config file with the friendly name
            sed -i "s/source: \"sysdefault\"/source: \"${ALSA_CARD}\"/" "$CONFIG_FILE"
            log_command_result "sed audio device configuration" $? "updating config file"
            # Comment out RTSP section
            sed -i '/rtsp:/,/      # - rtsp/s/^/#/' "$CONFIG_FILE"
            log_command_result "sed comment RTSP section" $? "disabling RTSP configuration"
                
            AUDIO_ENV="--device /dev/snd"
            return 0
        else
            log_message "WARN" "Invalid audio device selection: $selection"
            print_message "❌ Invalid selection. Please try again." "$RED"
        fi
    done
}

# Function to configure RTSP stream
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
            
            # Update config file
            sed -i "s|# - rtsp://user:password@example.com/stream1|      - ${RTSP_URL}|" "$CONFIG_FILE"
            log_command_result "sed RTSP URL configuration" $? "adding RTSP URL to config"
            # Comment out audio source section
            sed -i '/source: "sysdefault"/s/^/#/' "$CONFIG_FILE"
            log_command_result "sed comment audio source" $? "disabling audio source"
            
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

    # Update config file
    sed -i "s/type: wav/type: $format/" "$CONFIG_FILE"
}

# Function to configure locale
configure_locale() {
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
            # Update config file - fixed to replace the entire locale value
            sed -i "s/locale: [a-zA-Z0-9_-]*/locale: ${LOCALE_CODE}/" "$CONFIG_FILE"
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

# Function to configure timezone
configure_timezone() {
    print_message "\n🕐 Timezone Configuration" "$GREEN"
    print_message "BirdNET-Go needs to know your timezone for accurate timestamps and scheduling" "$YELLOW"
    
    # Get current system timezone
    local system_tz=""
    local detected_tz=""
    
    # Try multiple methods to detect timezone
    if [ -f /etc/timezone ]; then
        system_tz=$(cat /etc/timezone 2>/dev/null | tr -d '\n' | tr -d ' ')
    fi
    
    # Fallback to timedatectl if available
    if [ -z "$system_tz" ] && command_exists timedatectl; then
        system_tz=$(timedatectl show --property=Timezone --value 2>/dev/null | tr -d '\n' | tr -d ' ')
    fi
    
    # Fallback to readlink on /etc/localtime
    if [ -z "$system_tz" ] && [ -L /etc/localtime ]; then
        local tz_path=$(readlink -f /etc/localtime)
        system_tz=${tz_path#/usr/share/zoneinfo/}
    fi
    
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
        print_message "\n📋 Common timezone examples:" "$YELLOW"
        print_message "  • US/Eastern, US/Central, US/Mountain, US/Pacific" "$YELLOW"
        print_message "  • Europe/London, Europe/Berlin, Europe/Paris" "$YELLOW"
        print_message "  • Asia/Tokyo, Asia/Singapore, Asia/Dubai" "$YELLOW"
        print_message "  • Australia/Sydney, Australia/Melbourne" "$YELLOW"
        print_message "  • UTC (Coordinated Universal Time)" "$YELLOW"
        
        while true; do
            print_message "\n❓ Enter your timezone (e.g., US/Eastern, Europe/London): " "$YELLOW" "nonewline"
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
                print_message "❌ Invalid timezone '$user_tz'" "$RED"
                if [ "$user_tz" != "$normalized_tz" ]; then
                    print_message "   Also tried: '$normalized_tz'" "$RED"
                fi
                print_message "💡 Tip: You can list all available timezones with: timedatectl list-timezones" "$YELLOW"
                print_message "   Or check /usr/share/zoneinfo/ directory" "$YELLOW"
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
            # Update config file and return
            sed -i "s/latitude: 00.000/latitude: $lat/" "$CONFIG_FILE"
            local sed_result=$?
            sed -i "s/longitude: 00.000/longitude: $lon/" "$CONFIG_FILE"
            sed_result=$((sed_result + $?))
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
                    city=$(echo "$location" | cut -d',' -f1 | xargs)
                    country=$(echo "$location" | cut -d',' -f2 | xargs)
                    
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

    # Update config file
    log_message "INFO" "Location configured manually, updating config file"
    sed -i "s/latitude: 00.000/latitude: $lat/" "$CONFIG_FILE"
    local sed_result=$?
    sed -i "s/longitude: 00.000/longitude: $lon/" "$CONFIG_FILE"
    sed_result=$((sed_result + $?))
    log_command_result "sed latitude/longitude update" "$sed_result" "updating location coordinates in config file"
}

# Function to configure basic authentication
configure_auth() {
    log_message "INFO" "Starting authentication configuration"
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
                
                # Update config file - using different delimiter for sed
                sed -i "s|enabled: false    # true to enable basic auth|enabled: true    # true to enable basic auth|" "$CONFIG_FILE"
                log_command_result "sed enable auth" $? "enabling authentication"
                sed -i "s|password: \"\"|password: \"$password_hash\"|" "$CONFIG_FILE"
                log_command_result "sed password hash" $? "setting password hash"
                
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


# Function to configure web interface port
configure_web_port() {
    # Set default port
    WEB_PORT=8080
    
    # Update config file with port
    sed -i -E "s/^(\\s*port:\\s*)[0-9]+/\\1$WEB_PORT/" "$CONFIG_FILE"
    
    # Port validation already done in prerequisites section
}

# Generate systemd service content
generate_systemd_service_content() {
    # Use configured timezone if available, otherwise fall back to system timezone
    local TZ
    if [ -n "$CONFIGURED_TZ" ]; then
        TZ="$CONFIGURED_TZ"
    elif [ -f /etc/timezone ]; then
        TZ=$(cat /etc/timezone)
    else
        TZ="UTC"
    fi

    # Determine host UID/GID even when executed with sudo
    local HOST_UID=${SUDO_UID:-$(id -u)}
    local HOST_GID=${SUDO_GID:-$(id -g)}

    # Check for /dev/snd/
    local audio_env_line=""
    if check_directory_exists "/dev/snd/"; then
        audio_env_line="--device /dev/snd \\"
    fi

    # Check for /sys/class/thermal, used for Raspberry Pi temperature reporting in system dashboard
    local thermal_volume_line=""
    if check_directory_exists "/sys/class/thermal"; then
        thermal_volume_line="-v /sys/class/thermal:/sys/class/thermal \\"
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
${wifi_power_save_script}
ExecStart=/usr/bin/docker run --rm \\
    --name birdnet-go \\
    -p ${WEB_PORT}:8080 \\
    -p 80:80 \\
    -p 443:443 \\
    -p 8090:8090 \\
    --env TZ="${TZ}" \\
    --env BIRDNET_UID=${HOST_UID} \\
    --env BIRDNET_GID=${HOST_GID} \\
    ${audio_env_line}
    -v ${CONFIG_DIR}:/config \\
    -v ${DATA_DIR}:/data \\
    ${thermal_volume_line}
    ${BIRDNET_GO_IMAGE}
# Cleanup tasks on stop
ExecStopPost=/bin/sh -c 'umount -f ${CONFIG_DIR}/hls || true'
ExecStopPost=-/usr/bin/docker rm -f birdnet-go

[Install]
WantedBy=multi-user.target
EOF
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
    # Method 1: Check if cockpit packages are installed via dpkg
    if dpkg-query -W -f='${Status}' cockpit 2>/dev/null | grep -q "install ok installed"; then
        return 0
    fi
    
    # Method 2: Check if cockpit-ws command exists
    if command_exists cockpit-ws; then
        return 0
    fi
    
    # Method 3: Check if cockpit bridge exists 
    if command_exists cockpit-bridge; then
        return 0
    fi
    
    # Method 4: Check if systemd units exist
    if systemctl list-unit-files 2>/dev/null | grep -E "(cockpit\.(socket|service))" >/dev/null 2>&1; then
        return 0
    fi
    
    return 1
}

# Function to check if Cockpit service is enabled and running
is_cockpit_running() {
    # Check cockpit.socket first (preferred method)
    if systemctl is-active --quiet cockpit.socket 2>/dev/null; then
        return 0
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

# Function to configure Cockpit installation
configure_cockpit() {
    log_message "INFO" "Starting Cockpit configuration check"
    
    # Debug: Log detection results for troubleshooting
    log_message "INFO" "Cockpit detection debug: installed=$(is_cockpit_installed && echo 'true' || echo 'false'), running=$(is_cockpit_running && echo 'true' || echo 'false')"
    
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
                if sudo systemctl enable --now cockpit.socket; then
                    print_message "✅ Cockpit system management interface enabled and available at https://${IP_ADDR}:${COCKPIT_PORT}!" "$GREEN"
                    log_message "INFO" "Cockpit service enabled and started"
                    save_cockpit_status "installed"
                    return 0
                else
                    print_message "❌ Failed to enable Cockpit service" "$RED"
                    log_message "ERROR" "Failed to enable existing Cockpit service"
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
        
        if sudo apt update -q && sudo apt install -q -y cockpit; then
            log_message "INFO" "Cockpit installation successful"
            print_message "✅ Cockpit system management interface installed successfully!" "$GREEN"
            
            # Enable and start Cockpit socket
            if sudo systemctl enable --now cockpit.socket; then
                log_message "INFO" "Cockpit service enabled and started"
                print_message "✅ Cockpit system management interface enabled and available at https://${IP_ADDR}:${COCKPIT_PORT}" "$GREEN"
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
            print_message "💡 To install Cockpit manually, run: sudo apt install cockpit" "$YELLOW"
            print_message "   Then enable it with: sudo systemctl enable --now cockpit.socket" "$YELLOW"
            save_cockpit_status "install_failed"
            return 1
        fi
    else
        log_message "INFO" "User declined Cockpit installation"
        print_message "ℹ️ Cockpit installation skipped" "$YELLOW"
        print_message "💡 To install Cockpit later, run: sudo apt install cockpit" "$YELLOW"
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
    
    local service_needs_update
    service_needs_update=$(check_systemd_service)
    
    log_message "INFO" "Systemd service update needed: $service_needs_update"
    print_message "🔄 Checking for updates..." "$YELLOW"
    
    # Extract existing timezone from systemd service file if updating
    if [ -f "/etc/systemd/system/birdnet-go.service" ] && [ -z "$CONFIGURED_TZ" ]; then
        local existing_tz=$(grep -oP '(?<=--env TZ=")[^"]+' /etc/systemd/system/birdnet-go.service 2>/dev/null)
        if [ -n "$existing_tz" ]; then
            CONFIGURED_TZ="$existing_tz"
            log_message "INFO" "Extracted existing timezone from service: $CONFIGURED_TZ"
            print_message "📍 Using existing timezone configuration: $CONFIGURED_TZ" "$GREEN"
        fi
    fi
    
    # Stop the service and container
    log_message "INFO" "Stopping BirdNET-Go service for update"
    stop_birdnet_service
    
    # Clean up existing tmpfs mounts
    log_message "INFO" "Cleaning up tmpfs mounts"
    cleanup_hls_mount
    
    # Update configuration paths
    log_message "INFO" "Updating configuration paths"
    update_paths_in_config
    
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
    log_message "INFO" "Pulling latest Docker image: $BIRDNET_GO_IMAGE"
    print_message "📥 Pulling latest nightly image..." "$YELLOW"
    if ! docker pull "${BIRDNET_GO_IMAGE}"; then
        log_message "ERROR" "Failed to pull new Docker image during update"
        print_message "❌ Failed to pull new image" "$RED"
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
                        find "$dir" -type f 2>/dev/null | while read -r file; do
                            error_list="${error_list}  • $file\n"
                        done
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
        fi
    fi
    
    print_message "✅ Cleanup completed successfully" "$GREEN"
    return 0
}

# Function to start BirdNET-Go
start_birdnet_go() {   
    log_message "INFO" "Starting BirdNET-Go service"
    print_message "\n🚀 Starting BirdNET-Go..." "$GREEN"
    
    # Check if container is already running
    if check_container_running; then
        log_message "INFO" "BirdNET-Go container already running, skipping startup"
        print_message "✅ BirdNET-Go container is already running" "$GREEN"
        return 0
    fi
    
    # Start the service
    log_message "INFO" "Executing systemctl start birdnet-go.service"
    sudo systemctl start birdnet-go.service
    log_command_result "systemctl start birdnet-go.service" $? "starting BirdNET-Go service"
    
    # Check if service started
    if ! sudo systemctl is-active --quiet birdnet-go.service; then
        log_message "ERROR" "BirdNET-Go service failed to start"
        send_telemetry_event "error" "Service startup failed" "error" "step=start_birdnet_go"
        print_message "❌ Failed to start BirdNET-Go service" "$RED"
        
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

# Function to detect Raspberry Pi model
detect_rpi_model() {
    if [ -f /proc/device-tree/model ]; then
        local model
        model=$(tr -d '\0' < /proc/device-tree/model)
        case "$model" in
            *"Raspberry Pi 5"*)
                print_message "✅ Detected Raspberry Pi 5" "$GREEN"
                return 5
                ;;
            *"Raspberry Pi 4"*)
                print_message "✅ Detected Raspberry Pi 4" "$GREEN"
                return 4
                ;;
            *"Raspberry Pi 3"*)
                print_message "✅ Detected Raspberry Pi 3" "$GREEN"
                return 3
                ;;
            *"Raspberry Pi Zero 2"*)
                print_message "✅ Detected Raspberry Pi Zero 2" "$GREEN"
                return 2
                ;;
            *)
                print_message "ℹ️ Unknown Raspberry Pi model: $model" "$YELLOW"
                return 0
                ;;
        esac
    fi

    # Return 0 if no Raspberry Pi model is detected
    return 0
}

# Function to configure performance settings based on RPi model
optimize_settings() {
    print_message "\n⏱️ Optimizing settings based on system performance" "$GREEN"
    # enable XNNPACK delegate for inference acceleration
    sed -i 's/usexnnpack: false/usexnnpack: true/' "$CONFIG_FILE"
    print_message "✅ Enabled XNNPACK delegate for inference acceleration" "$GREEN"

    # Check if system is Raspberry Pi and inform about WiFi power saving
    if is_raspberry_pi; then
        print_message "🔧 WiFi power saving will be disabled on startup to prevent connection drops" "$YELLOW"
    fi

    # Detect RPi model
    detect_rpi_model
    local rpi_model=$?
    
    case $rpi_model in
        5)
            # RPi 5 settings
            sed -i 's/overlap: 1.5/overlap: 2.7/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi 5" "$GREEN"
            ;;
        4)
            # RPi 4 settings
            sed -i 's/overlap: 1.5/overlap: 2.6/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi 4" "$GREEN"
            ;;
        3)
            # RPi 3 settings
            sed -i 's/overlap: 1.5/overlap: 2.0/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi 3" "$GREEN"
            ;;
        2)
            # RPi Zero 2 settings
            sed -i 's/overlap: 1.5/overlap: 2.0/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi Zero 2" "$GREEN"
            ;;
    esac
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
# Configured timezone (will be set during configuration)
CONFIGURED_TZ=""


# Load telemetry configuration if it exists
load_telemetry_config

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

# Function to display menu options based on installation type
display_menu() {
    local installation_type="$1"
    
    if [ "$installation_type" = "full" ]; then
        print_message "🔍 Found existing BirdNET-Go installation (systemd service)" "$YELLOW"
        print_message "1) Check for updates" "$YELLOW"
        if has_previous_versions; then
            print_message "2) Revert to previous version" "$YELLOW"
        else
            print_message "2) Revert to previous version (no versions available)" "$GRAY"
        fi
        print_message "3) Fresh installation" "$YELLOW"
        print_message "4) Uninstall BirdNET-Go, remove data" "$YELLOW"
        print_message "5) Uninstall BirdNET-Go, preserve data" "$YELLOW"
        print_message "6) Exit" "$YELLOW"
        print_message "❓ Select an option (1-6): " "$YELLOW" "nonewline"
        return 6  # Return number of options
    elif [ "$installation_type" = "docker" ]; then
        print_message "🔍 Found existing BirdNET-Go Docker container/image" "$YELLOW"
        print_message "1) Check for updates" "$YELLOW"
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
        print_message "1) Install using existing data and configuration" "$YELLOW"
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
            print_message "👋 Goodbye!" "$GREEN"
            exit 0
            ;;
        *)
            print_message "❌ Invalid option" "$RED"
            exit 1
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
            print_message "\n🔄 Updating BirdNET-Go Docker image..." "$YELLOW"
            
            if docker pull "${BIRDNET_GO_IMAGE}"; then
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
                
                # Send telemetry for failure
                send_telemetry_event "error" "Docker image update failed" "error" "step=docker_update"
                
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

# Menu loop for existing installations
if [ "$INSTALLATION_TYPE" != "none" ] || [ "$PRESERVED_DATA" = true ]; then
    while true; do
        # Display menu based on installation type
        print_message ""  # Add spacing
        display_menu "$INSTALLATION_TYPE"
        max_options=$?
        
        # Read user selection
        read -r response
        
        # Validate user selection
        if [[ "$response" =~ ^[0-9]+$ ]] && [ "$response" -ge 1 ] && [ "$response" -le "$max_options" ]; then
            # Handle menu selection
            handle_menu_selection "$INSTALLATION_TYPE" "$response"
            menu_result=$?
            
            # If menu action succeeded (returned 0), break from loop and continue
            if [ $menu_result -eq 0 ]; then
                break
            fi
            # If menu action failed/cancelled (returned 1), continue loop to show menu again
        else
            print_message "❌ Invalid option. Please select a number between 1 and $max_options." "$RED"
            # Continue loop to show menu again
        fi
    done
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

# Download base config file
download_base_config

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

# Configure telemetry (only if not already configured or fresh install)
if [ "$FRESH_INSTALL" = "true" ] || [ "$TELEMETRY_ENABLED" = "" ]; then
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

print_message ""
print_message "✅ Installation completed!" "$GREEN"
print_message "📁 Configuration directory: " "$GREEN" "nonewline"
print_message "$CONFIG_DIR"
print_message "📁 Data directory: " "$GREEN" "nonewline"
print_message "$DATA_DIR"

# Get IP address
IP_ADDR=$(get_ip_address)
if [ -n "$IP_ADDR" ]; then
    log_message "INFO" "Web interface accessible at: http://${IP_ADDR}:${WEB_PORT}"
    print_message "🌐 BirdNET-Go web interface is available at http://${IP_ADDR}:${WEB_PORT}" "$GREEN"
else
    log_message "WARN" "Could not determine IP address for web interface access"
    print_message "⚠️ Could not determine IP address - you may access BirdNET-Go at http://localhost:${WEB_PORT}" "$YELLOW"
    print_message "To find your IP address manually, run: ip addr show or nmcli device show" "$YELLOW"
fi

# Check if mDNS is available
if check_mdns; then
    HOSTNAME=$(hostname)
    log_message "INFO" "mDNS available, accessible at: http://${HOSTNAME}.local:${WEB_PORT}"
    print_message "🌐 Also available at http://${HOSTNAME}.local:${WEB_PORT}" "$GREEN"
else
    log_message "INFO" "mDNS not available"
fi

# Display Cockpit URL if installed
if [ "$(check_cockpit_status 2>/dev/null)" = "installed" ] && is_cockpit_installed; then
    if [ -n "$IP_ADDR" ]; then
        log_message "INFO" "Cockpit web interface accessible at: https://${IP_ADDR}:${COCKPIT_PORT}"
        print_message "🖥️ Cockpit system management interface: https://${IP_ADDR}:${COCKPIT_PORT}" "$GREEN"
    else
        print_message "🖥️ Cockpit system management interface: https://localhost:${COCKPIT_PORT}" "$GREEN"
    fi
    
    if check_mdns; then
        HOSTNAME=$(hostname)
        print_message "🖥️ Cockpit also available at: https://${HOSTNAME}.local:${COCKPIT_PORT}" "$GREEN"
    fi
    
    print_message "ℹ️ Use your system username and password to log into Cockpit" "$YELLOW"
fi

# Configure Cockpit installation as final step
configure_cockpit

log_message "INFO" "Install.sh script execution completed successfully"
log_message "INFO" "=== End of BirdNET-Go Installation/Update Session ==="

