#!/bin/bash

BIRDNET_GO_VERSION="dev"
BIRDNET_GO_IMAGE="ghcr.io/tphakala/birdnet-go:${BIRDNET_GO_VERSION}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored messages
print_message() {
    if [ "$3" = "nonewline" ]; then
        echo -en "${2}${1}${NC}"
    else
        echo -e "${2}${1}${NC}"
    fi
}

# Function to get IP address
get_ip_address() {
    # Get primary IP address, excluding docker and localhost interfaces
    ip -4 addr show scope global | grep -v 'docker\|br-\|veth' | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -n 1
}

# Function to check if mDNS is available
check_mdns() {
    if systemctl is-active --quiet avahi-daemon; then
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
        print_message "❌ No network connectivity (ping test failed)" "$RED"
        print_message "Please check your internet connection and try again" "$YELLOW"
        exit 1
    fi

    # Now ensure curl is available for further tests
    ensure_curl

    # Continue with existing HTTP/HTTPS checks...
    local test_hosts=("github.com" "raw.githubusercontent.com" "ghcr.io")
    
    # HTTP/HTTPS Check
    print_message "\n📡 Testing HTTP/HTTPS connectivity..." "$YELLOW"
    local urls=(
        "https://github.com"
        "https://raw.githubusercontent.com"
        "https://ghcr.io"
    )
    
    for url in "${urls[@]}"; do
        local http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "$url")
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

# Function to check and install required packages
check_install_package() {
    if ! dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"; then
        print_message "🔧 Installing $1..." "$YELLOW"
        if sudo apt install -qq -y "$1"; then
            print_message "✅ $1 installed successfully" "$GREEN"
        else
            print_message "❌ Failed to install $1" "$RED"
            exit 1
        fi
    else
        print_message "✅ $1 found" "$GREEN"
    fi
}

# Function to check system prerequisites
check_prerequisites() {
    print_message "🔧 Checking system prerequisites..." "$YELLOW"

    # Check CPU architecture and generation
    case "$(uname -m)" in
        "x86_64")
            # Check CPU flags for AVX2 (Haswell and newer)
            if ! grep -q "avx2" /proc/cpuinfo; then
                print_message "❌ Your Intel CPU is too old. BirdNET-Go requires Intel Haswell (2013) or newer CPU with AVX2 support" "$RED"
                exit 1
            else
                print_message "✅ Intel CPU architecture and generation check passed" "$GREEN"
            fi
            ;;
        "aarch64"|"arm64")
            print_message "✅ ARM 64-bit architecture detected, continuing with installation" "$GREEN"
            ;;
        "armv7l"|"armv6l"|"arm")
            print_message "❌ 32-bit ARM architecture detected. BirdNET-Go requires 64-bit ARM processor and OS" "$RED"
            exit 1
            ;;
        *)
            print_message "❌ Unsupported CPU architecture: $(uname -m)" "$RED"
            exit 1
            ;;
    esac

    # Get OS information
    if [ -f /etc/os-release ]; then
        . /etc/os-release
    else
        print_message "❌ Cannot determine OS version" "$RED"
        exit 1
    fi

    # Check for supported distributions
    case "$ID" in
        debian)
            # Debian 11 (Bullseye) has VERSION_ID="11"
            if [ -n "$VERSION_ID" ] && [ "$VERSION_ID" -lt 11 ]; then
                print_message "❌ Debian $VERSION_ID too old. Version 11 (Bullseye) or newer required" "$RED"
                exit 1
            else
                print_message "✅ Debian $VERSION_ID found" "$GREEN"
            fi
            ;;
        raspbian)
            print_message "❌ You are running 32-bit version of Raspberry Pi OS. BirdNET-Go requires 64-bit version" "$RED"
            exit 1
            ;;
        ubuntu)
            # Ubuntu 20.04 has VERSION_ID="20.04"
            ubuntu_version=$(echo "$VERSION_ID" | awk -F. '{print $1$2}')
            if [ "$ubuntu_version" -lt 2004 ]; then
                print_message "❌ Ubuntu $VERSION_ID too old. Version 20.04 or newer required" "$RED"
                exit 1
            else
                print_message "✅ Ubuntu $VERSION_ID found" "$GREEN"
            fi
            ;;
        *)
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

        if [ "$groups_added" = true ]; then
            print_message "Please log out and log back in for group changes to take effect, and rerun install.sh to continue with install" "$YELLOW"
            exit 0
        fi
    }

    # Check and install Docker
    if ! command_exists docker; then
        print_message "🐳 Docker not found. Installing Docker..." "$YELLOW"
        # Install Docker from apt repository
        sudo apt -qq update
        sudo apt -qq install -y docker.io
        # Add current user to required groups
        add_user_to_groups
        # Start Docker service
        if sudo systemctl start docker; then
            print_message "✅ Docker service started successfully" "$GREEN"
        else
            print_message "❌ Failed to start Docker service" "$RED"
            exit 1
        fi
        
        # Enable Docker service on boot
        if  sudo systemctl enable docker; then
            print_message "✅ Docker service start on boot enabled successfully" "$GREEN"
        else
            print_message "❌ Failed to enable Docker service on boot" "$RED"
            exit 1
        fi
        print_message "⚠️ Docker installed successfully. To make group member changes take effect, please log out and log back in and rerun install.sh to continue with install" "$YELLOW"
        # exit install script
        exit 0
    else
        print_message "✅ Docker found" "$GREEN"
        
        # Check if user is in required groups
        add_user_to_groups

        # Check if Docker can be used by the user
        if ! docker info &>/dev/null; then
            print_message "❌ Docker cannot be accessed by user $USER. Please ensure you have the necessary permissions." "$RED"
            exit 1
        else
            print_message "✅ Docker is accessible by user $USER" "$GREEN"
        fi
    fi

    print_message "🥳 System prerequisites checks passed" "$GREEN"
    print_message ""
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

