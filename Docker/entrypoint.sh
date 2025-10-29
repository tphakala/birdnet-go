#!/bin/bash
set -e

# Default to UID/GID 1000 if not set
APP_UID=${BIRDNET_UID:-1000}
APP_GID=${BIRDNET_GID:-1000}
APP_USER="birdnet"

echo "Starting BirdNET-Go with UID:$APP_UID, GID:$APP_GID"

# Detect if we're running as root or in rootless mode
CURRENT_UID=$(id -u)
RUNNING_AS_ROOT=false
if [ "$CURRENT_UID" -eq 0 ]; then
    RUNNING_AS_ROOT=true
fi

# Only perform privileged operations if running as root
if [ "$RUNNING_AS_ROOT" = true ]; then
    # Check if group with specified GID exists
    if ! getent group "$APP_GID" >/dev/null; then
        echo "Creating group $APP_USER with GID $APP_GID"
        addgroup --gid "$APP_GID" "$APP_USER" || { echo "Failed to create group"; exit 1; }
    fi

    # Get group name for this GID
    GROUP_NAME=$(getent group "$APP_GID" | cut -d: -f1)
    export GROUP_NAME

    # Check if user with specified UID exists
    if ! getent passwd "$APP_UID" >/dev/null; then
        echo "Creating user $APP_USER with UID $APP_UID"
        adduser --uid "$APP_UID" --gid "$APP_GID" --disabled-password --gecos "" --home "/home/$APP_USER" --shell /bin/bash "$APP_USER" || { echo "Failed to create user"; exit 1; }
    fi

    # Get username for this UID
    USER_NAME=$(getent passwd "$APP_UID" | cut -d: -f1)

    # Ensure /config and /data are accessible to the user
    mkdir -p /config /data/clips /data/models
    chown -R "$APP_UID":"$APP_GID" /config
    chown "$APP_UID":"$APP_GID" /data
    chown "$APP_UID":"$APP_GID" /data/*
else
    # Running in rootless mode (already running as target user)
    echo "Running in rootless mode (current UID: $CURRENT_UID)"

    # Just ensure directories exist (permissions already set in Dockerfile)
    mkdir -p /config /data/clips /data/models 2>/dev/null || true

    # Use current user
    USER_NAME=$(whoami)
    echo "Running as user: $USER_NAME"
fi

# Set read permissions for model files (only when running as root)
if [ "$RUNNING_AS_ROOT" = true ]; then
    chmod -R a+r /data/models/*.tflite 2>/dev/null || true
    # Ensure directory is executable (browsable)
    chmod a+x /data/models 2>/dev/null || true
fi

# Check if user has custom model path configured via environment variable
if [ ! -z "$BIRDNET_MODELPATH" ]; then
    echo "Custom model path configured: $BIRDNET_MODELPATH"
    # Expand environment variables in the path using shell expansion
    EXPANDED_PATH=$(eval echo "$BIRDNET_MODELPATH")
    if [ -f "$EXPANDED_PATH" ]; then
        echo "Custom model file found at: $EXPANDED_PATH"
    else
        echo "Warning: Custom model file not found at: $EXPANDED_PATH"
    fi
fi

# Only chown clips directory if running as root and any subdirectories have wrong ownership
if [ "$RUNNING_AS_ROOT" = true ]; then
    NEEDS_CHOWN=false
    if [ -d "/data/clips" ] && [ "$(ls -A /data/clips)" ]; then
        for subdir in /data/clips/*/; do
            if [ -d "$subdir" ]; then
                CURRENT_UID=$(stat -c %u "$subdir" 2>/dev/null || echo "0")
                CURRENT_GID=$(stat -c %g "$subdir" 2>/dev/null || echo "0")
                if [ "$CURRENT_UID" != "$APP_UID" ] || [ "$CURRENT_GID" != "$APP_GID" ]; then
                    NEEDS_CHOWN=true
                    break
                fi
            fi
        done
    fi

    if [ "$NEEDS_CHOWN" = true ]; then
        echo "Fixing ownership of clips directory..."
        chown -R "$APP_UID":"$APP_GID" /data/clips
    fi

    # Create config directory and symlink for the user
    USER_HOME=$(getent passwd "$APP_UID" | cut -d: -f6)
    mkdir -p "$USER_HOME/.config"
    chown "$APP_UID":"$APP_GID" "$USER_HOME/.config"
    if [ ! -L "$USER_HOME/.config/birdnet-go" ]; then
        gosu "$USER_NAME" ln -sf /config "$USER_HOME/.config/birdnet-go"
    fi
