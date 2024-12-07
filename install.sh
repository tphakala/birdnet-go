#!/bin/bash

# Exit on error
set -e

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
    ip -4 addr show | grep -v 'docker\|br-\|veth\|lo' | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -n 1
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

# Function to check and install required packages
check_install_package() {
    if ! dpkg -l "$1" >/dev/null 2>&1; then
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

    # Check and install Docker
    if ! command_exists docker; then
        print_message "🐳 Docker not found. Installing Docker..." "$YELLOW"
        # Install Docker from apt repository
        sudo apt -qq update
        sudo apt -qq install -y docker.io
        # Add current user to docker group
        if sudo usermod -aG docker "$USER"; then
            print_message "✅ Added user $USER to docker group" "$GREEN"
        fi
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
        print_message "✅ Docker installed successfully. To make group member changes take effect, please log out and log back in and rerun install.sh to continue with install" "$GREEN"
        print_message "Exiting install script..." "$YELLOW"
        # exit install script
        exit 0
    else
        print_message "✅ Docker found" "$GREEN"
    fi

    print_message "🥳 System prerequisites checks passed" "$GREEN"
}

# Function to check if directories can be created
check_directory() {
    local dir="$1"
    if [ ! -d "$dir" ]; then
        if ! mkdir -p "$dir" 2>/dev/null; then
            print_message "❌ Cannot create directory $dir" "$RED"
            exit 1
        fi
    elif [ ! -w "$dir" ]; then
        print_message "❌ Cannot write to directory $dir" "$RED"
        exit 1
    fi
}

# Function to pull Docker image
pull_docker_image() {
    print_message "\n🐳 Pulling BirdNET-Go Docker image from GitHub Container Registry..." "$YELLOW"
    if docker pull "${BIRDNET_GO_IMAGE}"; then
        print_message "✅ Docker image pulled successfully" "$GREEN"
    else
        print_message "❌ Failed to pull Docker image" "$RED"
        print_message "This could be due to:" "$YELLOW"
        print_message "- No internet connection" "$YELLOW"
        print_message "- GitHub container registry being unreachable" "$YELLOW"
        print_message "- Invalid image name or tag" "$YELLOW"
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
    if command_exists ffprobe; then
        print_message "🧪 Testing RTSP connection..." "$YELLOW"
        if ffprobe -v quiet -i "$url" -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 2>/dev/null; then
            return 0
        fi
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
                configure_sound_card
                break
                ;;
            2)
                configure_rtsp_stream
                break
                ;;
            *)
                print_message "❌ Invalid selection. Please try again." "$RED"
                ;;
        esac
    done
}

