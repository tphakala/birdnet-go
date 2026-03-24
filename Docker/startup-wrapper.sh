#!/bin/bash

# Startup wrapper to catch and display application errors prominently
# This ensures critical startup errors are visible in container logs

set -o pipefail

# Store command as array to preserve argument boundaries
APP_CMD=("$@")
STARTUP_LOG="/tmp/birdnet-startup.log"
APP_PID=""

# Cleanup function
cleanup() {
    # Give tee a moment to finish writing
    sleep 1
}

# Signal handler to forward signals to child process
forward_signal() {
    local sig=$1
    if [ -n "$APP_PID" ] && kill -0 "$APP_PID" 2>/dev/null; then
        kill -"$sig" "$APP_PID" 2>/dev/null || true
    fi
}

# Trap signals and forward them
trap 'forward_signal TERM' TERM
trap 'forward_signal INT' INT
trap 'cleanup' EXIT

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🚀 Starting BirdNET-Go Application"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Command: ${APP_CMD[*]}"
echo "Time: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo ""

# Start application with output going to tee via process substitution
# This allows us to capture the real application PID for signal forwarding
"${APP_CMD[@]}" > >(tee "$STARTUP_LOG") 2>&1 &
APP_PID=$!

# Wait for the application to finish and capture its exit code.
# When a signal (SIGTERM/SIGINT) interrupts wait, bash returns immediately
# with 128+signal. Re-wait to collect the child's actual exit code.
wait "$APP_PID" 2>/dev/null
EXIT_CODE=$?
if [ $EXIT_CODE -ge 128 ] && kill -0 "$APP_PID" 2>/dev/null; then
    # wait was interrupted by a signal and the child is still running;
    # re-wait to collect the child's actual exit code.
    # Without the kill -0 guard, re-waiting a process that already exited
    # (e.g., SIGSEGV crash) would overwrite the real exit code with 127.
    wait "$APP_PID" 2>/dev/null
    EXIT_CODE=$?
fi

# Exit codes 143 (SIGTERM) and 130 (SIGINT) indicate the process was
# terminated by a signal we forwarded — this is a clean shutdown, not a failure.
if [ $EXIT_CODE -eq 143 ] || [ $EXIT_CODE -eq 130 ]; then
    exit $EXIT_CODE
fi

# Check if the application failed
if [ $EXIT_CODE -ne 0 ]; then
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "❌ APPLICATION STARTUP FAILED (Exit Code: $EXIT_CODE)"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # Check for specific error patterns (case-insensitive)
    if grep -qi "insufficient disk space" "$STARTUP_LOG"; then
        echo "⚠️  DISK SPACE ERROR DETECTED"
        echo ""
        grep -i -A 5 "insufficient disk space" "$STARTUP_LOG" || true
        echo ""
        echo "💡 Quick fixes:"
        echo "  • Check available space: df -h"
        echo "  • Clean old clips: rm -rf /data/clips/*"
        echo "  • Increase volume size"
    elif grep -qiE "permission denied|cannot write|config directory not writable" "$STARTUP_LOG"; then
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
