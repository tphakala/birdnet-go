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
    echo -e "${2}${1}${NC}"
}

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to check and install required packages
check_install_package() {
    if ! dpkg -l "$1" >/dev/null 2>&1; then
        print_message "Installing $1..." "$YELLOW"
        sudo apt-get install -y "$1"
    fi
}

# Function to check system prerequisites
check_prerequisites() {
    print_message "Checking system prerequisites..." "$YELLOW"

    # Check if system is using apt package manager
    if ! command_exists apt-get; then
        print_message "Error: This script requires an apt-based Linux distribution (Debian, Ubuntu, or Raspberry Pi OS)" "$RED"
        exit 1
    fi

    # Get OS information
    if [ -f /etc/os-release ]; then
        . /etc/os-release
    else
        print_message "Error: Cannot determine OS version" "$RED"
        exit 1
    fi

    # Check for supported distributions
    case "$ID" in
        debian|raspbian)
            # Debian 11 (Bullseye) has VERSION_ID="11"
            if [ -n "$VERSION_ID" ] && [ "$VERSION_ID" -lt 11 ]; then
                print_message "Error: Debian/Raspberry Pi OS version too old. Version 11 (Bullseye) or newer required" "$RED"
                print_message "Current version: Debian/Raspberry Pi OS $VERSION_ID" "$RED"
                exit 1
            fi
            ;;
        ubuntu)
            # Ubuntu 20.04 has VERSION_ID="20.04"
            ubuntu_version=$(echo "$VERSION_ID" | awk -F. '{print $1$2}')
            if [ "$ubuntu_version" -lt 2004 ]; then
                print_message "Error: Ubuntu version too old. Version 20.04 or newer required" "$RED"
                print_message "Current version: Ubuntu $VERSION_ID" "$RED"
                exit 1
            fi
            ;;
        *)
            print_message "Error: Unsupported Linux distribution. Please use Debian 11+, Ubuntu 20.04+, or Raspberry Pi OS (Bullseye+)" "$RED"
            print_message "Current distribution: $PRETTY_NAME" "$RED"
            exit 1
            ;;
    esac

    print_message "System prerequisites check passed: $PRETTY_NAME" "$GREEN"
}

# Function to check if directories can be created
check_directory() {
    local dir="$1"
    if [ ! -d "$dir" ]; then
        if ! mkdir -p "$dir" 2>/dev/null; then
            print_message "Error: Cannot create directory $dir" "$RED"
            exit 1
        fi
    elif [ ! -w "$dir" ]; then
        print_message "Error: Cannot write to directory $dir" "$RED"
        exit 1
    fi
}

# Function to test RTSP URL
test_rtsp_url() {
    local url=$1
    if command_exists ffprobe; then
        print_message "Testing RTSP connection..." "$YELLOW"
        if ffprobe -v quiet -i "$url" -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 2>/dev/null; then
            return 0
        fi
    fi
    return 1
}

# Function to configure locale
configure_locale() {
    print_message "\nLocale Configuration" "$GREEN"
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
        print_message "\nSelect your language (1-${#locale_codes[@]}):" "$YELLOW"
        read -r selection
        
        if [[ "$selection" =~ ^[0-9]+$ ]] && [ "$selection" -ge 1 ] && [ "$selection" -le "${#locale_codes[@]}" ]; then
            LOCALE_CODE="${locale_codes[$((selection-1))]}"
            print_message "Selected language: ${locale_names[$((selection-1))]}" "$GREEN"
            # Update config file
            sed -i "s/locale: en/locale: ${LOCALE_CODE}/" "$CONFIG_FILE"
            break
        else
            print_message "Invalid selection. Please try again." "$RED"
        fi
    done
}

# Function to configure location
configure_location() {
    print_message "\nLocation Configuration" "$GREEN"
    print_message "1) Enter coordinates manually" "$YELLOW"
    print_message "2) Enter city name" "$YELLOW"
    read -p "Select location input method (1/2): " location_choice

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
                    print_message "Invalid coordinates. Please try again." "$RED"
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
                    print_message "Found coordinates: $lat, $lon" "$GREEN"
                    break
                else
                    print_message "Could not find coordinates for the specified city. Please try again." "$RED"
                fi
            done
            ;;
        *)
            print_message "Invalid choice. Exiting." "$RED"
            exit 1
            ;;
    esac

    # Update config file
    sed -i "s/latitude: 00.000/latitude: $lat/" "$CONFIG_FILE"
    sed -i "s/longitude: 00.000/longitude: $lon/" "$CONFIG_FILE"
}

# Function to configure audio input
configure_audio_input() {
    print_message "\nAudio Input Configuration" "$GREEN"
    print_message "1) Use sound card" "$YELLOW"
    print_message "2) Use RTSP stream" "$YELLOW"
    read -p "Select audio input method (1/2): " audio_choice

    case $audio_choice in
        1)
            configure_sound_card
            ;;
        2)
            configure_rtsp_stream
            ;;
        *)
            print_message "Invalid choice. Exiting." "$RED"
            exit 1
            ;;
    esac
}

