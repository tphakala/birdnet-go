# RTSP Stream Telemetry Integration

This document describes the Sentry telemetry integration added to the RTSP streaming functionality in BirdNET-Go's myaudio package. This telemetry helps diagnose RTSP connection issues, stream failures, and reconnection problems.

## Overview

The RTSP telemetry integration captures critical events and errors during RTSP stream processing, including:

- Connection attempts and failures
- FFmpeg process lifecycle events
- Stream reconnection attempts and patterns
- Buffer errors and performance issues
- Process cleanup and termination events

## Telemetry Events Captured

### 1. Connection Events

#### `rtsp-connection-start`
- **Level**: Info
- **Trigger**: When an RTSP capture session begins
- **Data**: RTSP URL and transport protocol
- **Purpose**: Track connection attempts and configurations

#### `rtsp-ffmpeg-unavailable`
- **Level**: Error
- **Trigger**: When FFmpeg binary is not available
- **Data**: RTSP URL and error details
- **Purpose**: Identify FFmpeg installation/path issues

### 2. FFmpeg Process Management

#### `ffmpeg-path-validation`
- **Level**: Error
- **Trigger**: FFmpeg path validation fails
- **Data**: RTSP URL and validation error
- **Purpose**: Track FFmpeg binary access issues

#### `ffmpeg-pipe-creation`
- **Level**: Error
- **Trigger**: Stdout pipe creation fails
- **Data**: RTSP URL and pipe error
- **Purpose**: Identify process communication setup failures

#### `ffmpeg-process-start`
- **Level**: Error
- **Trigger**: FFmpeg process startup fails
- **Data**: RTSP URL and startup error
- **Purpose**: Track process launching issues

#### `ffmpeg-process-started`
- **Level**: Info
- **Trigger**: FFmpeg process starts successfully
- **Data**: RTSP URL and process PID
- **Purpose**: Confirm successful process creation

### 3. Stream Reconnection Logic

#### `ffmpeg-retry-attempt`
- **Level**: Warning
- **Trigger**: FFmpeg startup retry with backoff delay
- **Data**: RTSP URL and delay duration
- **Purpose**: Track reconnection patterns and backoff behavior

#### `ffmpeg-backoff-exhausted`
- **Level**: Error
- **Trigger**: Maximum retry attempts reached
- **Data**: RTSP URL and final error
- **Purpose**: Identify persistent connection failures

#### `ffmpeg-connection-success`
- **Level**: Info
- **Trigger**: Connection established after retries
- **Data**: RTSP URL
- **Purpose**: Track successful recovery from failures

### 4. Stream Health Monitoring

#### `rtsp-watchdog-timeout`
- **Level**: Warning
- **Trigger**: Watchdog detects no audio data
- **Data**: RTSP URL
- **Purpose**: Track stream health and data flow issues

#### `rtsp-analysis-buffer-error`
- **Level**: Error
- **Trigger**: Error writing to analysis buffer
- **Data**: RTSP URL and buffer error
- **Purpose**: Track audio processing pipeline issues

#### `rtsp-capture-buffer-error`
- **Level**: Error
- **Trigger**: Error writing to capture buffer
- **Data**: RTSP URL and buffer error
- **Purpose**: Track audio storage pipeline issues

#### `rtsp-buffer-error-threshold`
- **Level**: Error
- **Trigger**: Too many buffer errors trigger restart
- **Data**: RTSP URL and error count
- **Purpose**: Track severe buffer issues requiring intervention

### 5. Process Cleanup and Termination

#### `ffmpeg-cleanup-start`
- **Level**: Info
- **Trigger**: FFmpeg cleanup process begins
- **Data**: RTSP URL
- **Purpose**: Track cleanup operations

#### `ffmpeg-cleanup-normal`
- **Level**: Info
- **Trigger**: FFmpeg process terminates normally
- **Data**: RTSP URL
- **Purpose**: Track clean shutdown events

#### `ffmpeg-cleanup-timeout`
- **Level**: Warning
- **Trigger**: FFmpeg process doesn't terminate within timeout
- **Data**: RTSP URL
- **Purpose**: Track forced termination scenarios

#### `ffmpeg-kill-failure`
- **Level**: Error
- **Trigger**: Unable to terminate FFmpeg process
- **Data**: RTSP URL and termination error
- **Purpose**: Track process management failures

### 6. Process Monitoring

#### `ffmpeg-orphaned-cleanup`
- **Level**: Info
- **Trigger**: Monitor cleans up orphaned FFmpeg process
- **Data**: RTSP URL
- **Purpose**: Track process lifecycle management

#### `ffmpeg-interface-error`
- **Level**: Warning
- **Trigger**: Process doesn't implement expected interface
- **Data**: RTSP URL
- **Purpose**: Track internal consistency issues

#### `rtsp-lifecycle-error`
- **Level**: Error
- **Trigger**: RTSP lifecycle manager exits with error
- **Data**: RTSP URL and error details
- **Purpose**: Track high-level stream management failures

## Privacy Compliance

All telemetry respects the user's opt-in setting configured in `sentry.enabled`. The implementation:

- Only captures essential technical information
- Does not include sensitive data or audio content
- Filters out personal information from RTSP URLs
- Provides clear component tagging for error categorization

## Usage for Debugging

### Common RTSP Issues Tracked

1. **Connection Failures**: Look for `rtsp-connection-start` followed by `ffmpeg-process-start` errors
2. **Reconnection Loops**: Monitor `ffmpeg-retry-attempt` frequency and patterns
3. **Stream Interruptions**: Check for `rtsp-watchdog-timeout` events
4. **Buffer Issues**: Track `rtsp-buffer-error-threshold` occurrences
5. **Process Management**: Monitor cleanup and termination events

### Telemetry Analysis

- **Error Rate**: Track frequency of connection and process failures
- **Recovery Time**: Measure time between failures and successful reconnections
- **Failure Patterns**: Identify if issues are URL-specific or systemic
- **Resource Usage**: Monitor cleanup and termination success rates

## Configuration

No additional configuration is required beyond enabling Sentry in the main application settings:

```yaml
sentry:
  enabled: true
  dsn: "YOUR_SENTRY_DSN"
  samplerate: 1.0
  debug: false
```

## Implementation Files

The RTSP telemetry integration is implemented in:

- `internal/myaudio/ffmpeg_input.go` - Main RTSP capture and process management
- `internal/myaudio/ffmpeg_monitor.go` - Process monitoring and cleanup

These files now include comprehensive telemetry collection for all critical RTSP streaming events while maintaining privacy compliance and respecting user opt-in preferences.