else
    # In rootless mode, create symlink in current user's home if it exists
    if [ -d "$HOME" ]; then
        mkdir -p "$HOME/.config" 2>/dev/null || true
        if [ ! -L "$HOME/.config/birdnet-go" ]; then
            ln -sf /config "$HOME/.config/birdnet-go" 2>/dev/null || true
        fi
    fi
fi

# Configure timezone if TZ environment variable is set
if [ -n "$TZ" ]; then
    echo "Timezone configuration: TZ=$TZ"

    # Warn about legacy timezone formats
    if [[ "$TZ" == US/* ]] || [[ "$TZ" == Etc/* ]]; then
        echo "⚠️  WARNING: Using legacy timezone format '$TZ'"
        echo "    Consider canonical format (e.g., 'America/Denver' instead of 'US/Mountain')"
        echo "    Legacy names may be removed in future Debian releases"
    fi

    # Validate timezone exists in tzdata
    if [ -f "/usr/share/zoneinfo/$TZ" ]; then
        ln -snf "/usr/share/zoneinfo/$TZ" /etc/localtime
        echo "$TZ" > /etc/timezone
        echo "✓ Timezone configured: $TZ"
    else
        echo "❌ ERROR: Timezone '$TZ' not found"
        echo "   Available timezones: ls /usr/share/zoneinfo/"
        echo "   Install tzdata-legacy if using US/*, Etc/*, or other legacy names"
        echo "   Falling back to UTC"
        # Actually configure UTC as fallback
        ln -snf "/usr/share/zoneinfo/UTC" /etc/localtime
        echo "UTC" > /etc/timezone
    fi
else
    echo "No TZ environment variable set, using container default (UTC)"
fi

# If audio device present, ensure permissions are correct
if [ -d "/dev/snd" ]; then
    if [ "$RUNNING_AS_ROOT" = true ]; then
        # Add user to audio group
        if getent group audio >/dev/null; then
            adduser "$USER_NAME" audio || true
        fi
        # Make device accessible
        chmod -R a+rw /dev/snd || true
    fi
fi

# Pre-flight checks before starting application
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔍 Running pre-flight checks..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Check data directory disk space
DATA_SPACE_KB=$(df -k /data | awk 'NR==2 {print $4}')
DATA_SPACE_MB=$((DATA_SPACE_KB / 1024))
REQUIRED_SPACE_MB=1024

if [ "$DATA_SPACE_MB" -lt "$REQUIRED_SPACE_MB" ]; then
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "❌ STARTUP ERROR: Insufficient disk space"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Location:  /data"
    echo "Required:  ${REQUIRED_SPACE_MB}MB"
    echo "Available: ${DATA_SPACE_MB}MB"
    echo ""
    echo "💡 To resolve:"
    echo "  1. Increase volume size for /data mount"
    echo "  2. Free up space: docker exec birdnet-go rm -rf /data/clips/*"
    echo "  3. Check host mount: df -h"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "Container will exit in 30 seconds..."
    echo "Use 'journalctl -u birdnet-go.service -n 50' to view this message"
    sleep 30
    exit 1
fi

# Check config directory exists and is writable
if [ ! -w /config ]; then
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "❌ STARTUP ERROR: Config directory not writable"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Check permissions on host directory mounted to /config"
    echo "Container will exit in 10 seconds..."
    sleep 10
    exit 1
fi

echo "✅ Pre-flight checks passed"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Execute the application
if [ "$RUNNING_AS_ROOT" = true ]; then
    echo "Starting BirdNET-Go as user $USER_NAME ($APP_UID:$APP_GID)"
    exec gosu "$USER_NAME" "$@"
else
    echo "Starting BirdNET-Go as user $USER_NAME (rootless mode)"
    exec "$@"
fi