# Function to check if there is enough disk space for Docker image
check_docker_space() {
    local required_space=2000000  # 2GB in KB
    local available_space=$(df -k /var/lib/docker | awk 'NR==2 {print $4}')
    
    if [ "$available_space" -lt "$required_space" ]; then
        print_message "❌ Insufficient disk space for Docker image" "$RED"
        print_message "Required: 2GB, Available: $((available_space/1024))MB" "$YELLOW"
        exit 1
    fi
}

# Function to pull Docker image
pull_docker_image() {
    print_message "\n🐳 Pulling BirdNET-Go Docker image from GitHub Container Registry..." "$YELLOW"
    
    # Check if Docker can be used by the user
    if ! docker info &>/dev/null; then
        print_message "❌ Docker cannot be accessed by user $USER. Please ensure you have the necessary permissions." "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- User $USER is not in the docker group" "$YELLOW"
        print_message "- Docker service is not running" "$YELLOW"
        print_message "- Insufficient privileges to access Docker socket" "$YELLOW"
        exit 1
    fi

    if docker pull "${BIRDNET_GO_IMAGE}"; then
        print_message "✅ Docker image pulled successfully" "$GREEN"
    else
        print_message "❌ Failed to pull Docker image" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- No internet connection" "$YELLOW"
        print_message "- GitHub container registry being unreachable" "$YELLOW"
        print_message "- Invalid image name or tag" "$YELLOW"
        print_message "- Insufficient privileges to access Docker socket on local system" "$YELLOW"
        exit 1
    fi
}

