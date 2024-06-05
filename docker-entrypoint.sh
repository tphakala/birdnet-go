#!/bin/bash
set -euo pipefail

# Inspired from https://github.com/docker-library/postgres/blob/master/17/bookworm/docker-entrypoint.sh
# and https://github.com/redis/docker-library-redis/blob/master/docker-entrypoint.sh

# Downgrade user if running as root, otherwise run as current user, allowing `--user` argument to work
if [ "$(id -u)" = '0' ]; then
    # Allow birdnet-user to write to the mounted volumes
    find /data /config \! -user birdnet-user -exec chown birdnet-user:birdnet-user '{}' +

    exec gosu birdnet-user "$BASH_SOURCE" "$@"
fi

echo "🐋 Starting BirdNET-Go inside container as $(id)..."

# Check if the current user is in the audio group
if ! id | grep -q '\baudio\b'; then
    echo "Current user is not in the audio group. If running container with `--user`, ensure the user is in the audio group by passing `--group-add audio` to docker run"
fi

# Check read and write access for mounted volumes
directories=("/data" "/config")
if [[ -n "$directories" ]]; then
    for directory in "${directories[@]}"; do
        if ! test -r "$directory"; then
            echo "birdnet-user ($(id)) does not have read access to $directory, $(stat -c "currently owned by uid=%u gid=%g with permissions %a/%A" $directory)"
            exit 1
        fi
        if ! test -w "$directory"; then
            echo "birdnet-user ($(id)) does not have write access to $directory, $(stat -c "currently owned by uid=%u gid=%g with permissions %a/%A" $directory)"
            exit 1
        fi
    done
fi

exec /usr/bin/birdnet-go $@
