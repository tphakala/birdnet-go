#!/bin/bash
# Specific debug collection for issue #811
# This script collects targeted data for the memory/CPU issues reported

set -euo pipefail

echo "BirdNET-Go Debug Collection for Issue #811"
echo "=========================================="
echo ""
echo "This script will collect debug data specifically for investigating:"
echo "- High memory usage with new telemetry features"
echo "- Performance impact of sound level monitoring"
echo "- Cache improvements effectiveness"
echo "- FFmpeg watchdog resource usage"
echo ""

# Run the main collection script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"${SCRIPT_DIR}/collect-debug-data.sh"

# Find the latest debug directory
DEBUG_DIR=$(ls -td debug-data-* | head -1)

echo ""
echo "Running specific analysis for issue #811..."
echo ""

# Create issue-specific report
REPORT_FILE="${DEBUG_DIR}/issue-811-analysis.md"

cat > "${REPORT_FILE}" << 'EOF'
# BirdNET-Go Issue #811 Analysis Report

## Background

This report analyzes performance data related to issue #811, which reports high memory usage
after implementing:
- Sentry telemetry
- Sound level monitoring
- Improved caching
- Enhanced error handling
- FFmpeg watchdog

## Memory Analysis Focus Areas

### 1. Sound Level Monitoring (myaudio/soundlevel.go)
Look for allocations from:
- `newSoundLevelProcessor`
- `ProcessAudioData`
- Octave band filter allocations

### 2. Image Cache (imageprovider/)
Check for:
- Cache size growth
- Uncleaned cache entries
- Memory held by image data

### 3. Telemetry (telemetry/)
Monitor:
- Sentry client memory usage
- Event queue size
- Attachment accumulation

### 4. FFmpeg Processes
Verify:
- Process cleanup working correctly
- No zombie processes
- Stream buffer management

## Specific Checks

Run these commands for targeted analysis:

```bash
# Check sound level processor allocations
go tool pprof -top -focus=soundlevel heap.pprof

# Check image cache memory
go tool pprof -top -focus=imageprovider heap.pprof

# Check telemetry overhead
go tool pprof -top -focus=telemetry heap.pprof

# Look for FFmpeg related memory
go tool pprof -list='.*ffmpeg.*' heap.pprof

# Check for growing goroutines
go tool pprof -text goroutine.pprof | grep -E "(soundlevel|telemetry|ffmpeg)"
```

## Expected vs Actual

Based on the implementation:
- Sound level monitoring should use <50MB (30 bands × buffers)
- Image cache should be bounded by configured limits
- Telemetry should have minimal overhead (<10MB)
- FFmpeg streams should clean up when inactive

Compare actual usage against these expectations.
EOF

echo "Issue-specific analysis guide created: ${REPORT_FILE}"
echo ""

# Run Python analyzer if available
if command -v python3 &> /dev/null; then
    echo "Running automated analysis..."
    python3 "${SCRIPT_DIR}/analyze-debug-data.py" "${DEBUG_DIR}"
else
    echo "Python 3 not found. Skipping automated analysis."
    echo "Install Python 3 to get comprehensive analysis reports."
fi

echo ""
echo "=========================================="
echo "Collection complete for issue #811!"
echo "=========================================="
echo ""
echo "Next steps:"
echo "1. Review: ${DEBUG_DIR}/analysis-report.md"
echo "2. Check: ${DEBUG_DIR}/issue-811-analysis.md"
echo "3. Run interactive analysis:"
echo "   go tool pprof -http=:8081 ${DEBUG_DIR}/heap.pprof"
echo ""
echo "To share with developers:"
echo "   Upload: birdnet-go-debug-*.tar.gz"
echo "   Include: Description of when the issue occurs"
echo "   Mention: Issue #811 in your report"
echo ""