# Function to download base config file
download_base_config() {
    print_message "\n📥 Downloading base configuration file from GitHub to: " "$YELLOW" "nonewline"
    print_message "$CONFIG_FILE" "$NC"
    
    # Download new config to temporary file first
    local temp_config="/tmp/config.yaml.new"
    if ! curl -s --fail https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml > "$temp_config"; then
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
            return 0
        fi

        print_message "⚠️ Existing configuration file found." "$YELLOW"
        print_message "❓ Do you want to overwrite it? Backup of current configuration will be created (y/n): " "$YELLOW" "nonewline"
        read -r response
        
        if [[ "$response" =~ ^[Yy]$ ]]; then
            # Create backup with timestamp
            local backup_file="${CONFIG_FILE}.$(date '+%Y%m%d_%H%M%S').backup"
            cp "$CONFIG_FILE" "$backup_file"
            print_message "✅ Backup created: " "$GREEN" "nonewline"
            print_message "$backup_file" "$NC"
            
            mv "$temp_config" "$CONFIG_FILE"
            print_message "✅ Configuration updated successfully" "$GREEN"
        else
            print_message "✅ Keeping existing configuration file" "$YELLOW"
            rm -f "$temp_config"
        fi
    else
        mv "$temp_config" "$CONFIG_FILE"
        print_message "✅ Base configuration downloaded successfully" "$GREEN"
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
    while true; do
        print_message "\n🎤 Audio Capture Configuration" "$GREEN"
        print_message "1) Use sound card" 
        print_message "2) Use RTSP stream" 
        print_message "❓ Select audio input method (1/2): " "$YELLOW" "nonewline"
        read -r audio_choice

        case $audio_choice in
            1)
                if configure_sound_card; then
                    break
                fi
                ;;
            2)
                if configure_rtsp_stream; then
                    break
                fi
                ;;
            *)
                print_message "❌ Invalid selection. Please try again." "$RED"
                ;;
        esac
    done
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

    # Test audio device access
    if ! arecord -c 1 -f S16_LE -r 48000 -d 1 -D "$device" /dev/null 2>/dev/null; then
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
    while true; do
        print_message "\n🎤 Detected audio devices:" "$GREEN"
        
        # Create arrays to store device information
        declare -a devices
        declare -a device_names
        local default_selection=0
        
        # Capture arecord output to a variable first
        local arecord_output
        arecord_output=$(arecord -l 2>/dev/null)
        
        if [ -z "$arecord_output" ]; then
            print_message "❌ No audio capture devices found!" "$RED"
            return 1
        fi
        
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
            print_message "❌ No audio capture devices found!" "$RED"
            return 1
        fi

        # If no USB device was found, use first device as default
        if [ $default_selection -eq 0 ]; then
            default_selection=1
        fi

        print_message "\nPlease select a device number from the list above (1-${#devices[@]}) [${default_selection}] or 'b' to go back: " "$YELLOW" "nonewline"
        read -r selection

        if [ "$selection" = "b" ]; then
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
                if [[ $line =~ ^card[[:space:]]+([0-9]+)[[:space:]]*:[[:space:]]*([^,]+),[[:space:]]*device[[:space:]]+([0-9]+) ]]; then
                    if [ $index -eq $selection ]; then
                        card_num="${BASH_REMATCH[1]}"
                        device_num="${BASH_REMATCH[3]}"
                        break
                    fi
                    ((index++))
                fi
            done <<< "$(arecord -l)"

            local alsa_hw="hw:${card_num},${device_num}"  # Use actual card and device numbers
            
            ALSA_CARD="$friendly_name"
            print_message "✅ Selected capture device: " "$GREEN" "nonewline"
            print_message "$ALSA_CARD"

            # validate audio device using ALSA hw format
            if ! validate_audio_device "$alsa_hw"; then
                print_message "❌ Failed to validate audio device" "$RED"
                return 1                
            fi

            # Update config file with the friendly name
            sed -i "s/source: \"sysdefault\"/source: \"${ALSA_CARD}\"/" "$CONFIG_FILE"
            # Comment out RTSP section
            sed -i '/rtsp:/,/      # - rtsp/s/^/#/' "$CONFIG_FILE"
                
            AUDIO_ENV="--device /dev/snd"
            return 0
        else
            print_message "❌ Invalid selection. Please try again." "$RED"
        fi
    done
}

