#!/bin/bash
set -euxo pipefail

sudo apt-get update

# Install required runtime dependencies
sudo apt-get install -y ca-certificates libasound2 ffmpeg sox alsa-utils

# Install file editors
sudo apt-get install -y nano vim

# Install extras
sudo apt-get install -y dialog

# Install air to support live reloading of server on code changes
go install github.com/air-verse/air@latest
