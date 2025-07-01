#!/bin/bash

# Script to run telemetry tests with race detector enabled

echo "Running telemetry tests with race detector..."
echo "============================================"

# Set test timeout
TEST_TIMEOUT="30s"

# Run tests with race detector
go test -race -v -timeout=$TEST_TIMEOUT ./internal/telemetry/...

# Capture exit code
EXIT_CODE=$?

if [ $EXIT_CODE -eq 0 ]; then
    echo ""
    echo "✅ All tests passed with race detector enabled!"
else
    echo ""
    echo "❌ Tests failed with race detector. Exit code: $EXIT_CODE"
fi

exit $EXIT_CODE