# Function to configure RTSP stream
configure_rtsp_stream() {
    while true; do
        print_message "\n🎥 RTSP Stream Configuration" "$GREEN"
        print_message "Configure primary RTSP stream. Additional streams can be added later via web interface at Audio Capture Settings." "$YELLOW"
        print_message "Enter RTSP URL (format: rtsp://user:password@address:port/path) or 'b' to go back: " "$YELLOW" "nonewline"
        read -r RTSP_URL

        if [ "$RTSP_URL" = "b" ]; then
            return 1
        fi
        
        if [[ ! $RTSP_URL =~ ^rtsp:// ]]; then
            print_message "❌ Invalid RTSP URL format. Please try again." "$RED"
            continue
        fi
        
        if test_rtsp_url "$RTSP_URL"; then
            print_message "✅ RTSP connection successful!" "$GREEN"
            
            # Update config file
            sed -i "s|# - rtsp://user:password@example.com/stream1|      - ${RTSP_URL}|" "$CONFIG_FILE"
            # Comment out audio source section
            sed -i '/source: "sysdefault"/s/^/#/' "$CONFIG_FILE"
            
            AUDIO_ENV=""
            return 0
        else
            print_message "❌ Could not connect to RTSP stream. Do you want to:" "$RED"
            print_message "1) Try again"
            print_message "2) Go back to audio input selection"
            print_message "❓ Select option (1/2): " "$YELLOW" "nonewline"
            read -r retry
            if [ "$retry" = "2" ]; then
                return 1
            fi
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
    declare -a locale_codes=("af" "ca" "cs" "zh" "hr" "da" "nl" "en" "et" "fi" "fr" "de" "el" "hu" "is" "id" "it" "ja" "lv" "lt" "no" "pl" "pt" "ru" "sk" "sl" "es" "sv" "th" "uk")
    declare -a locale_names=("Afrikaans" "Catalan" "Czech" "Chinese" "Croatian" "Danish" "Dutch" "English" "Estonian" "Finnish" "French" "German" "Greek" "Hungarian" "Icelandic" "Indonesia" "Italian" "Japanese" "Latvian" "Lithuania" "Norwegian" "Polish" "Portuguese" "Russian" "Slovak" "Slovenian" "Spanish" "Swedish" "Thai" "Ukrainian")
    
    # Display available locales
    for i in "${!locale_codes[@]}"; do
        printf "%2d) %-12s" "$((i+1))" "${locale_names[i]}"
        if [ $((i % 3)) -eq 2 ]; then
            echo
        fi
    done
    echo

    while true; do
        print_message "❓ Select your language (1-${#locale_codes[@]}): " "$YELLOW" "nonewline"
        read -r selection
        
        if [[ "$selection" =~ ^[0-9]+$ ]] && [ "$selection" -ge 1 ] && [ "$selection" -le "${#locale_codes[@]}" ]; then
            LOCALE_CODE="${locale_codes[$((selection-1))]}"
            print_message "✅ Selected language: " "$GREEN" "nonewline"
            print_message "${locale_names[$((selection-1))]}"
            # Update config file
            sed -i "s/locale: en/locale: ${LOCALE_CODE}/" "$CONFIG_FILE"
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
    nordvpn_info=$(curl -s "https://nordvpn.com/wp-admin/admin-ajax.php" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        --data-urlencode "action=get_user_info_data")
    
    if [ $? -eq 0 ] && [ -n "$nordvpn_info" ]; then
        local city=$(echo "$nordvpn_info" | jq -r '.city')
        local country=$(echo "$nordvpn_info" | jq -r '.country')
        
        if [ "$city" != "null" ] && [ "$country" != "null" ] && [ -n "$city" ] && [ -n "$country" ]; then
            # Use OpenStreetMap to get precise coordinates
            local coordinates
            coordinates=$(curl -s "https://nominatim.openstreetmap.org/search?city=${city}&country=${country}&format=json" | jq -r '.[0] | "\(.lat) \(.lon)"')
            
            if [ -n "$coordinates" ] && [ "$coordinates" != "null null" ]; then
                local lat=$(echo "$coordinates" | cut -d' ' -f1)
                local lon=$(echo "$coordinates" | cut -d' ' -f2)
                echo "$lat|$lon|$city|$country"
                return 0
            fi
        fi
    fi
    return 1
}

# Function to configure location
configure_location() {
    print_message "\n🌍 Location Configuration, this is used to limit bird species present in your region" "$GREEN"
    
    # Try to get location from NordVPN/OpenStreetMap
    local ip_location
    ip_location=$(get_ip_location)
    
    if [ $? -eq 0 ]; then
        local ip_lat=$(echo "$ip_location" | cut -d'|' -f1)
        local ip_lon=$(echo "$ip_location" | cut -d'|' -f2)
        local ip_city=$(echo "$ip_location" | cut -d'|' -f3)
        local ip_country=$(echo "$ip_location" | cut -d'|' -f4)
        
        print_message "📍 Based on your IP address, your location appears to be: " "$YELLOW" "nonewline"
        print_message "$ip_city, $ip_country ($ip_lat, $ip_lon)" "$NC"
        print_message "❓ Would you like to use this location? (y/n): " "$YELLOW" "nonewline"
        read -r use_ip_location
        
        if [[ $use_ip_location == "y" ]]; then
            lat=$ip_lat
            lon=$ip_lon
            print_message "✅ Using IP-based location" "$GREEN"
            # Update config file and return
            sed -i "s/latitude: 00.000/latitude: $lat/" "$CONFIG_FILE"
            sed -i "s/longitude: 00.000/longitude: $lon/" "$CONFIG_FILE"
            return
        fi
    else
        print_message "⚠️ Could not automatically determine location" "$YELLOW"
    fi
    
    # If automatic location failed or was rejected, continue with manual input
    print_message "1) Enter coordinates manually" "$YELLOW"
    print_message "2) Enter city name for OpenStreetMap lookup" "$YELLOW"
    
    while true; do
        print_message "❓ Select location input method (1/2): " "$YELLOW" "nonewline"
        read -r location_choice

        case $location_choice in
            1)
                while true; do
                    read -p "Enter latitude (-90 to 90): " lat
                    read -p "Enter longitude (-180 to 180): " lon
                    
                    if [[ "$lat" =~ ^-?[0-9]*\.?[0-9]+$ ]] && \
                       [[ "$lon" =~ ^-?[0-9]*\.?[0-9]+$ ]] && \
                       (( $(echo "$lat >= -90 && $lat <= 90" | bc -l) )) && \
                       (( $(echo "$lon >= -180 && $lon <= 180" | bc -l) )); then
                        break
                    else
                        print_message "❌ Invalid coordinates. Please try again." "$RED"
                    fi
                done
                break
                ;;
            2)
                while true; do
                    print_message "Enter location (e.g., 'Helsinki, Finland', 'New York, US', or 'Sungei Buloh, Singapore'): " "$YELLOW" "nonewline"
                    read -r location
                    
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
                        print_message "✅ Found coordinates for $city, $country: " "$GREEN" "nonewline"
                        print_message "$lat, $lon"
                        break
                    else
                        print_message "❌ Could not find coordinates. Please try again with format: 'City, Country'" "$RED"
                    fi
                done
                break
                ;;
            *)
                print_message "❌ Invalid selection. Please try again." "$RED"
                ;;
        esac
    done

    # Update config file
    sed -i "s/latitude: 00.000/latitude: $lat/" "$CONFIG_FILE"
    sed -i "s/longitude: 00.000/longitude: $lon/" "$CONFIG_FILE"
}

