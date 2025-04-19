#!/bin/bash
set -e

# Default to UID/GID 1000 if not set
APP_UID=${BIRDNET_UID:-1000}
APP_GID=${BIRDNET_GID:-1000}
APP_USER="birdnet"

echo "Starting BirdNET-Go with UID:$APP_UID, GID:$APP_GID"

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
mkdir -p /config /data/clips
chown -R "$APP_UID":"$APP_GID" /config
chown "$APP_UID":"$APP_GID" /data
chown "$APP_UID":"$APP_GID" /data/*

# Chown clips directory at background to avoid blocking the main process
chown -R "$APP_UID":"$APP_GID" /data/clips &

# Create config directory and symlink for the user
USER_HOME=$(getent passwd "$APP_UID" | cut -d: -f6)
mkdir -p "$USER_HOME/.config"
chown "$APP_UID":"$APP_GID" "$USER_HOME/.config"
if [ ! -L "$USER_HOME/.config/birdnet-go" ]; then
    gosu "$USER_NAME" ln -sf /config "$USER_HOME/.config/birdnet-go"
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
exec gosu "$USER_NAME" "$@"