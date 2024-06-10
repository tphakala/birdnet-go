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

# Install golangci-lint to allow running of linting locally
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v1.57.2