# Function to configure basic authentication
configure_auth() {
    print_message "\n🔒 Security Configuration" "$GREEN"
    print_message "Do you want to enable password protection for the settings interface?" "$YELLOW"
    print_message "This is highly recommended if BirdNET-Go will be accessible from the internet." "$YELLOW"
    print_message "❓ Enable password protection? (y/n): " "$YELLOW" "nonewline"
    read -r enable_auth

    if [[ $enable_auth == "y" ]]; then
        while true; do
            read -p "Enter password: " password
            read -p "Confirm password: " password2
            
            if [ "$password" = "$password2" ]; then
                # Generate password hash (using bcrypt)
                password_hash=$(echo -n "$password" | htpasswd -niB "" | cut -d: -f2)
                
                # Update config file - using different delimiter for sed
                sed -i "s|enabled: false    # true to enable basic auth|enabled: true    # true to enable basic auth|" "$CONFIG_FILE"
                sed -i "s|password: \"\"|password: \"$password_hash\"|" "$CONFIG_FILE"
                
                print_message "✅ Password protection enabled successfully!" "$GREEN"
                print_message "If you forget your password, you can reset it by editing:" "$YELLOW"
                print_message "$CONFIG_FILE" "$YELLOW"
                sleep 3
                break
            else
                print_message "❌ Passwords don't match. Please try again." "$RED"
            fi
        done
    fi
}

