#!/bin/bash
set -e

# Default to UID/GID 1000 if not set
APP_UID=${BIRDNET_UID:-1000}
APP_GID=${BIRDNET_GID:-1000}
APP_USER="birdnet"

# SKIP_CHOWN: set to "true" to skip all ownership changes (useful for NFS mounts)
SKIP_CHOWN="$(echo "${SKIP_CHOWN:-false}" | tr '[:upper:]' '[:lower:]')"

echo "Starting BirdNET-Go with UID:$APP_UID, GID:$APP_GID"

# Check ownership of a path and chown only if it differs from expected UID:GID.
# Usage: check_and_chown [-R] <path>
check_and_chown() {
    local recursive=false
    if [ "$1" = "-R" ]; then
        recursive=true
        shift
    fi
    local target="$1"

    # Skip if path doesn't exist (also handle dangling symlinks for chown -h)
    [ -e "$target" ] || [ -L "$target" ] || return 0

    if [ "$recursive" = true ]; then
        # Chown only files/dirs with mismatched ownership, avoiding redundant operations
        find "$target" -not \( -uid "$APP_UID" -a -gid "$APP_GID" \) -exec chown -h "$APP_UID":"$APP_GID" -- {} +
    else
        local owner
        owner=$(stat -c "%u:%g" -- "$target" 2>/dev/null) || return 0
        if [ "$owner" != "$APP_UID:$APP_GID" ]; then
            chown -h "$APP_UID":"$APP_GID" -- "$target"
        fi
    fi
}

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
if [ "$SKIP_CHOWN" = "true" ]; then
    echo "SKIP_CHOWN is set, skipping ownership changes for /config and /data"
else
    check_and_chown -R /config
    check_and_chown /data
    # Chown items in /data one level deep, only those with mismatched ownership
    find /data -mindepth 1 -maxdepth 1 -not \( -uid "$APP_UID" -a -gid "$APP_GID" \) -exec chown -h "$APP_UID":"$APP_GID" -- {} +
fi

# Set read permissions for model files
chmod -R a+r /data/models/*.tflite 2>/dev/null || true
# Ensure directory is executable (browsable)
chmod a+x /data/models

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

# Only chown clips directory if ownership differs
if [ "$SKIP_CHOWN" != "true" ]; then
    check_and_chown -R /data/clips
fi

# Create config directory and symlink for the user
USER_HOME=$(getent passwd "$APP_UID" | cut -d: -f6)
mkdir -p "$USER_HOME/.config"
if [ "$SKIP_CHOWN" != "true" ]; then
    check_and_chown "$USER_HOME/.config"
fi
if [ ! -L "$USER_HOME/.config/birdnet-go" ]; then
    # For Podman rootless, create symlink without gosu
    su - "$USER_NAME" -c "ln -sf /config ~/.config/birdnet-go"
fi

# If audio device present, ensure permissions are correct
if [ -d "/dev/snd" ]; then
    # Add user to audio group
    if getent group audio >/dev/null; then
        adduser "$USER_NAME" audio || true
    fi
    # Make device accessible
    chmod -R a+rw /dev/snd || true
fi

echo "Starting BirdNET-Go as user $USER_NAME ($APP_UID:$APP_GID)"
# Direct execution for Podman rootless (no gosu needed)
exec "$@"