# Function to configure sound card
configure_sound_card() {
    print_message "\n🎤 Detected audio devices:" "$GREEN"
    
    # Create an array to store device information
    declare -a devices
    declare -a device_names
    
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
            echo "[$((${#devices[@]}))] Card $card_num: $card_name"
            echo "    Device $device_num: $device_name [$device_desc]"
        fi
    done < <(arecord -l)

    if [ ${#devices[@]} -eq 0 ]; then
        print_message "❌ No audio capture devices found!" "$RED"
        exit 1
    fi

    while true; do
        print_message "\nPlease select a device number from the list above (1-${#devices[@]}): " "$YELLOW" "nonewline"
        read -r selection

        if [[ "$selection" =~ ^[0-9]+$ ]] && [ "$selection" -ge 1 ] && [ "$selection" -le "${#devices[@]}" ]; then
            ALSA_CARD="${devices[$((selection-1))]}"
            print_message "✅ Selected capture device: " "$GREEN" "nonewline"
            print_message "$ALSA_CARD"
            break
        else
            print_message "❌ Invalid selection. Please try again." "$RED"
        fi
    done
    
    # Update config file
    sed -i "s/source: \"sysdefault\"/source: \"${ALSA_CARD}\"/" "$CONFIG_FILE"
    # Comment out RTSP section
    sed -i '/rtsp:/,/      # - rtsp/s/^/#/' "$CONFIG_FILE"
    
    AUDIO_ENV="--device /dev/snd"
}

# Function to configure RTSP stream
configure_rtsp_stream() {
    while true; do
        print_message "\nEnter RTSP URL (format: rtsp://user:password@address/path): " "$YELLOW" "nonewline"
        read -r RTSP_URL
        
        if [[ ! $RTSP_URL =~ ^rtsp:// ]]; then
            print_message "❌ Invalid RTSP URL format. Please try again." "$RED"
            continue
        fi
        
        if test_rtsp_url "$RTSP_URL"; then
            print_message "✅ RTSP connection successful!" "$GREEN"
            break
        else
            print_message "❌ Could not connect to RTSP stream. Do you want to try again? (y/n)" "$RED"
            read -r retry
            if [[ $retry != "y" ]]; then
                break
            fi
        fi
    done
    
    # Update config file
    sed -i "s|# - rtsp://user:password@example.com/stream1|      - ${RTSP_URL}|" "$CONFIG_FILE"
    # Comment out audio source section
    sed -i '/source: "sysdefault"/s/^/#/' "$CONFIG_FILE"
    
    AUDIO_ENV=""
}

# Function to configure audio export format
configure_audio_format() {
    print_message "\n🔊 Audio Export Configuration" "$GREEN"
    print_message "Select audio format for captured sounds:"
    print_message "1) WAV (Uncompressed, largest files)" 
    print_message "2) FLAC (Lossless compression)"
    print_message "3) AAC (High quality, smaller files) - recommended" 
    print_message "4) MP3 (For legacy use only)" 
    print_message "5) Opus (Best compression)" 
    
    while true; do
        print_message "❓ Select format (1-5): " "$YELLOW" "nonewline"
        read -r format_choice

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

# Function to configure location
configure_location() {
    print_message "\n🌍 Location Configuration, this is used to limit bird species present in your region" "$GREEN"
    print_message "1) Enter coordinates manually" "$YELLOW"
    print_message "2) Enter city name" "$YELLOW"
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
            ;;
        2)
            while true; do
                read -p "Enter city name: " city
                read -p "Enter country code (e.g., US, FI): " country
                
                # Use OpenStreetMap Nominatim API to get coordinates
                coordinates=$(curl -s "https://nominatim.openstreetmap.org/search?city=${city}&country=${country}&format=json" | jq -r '.[0] | "\(.lat) \(.lon)"')
                
                if [ -n "$coordinates" ] && [ "$coordinates" != "null null" ]; then
                    lat=$(echo "$coordinates" | cut -d' ' -f1)
                    lon=$(echo "$coordinates" | cut -d' ' -f2)
                    print_message "✅ Found coordinates: " "$GREEN" "nonewline"
                    print_message "$lat, $lon"
                    break
                else
                    print_message "❌ Could not find coordinates for the specified city. Please try again." "$RED"
                fi
            done
            ;;
        *)
            print_message "❌ Invalid selection. Exiting." "$RED"
            exit 1
            ;;
    esac

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
print_message "  - Installing system packages (alsa-utils, curl, ffmpeg, bc, jq, apache2-utils)" "$YELLOW"
print_message "  - Installing Docker" "$YELLOW"
print_message "  - Creating systemd service" "$YELLOW"
print_message ""

# Default paths
CONFIG_DIR="$HOME/birdnet-go-app/config"
DATA_DIR="$HOME/birdnet-go-app/data"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
TEMP_CONFIG="/tmp/config.yaml"

# Check prerequisites before proceeding
check_prerequisites

# Update package list
print_message "\n🔧 Updating package list..." "$YELLOW"
sudo apt -qq update

# Install required packages
print_message "\n🔧 Checking and installing required packages..." "$YELLOW"
check_install_package "alsa-utils"
check_install_package "curl"
check_install_package "ffmpeg"
check_install_package "bc"
check_install_package "jq"
check_install_package "apache2-utils"

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

# Add systemd service configuration
add_systemd_config

# Start BirdNET-Go
start_birdnet_go

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