# Function to add systemd service configuration
add_systemd_config() {
    # Get timezone
    if [ -f /etc/timezone ]; then
        TZ=$(cat /etc/timezone)
    else
        TZ="UTC"
    fi

    # Create systemd service
    print_message "\n🚀 Creating systemd service..." "$GREEN"
    sudo tee /etc/systemd/system/birdnet-go.service << EOF
[Unit]
Description=BirdNET-Go
After=docker.service
Requires=docker.service

[Service]
Restart=always
ExecStart=/usr/bin/docker run --rm \\
    -p 8080:8080 \\
    --env TZ="${TZ}" \\
    ${AUDIO_ENV} \\
    -v ${CONFIG_DIR}:/config \\
    -v ${DATA_DIR}:/data \\
    ${BIRDNET_GO_IMAGE}

[Install]
WantedBy=multi-user.target
EOF

    # Reload systemd and enable service
    sudo systemctl daemon-reload
    sudo systemctl enable birdnet-go.service
}

# Function to check and handle existing BirdNET-Go containers
check_existing_containers() {
    print_message "🐳 Checking for existing BirdNET-Go containers..." "$YELLOW"
    local existing_containers=$(docker ps --format "{{.ID}}: {{.Command}}" --no-trunc | grep "/usr/bin/birdnet-go" | cut -d: -f1)
    
    if [ -n "$existing_containers" ]; then
        print_message "⚠️ Found existing BirdNET-Go container: " "$YELLOW" "nonewline"
        print_message "$existing_containers"
        print_message "❓ Do you want to update BirdNET-Go? (y/n): " "$YELLOW" "nonewline"
        read -r response
        
        if [[ "$response" =~ ^[Yy]$ ]]; then
            # First stop the systemd service to prevent auto-restart
            print_message "🛑 Stopping BirdNET-Go service..." "$YELLOW"
            if sudo systemctl stop birdnet-go.service; then
                print_message "✅ Service stopped successfully" "$GREEN"
                print_message ""
            else
                print_message "⚠️ Could not stop service, attempting to stop container directly..." "$YELLOW"
                # Then stop the container
                print_message "🛑 Stopping container..." "$YELLOW"
                docker stop "$existing_containers"
                print_message "✅ Container stopped successfully" "$GREEN"
                print_message ""
            fi
            

            # Continue with installation/update
            return 0
        else
            print_message "❌ Installation cancelled" "$RED"
            exit 1
        fi
    fi
}

# Function to start BirdNET-Go
start_birdnet_go() {   
    print_message "\n🚀 Starting BirdNET-Go..." "$GREEN"
    sudo systemctl start birdnet-go.service
    # check status
    if sudo systemctl is-active --quiet birdnet-go.service; then
        print_message "✅ BirdNET-Go started successfully!" "$GREEN"
        print_message "\n🐳 Checking container logs..." "$YELLOW"
        # Wait a moment for the container to start
        sleep 2
        # Get container ID and show logs
        container_id=$(docker ps --filter "ancestor=${BIRDNET_GO_IMAGE}" --format "{{.ID}}")
        if [ -n "$container_id" ]; then
            docker logs "$container_id"
            print_message "\nTo follow logs in real-time, use:" "$YELLOW"
            print_message "docker logs -f $container_id" "$NC"
        else
            print_message "❌ Container not found. Please check 'docker ps' output." "$RED"
            exit 1
        fi
    else
        print_message "❌ Failed to start BirdNET-Go. Please check the logs for errors." "$RED"
        exit 1
    fi
}

# Function to detect Raspberry Pi model
detect_rpi_model() {
    if [ -f /proc/device-tree/model ]; then
        local model=$(tr -d '\0' < /proc/device-tree/model)
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
            sed -i 's/overlap: 1.5/overlap: 2.7/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi 4" "$GREEN"
            ;;
        3)
            # RPi 3 settings
            sed -i 's/overlap: 1.5/overlap: 2.5/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi 3" "$GREEN"
            ;;
        2)
            # RPi Zero 2 settings
            sed -i 's/overlap: 1.5/overlap: 2.5/' "$CONFIG_FILE"
            print_message "✅ Applied optimized settings for Raspberry Pi Zero 2" "$GREEN"
            ;;
    esac
}