# Function to configure sound card
configure_sound_card() {
    print_message "\nDetected audio devices:" "$GREEN"
    
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
        print_message "No audio capture devices found!" "$RED"
        exit 1
    fi

    while true; do
        print_message "\nPlease select a device number from the list above (1-${#devices[@]}):" "$YELLOW"
        read -r selection

        if [[ "$selection" =~ ^[0-9]+$ ]] && [ "$selection" -ge 1 ] && [ "$selection" -le "${#devices[@]}" ]; then
            ALSA_CARD="${devices[$((selection-1))]}"
            print_message "Selected device: $ALSA_CARD" "$GREEN"
            break
        else
            print_message "Invalid selection. Please try again." "$RED"
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
        print_message "\nEnter RTSP URL (format: rtsp://user:password@address/path):" "$YELLOW"
        read -r RTSP_URL
        
        if [[ ! $RTSP_URL =~ ^rtsp:// ]]; then
            print_message "Invalid RTSP URL format. Please try again." "$RED"
            continue
        fi
        
        if test_rtsp_url "$RTSP_URL"; then
            print_message "RTSP connection successful!" "$GREEN"
            break
        else
            print_message "Could not connect to RTSP stream. Do you want to try again? (y/n)" "$RED"
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
    print_message "\nAudio Export Configuration" "$GREEN"
    print_message "Select audio format for captured sounds:" "$YELLOW"
    print_message "1) WAV (Uncompressed, largest files)" "$YELLOW"
    print_message "2) FLAC (Lossless compression)" "$YELLOW"
    print_message "3) AAC (High quality, smaller files)" "$YELLOW"
    print_message "4) MP3 (Most compatible)" "$YELLOW"
    print_message "5) Opus (Best compression)" "$YELLOW"
    
    while true; do
        read -p "Select format (1-5): " format_choice
        case $format_choice in
            1) format="wav"; break;;
            2) format="flac"; break;;
            3) format="aac"; break;;
            4) format="mp3"; break;;
            5) format="opus"; break;;
            *) print_message "Invalid choice. Please try again." "$RED";;
        esac
    done

    # Update config file
    sed -i "s/type: wav/type: $format/" "$CONFIG_FILE"
}

# Function to configure basic authentication
configure_auth() {
    print_message "\nSecurity Configuration" "$GREEN"
    print_message "Do you want to enable password protection for the settings interface?" "$YELLOW"
    print_message "This is recommended if BirdNET-Go will be accessible from the internet." "$YELLOW"
    read -p "Enable password protection? (y/n): " enable_auth

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
                
                print_message "\nPassword protection enabled successfully!" "$GREEN"
                print_message "If you forget your password, you can reset it by editing:" "$YELLOW"
                print_message "$CONFIG_FILE" "$YELLOW"
                break
            else
                print_message "Passwords don't match. Please try again." "$RED"
            fi
        done
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

print_message "\nBirdNET-Go Installation Script" "$GREEN"
print_message "This script will install BirdNET-Go and its dependencies." "$YELLOW"
print_message "Note: Root privileges will be required for:" "$YELLOW"
print_message "  - Installing system packages (alsa-utils, curl, ffmpeg)" "$YELLOW"
print_message "  - Installing Docker" "$YELLOW"
print_message "  - Creating systemd service\n" "$YELLOW"

# Default paths
CONFIG_DIR="$HOME/birdnet-go-app/config"
DATA_DIR="$HOME/birdnet-go-app/data"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
TEMP_CONFIG="/tmp/config.yaml"

# Check if script is run as root
if [ "$EUID" -eq 0 ]; then
    print_message "Please do not run this script as root or with sudo" "$RED"
    exit 1
fi

# Check prerequisites before proceeding
check_prerequisites

# Update package list
print_message "Updating package list..." "$YELLOW"
sudo apt-get update

# Install required packages
print_message "Checking and installing required packages..." "$YELLOW"
check_install_package "alsa-utils"
check_install_package "curl"
check_install_package "ffmpeg"
check_install_package "bc"
check_install_package "jq"
check_install_package "apache2-utils"

# Check and install Docker
if ! command_exists docker; then
    print_message "Docker not found. Installing Docker..." "$YELLOW"
    # Install Docker using convenience script
    curl -fsSL https://get.docker.com | sh
    # Add current user to docker group
    sudo usermod -aG docker "$USER"
    print_message "Docker installed successfully. You may need to log out and back in for group changes to take effect." "$GREEN"
fi

# Check if directories can be created
check_directory "$CONFIG_DIR"
check_directory "$DATA_DIR"

# Create directories
print_message "Creating directories..." "$YELLOW"
mkdir -p "$CONFIG_DIR"
mkdir -p "$DATA_DIR"

# Download original config file
print_message "Downloading configuration template..." "$YELLOW"
curl -s https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml > "$CONFIG_FILE"

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

# Get timezone
if [ -f /etc/timezone ]; then
    TZ=$(cat /etc/timezone)
else
    TZ="UTC"
fi

# Pause for 5 seconds
sleep 5

# Create systemd service
print_message "\nCreating systemd service..." "$YELLOW"
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

print_message "\nInstallation completed!" "$GREEN"
print_message "Configuration directory: $CONFIG_DIR" "$GREEN"
print_message "Data directory: $DATA_DIR" "$GREEN"
print_message "\nTo start BirdNET-Go, run: sudo systemctl start birdnet-go" "$YELLOW"
print_message "To check status, run: sudo systemctl status birdnet-go" "$YELLOW"
print_message "The web interface will be available at http://localhost:8080" "$YELLOW"

if ! groups "$USER" | grep -q docker; then
    print_message "\nIMPORTANT: Please log out and log back in for Docker permissions to take effect." "$RED"
fi
