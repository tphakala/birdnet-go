#!/bin/bash

# Startup wrapper to catch and display application errors prominently
# This ensures critical startup errors are visible in container logs

set -o pipefail

APP_CMD="$@"
STARTUP_LOG="/tmp/birdnet-startup.log"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🚀 Starting BirdNET-Go Application"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Command: $APP_CMD"
echo "Time: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo ""

# Run the application and capture output
if ! $APP_CMD 2>&1 | tee "$STARTUP_LOG"; then
    EXIT_CODE=$?

    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "❌ APPLICATION STARTUP FAILED (Exit Code: $EXIT_CODE)"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # Check for specific error patterns
    if grep -q "insufficient disk space" "$STARTUP_LOG"; then
        echo "⚠️  DISK SPACE ERROR DETECTED"
        echo ""
        grep -A 5 "insufficient disk space" "$STARTUP_LOG" || true
        echo ""
        echo "💡 Quick fixes:"
        echo "  • Check available space: df -h"
        echo "  • Clean old clips: rm -rf /data/clips/*"
        echo "  • Increase volume size"
    elif grep -q "permission denied\|cannot write" "$STARTUP_LOG"; then
        echo "⚠️  PERMISSION ERROR DETECTED"
        echo ""
        echo "💡 Check volume mount permissions:"
        echo "  • Host directories must be writable by UID:GID"
        echo "  • Set BIRDNET_UID/BIRDNET_GID environment variables"
    else
        echo "Last 20 lines of output:"
        tail -n 20 "$STARTUP_LOG"
    fi

    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "📋 View full logs:"
    echo "  docker logs birdnet-go"
    echo "  journalctl -u birdnet-go.service -n 100"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    exit $EXIT_CODE
fi