# Function to validate installation
validate_installation() {
    print_message "\n🔍 Validating installation..." "$YELLOW"
    local checks=0
    
    # Check Docker container
    if docker ps | grep -q 'birdnet-go'; then
        ((checks++))
    fi
    
    # Check service status
    if systemctl is-active --quiet birdnet-go.service; then
        ((checks++))
    fi
    
    # Check web interface
    if curl -s "http://localhost:8080" >/dev/null; then
        ((checks++))
    fi
    
    if [ "$checks" -eq 3 ]; then
        print_message "✅ Installation validated successfully" "$GREEN"
        return 0
    fi
    print_message "⚠️ Installation validation failed" "$RED"
    return 1
}

# ASCII Art Banner
cat << "EOF"
 ____  _         _ _   _ _____ _____    ____      
| __ )(_)_ __ __| | \ | | ____|_   _|  / ___| ___ 
|  _ \| | '__/ _` |  \| |  _|   | |   | |  _ / _ \
| |_) | | | | (_| | |\  | |___  | |   | |_| | (_) |
|____/|_|_|  \__,_|_| \_|_____| |_|    \____|\___/ 
EOF

print_message "\n🐦 BirdNET-Go Installation Script" "$GREEN"
print_message "This script will install BirdNET-Go and its dependencies." "$YELLOW"
print_message "Note: Root privileges will be required for:" "$YELLOW"
print_message "  - Installing system packages (alsa-utils, curl, bc, jq, apache2-utils)" "$YELLOW"
print_message "  - Installing Docker" "$YELLOW"
print_message "  - Creating systemd service" "$YELLOW"
print_message ""

# Default paths
CONFIG_DIR="$HOME/birdnet-go-app/config"
DATA_DIR="$HOME/birdnet-go-app/data"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
TEMP_CONFIG="/tmp/config.yaml"

# First check basic network connectivity and ensure curl is available
check_network

# Check prerequisites before proceeding
check_prerequisites

# Now proceed with rest of package installation
print_message "\n🔧 Updating package list..." "$YELLOW"
sudo apt -qq update

# Install required packages
print_message "\n🔧 Checking and installing required packages..." "$YELLOW"

# Check which packages need to be installed
REQUIRED_PACKAGES="alsa-utils curl bc jq apache2-utils netcat-openbsd"
TO_INSTALL=""

for pkg in $REQUIRED_PACKAGES; do
    if ! dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null | grep -q "install ok installed"; then
        TO_INSTALL="$TO_INSTALL $pkg"
    else
        print_message "✅ $pkg found" "$GREEN"
    fi
done

# Install missing packages in a single command
if [ ! -z "$TO_INSTALL" ]; then
    print_message "🔧 Installing missing packages:$TO_INSTALL" "$YELLOW"
    if sudo apt install -qq -y $TO_INSTALL; then
        print_message "✅ All packages installed successfully" "$GREEN"
    else
        print_message "❌ Failed to install some packages" "$RED"
        exit 1
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

# Download base config file
download_base_config

# Now lets query user for configuration
print_message "\n🔧 Now lets configure some basic settings" "$YELLOW"

# Configure audio input
configure_audio_input

# Configure audio format
configure_audio_format

# Configure locale
configure_locale

# Configure location
configure_location

# Configure security
configure_auth

# Optimize settings
optimize_settings

# Add systemd service configuration
add_systemd_config

# Start BirdNET-Go
start_birdnet_go

# Validate installation
validate_installation

print_message ""
print_message "✅ Installation completed!" "$GREEN"
print_message "📁 Configuration directory: " "$GREEN" "nonewline"
print_message "$CONFIG_DIR"
print_message "📁 Data directory: " "$GREEN" "nonewline"
print_message "$DATA_DIR"

# Get IP address
IP_ADDR=$(get_ip_address)
if [ -n "$IP_ADDR" ]; then
    print_message "🌐 BirdNET-Go web interface is available at http://${IP_ADDR}:8080" "$GREEN"
fi

# Check if mDNS is available
if check_mdns; then
    HOSTNAME=$(hostname)
    print_message "🌐 Also available at http://${HOSTNAME}.local:8080" "$GREEN"
fi

