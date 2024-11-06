#!/bin/bash
set -eo pipefail
IFS=$'\n\t'

# Text styling
BOLD='\033[1m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

# Standard config locations
CONFIG_PATHS=(
    "$1"  # Command line parameter takes precedence
    "./config.yaml"
    "$HOME/.config/birdnet-go/config.yaml"
    "/etc/birdnet-go/config.yaml"
)

echo -e "${BOLD}BirdNET-Go Authentication Reset Tool${NC}\n"

if [ "$1" ]; then
    echo -e "${BLUE}Using provided config path:${NC} $1"
fi

for CONFIG_PATH in "${CONFIG_PATHS[@]}"; do
    [ -z "$CONFIG_PATH" ] && continue  # Skip empty paths
    
    if [ -f "$CONFIG_PATH" ]; then
        # Check write permissions
        if [ ! -w "$CONFIG_PATH" ]; then
            echo -e "${BOLD}Error:${NC} No write permission for $CONFIG_PATH"
            continue
        fi

        echo -e "${BLUE}Found config at:${NC} $CONFIG_PATH"
        
        # Create timestamped backup
        BACKUP="${CONFIG_PATH}.$(date +%Y%m%d_%H%M%S).bak"
        while [ -f "$BACKUP" ]; do
            BACKUP="${CONFIG_PATH}.$(date +%Y%m%d_%H%M%S)_$RANDOM.bak"
        done
        cp "$CONFIG_PATH" "$BACKUP"        
        # Reset auth settings
        sed -i '
            /^security:/,/^[^[:space:]]/ {
                s/\(host:\).*/\1 ""/
                s/\(autotls:\).*/\1 false/
                s/\(redirecttohttps:\).*/\1 false/
            }
            /^[[:space:]]*basicauth:/,/^[[:space:]]*[^[:space:]]/ {
                s/\(enabled:\).*/\1 false/
            }
            /^[[:space:]]*googleauth:/,/^[[:space:]]*[^[:space:]]/ {
                s/\(enabled:\).*/\1 false/
            }
            /^[[:space:]]*githubauth:/,/^[[:space:]]*[^[:space:]]/ {
                s/\(enabled:\).*/\1 false/
            }
        ' "$CONFIG_PATH"
        
        echo -e "\n${GREEN}✓ Authentication settings reset successfully${NC}"
        echo -e "${BLUE}Backup saved as:${NC} $BACKUP"
        exit 0
    fi
done

echo -e "\n${BOLD}No config file found in standard locations${NC}"
echo -e "Usage: $0 [path/to/config.yaml]"
exit 1
