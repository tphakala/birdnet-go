#!/bin/bash
set -e

# Default to UID/GID 1000 if not set (common default user id)
APP_UID=${BIRDNET_UID:-1000}
APP_GID=${BIRDNET_GID:-1000}
APP_USER="birdnet"

echo "Starting BirdNET-Go with UID:$APP_UID, GID:$APP_GID"

# Create group if it doesn't exist
if ! getent group | awk -F: '$3 == '"$APP_GID"'' > /dev/null; then
    groupadd -g "$APP_GID" "$APP_USER"
fi

# Get group name for this GID
GROUP_NAME=$(getent group | awk -F: '$3 == '"$APP_GID"' {print $1}')
export GROUP_NAME

# Create user if it doesn't exist
if ! getent passwd | awk -F: '$3 == '"$APP_UID"'' > /dev/null; then
    useradd -u "$APP_UID" -g "$APP_GID" -s /bin/bash -m -d /home/"$APP_USER" "$APP_USER"
fi

# Get username for this UID
USER_NAME=$(getent passwd | awk -F: '$3 == '"$APP_UID"' {print $1}')

# Ensure /config and /data are accessible to the user
# Create necessary symlinks for the application
mkdir -p /config /data
chown -R "$APP_UID":"$APP_GID" /config /data

# Create config directory and symlink for the user
USER_HOME=$(getent passwd | awk -F: '$3 == '"$APP_UID"' {print $6}')
mkdir -p "$USER_HOME/.config"
chown "$APP_UID":"$APP_GID" "$USER_HOME/.config"
if [ ! -L "$USER_HOME/.config/birdnet-go" ]; then
    gosu "$USER_NAME" ln -sf /config "$USER_HOME/.config/birdnet-go"
fi

# If audio device present, ensure permissions are correct
if [ -d "/dev/snd" ]; then
    # Add user to audio group
    if getent group audio > /dev/null 2>&1; then
        usermod -a -G audio "$USER_NAME"
    fi
    # Make device accessible
    chmod -R a+rw /dev/snd || true
fi

echo "Starting BirdNET-Go as user $USER_NAME ($APP_UID:$APP_GID)"
exec gosu "$USER_NAME" "$@"