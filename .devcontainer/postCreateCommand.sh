#!/bin/bash

# Install required apt dependencies
apt-get update
apt-get install -y ca-certificates libasound2 ffmpeg sox
apt-get install -y nano vim
apt-get clean

# Install air to support live reloading of server on code changes
go install github.com/air-verse/air@latest

# Install golangci-lint to allow running of linting locally
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v1.57.2
