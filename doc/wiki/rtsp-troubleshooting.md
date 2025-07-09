# RTSP Troubleshooting Guide

This guide covers common RTSP streaming issues and their solutions in BirdNET-Go, including advanced configuration options for problematic cameras.

## Table of Contents

- [Common RTSP Issues](#common-rtsp-issues)
- [Health Monitoring Configuration](#health-monitoring-configuration)
- [Advanced FFmpeg Parameters](#advanced-ffmpeg-parameters)
- [Camera-Specific Issues](#camera-specific-issues)
- [Troubleshooting Steps](#troubleshooting-steps)
- [Configuration Examples](#configuration-examples)

## Common RTSP Issues

### Stream Not Recovering After Camera Reboot

**Symptoms:**
- Stream starts normally and receives data
- After camera reboot, stream shows "unhealthy" but never recovers
- Restart count shows as 0 in logs despite multiple restart attempts
- FFmpeg process doesn't exit immediately when camera reboots

**Root Cause:**
Some cameras don't properly close TCP connections during reboots, leaving "half-open" TCP sessions. FFmpeg continues waiting on these dead connections instead of detecting the failure immediately.

**Solution:**
Use custom FFmpeg parameters to add connection timeouts and reconnection logic.

### High Restart Count with Circuit Breaker

**Symptoms:**
- Frequent stream restarts
- Circuit breaker opens after consecutive failures
- Streams get throttled or stopped completely

**Root Cause:**
Network issues, authentication problems, or incompatible camera settings causing repeated failures.

**Solution:**
Check network connectivity, verify RTSP credentials, and adjust health monitoring thresholds.

## Health Monitoring Configuration

BirdNET-Go includes configurable health monitoring for RTSP streams:

```yaml
realtime:
  rtsp:
    health:
      healthydatathreshold: 60  # seconds before stream considered unhealthy
      monitoringinterval: 30    # health check interval in seconds
```

### Health Settings Explained

- **healthydatathreshold**: How long (in seconds) without receiving data before a stream is considered unhealthy (default: 60)
- **monitoringinterval**: How often (in seconds) to check stream health (default: 30)

### Adjusting for Your Network

For unstable networks or slow cameras:
```yaml
realtime:
  rtsp:
    health:
      healthydatathreshold: 120  # Allow 2 minutes without data
      monitoringinterval: 60     # Check every minute
```

For fast networks requiring quick recovery:
```yaml
realtime:
  rtsp:
    health:
      healthydatathreshold: 30   # Only allow 30 seconds without data
      monitoringinterval: 15     # Check every 15 seconds
```

## Advanced FFmpeg Parameters

The `ffmpegparameters` setting allows you to add custom FFmpeg command-line arguments for problematic cameras.

### Basic Syntax

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-parameter1"
      - "value1"
      - "-parameter2"
      - "value2"
```

### Common Parameters for Connection Issues

#### Socket and I/O Timeouts
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-stimeout"
      - "5000000"      # 5 second socket timeout (in microseconds)
      - "-timeout"
      - "10000000"     # 10 second I/O timeout (in microseconds)
```

#### Reconnection Settings
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-reconnect"
      - "1"            # Enable automatic reconnection
      - "-reconnect_at_eof"
      - "1"            # Reconnect at end of file
      - "-reconnect_streamed"
      - "1"            # Reconnect for streamed inputs
```

#### TCP Socket Optimization
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-tcp_nodelay"
      - "1"            # Disable Nagle's algorithm for lower latency
      - "-rw_timeout"
      - "10000000"     # Read/write timeout (in microseconds)
```

#### Buffer and Probe Settings
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-buffer_size"
      - "32768"        # Set buffer size
      - "-probesize"
      - "32"           # Reduce probe size for faster detection
      - "-analyzeduration"
      - "1000000"      # Reduce analysis duration (in microseconds)
```

## Camera-Specific Issues

### Reolink Cameras
Generally work well with default settings. If issues occur, try:
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-rtsp_flags"
      - "prefer_tcp"
```

### Hikvision Cameras
May require authentication parameters:
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-rtsp_flags"
      - "prefer_tcp"
      - "-stimeout"
      - "5000000"
```

### Dahua Cameras
Often benefit from increased timeouts:
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-timeout"
      - "15000000"     # 15 second timeout
      - "-reconnect"
      - "1"
```

### Generic IP Cameras with Poor TCP Handling
For cameras that don't properly close connections:
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-stimeout"
      - "5000000"      # 5 second socket timeout
      - "-timeout"
      - "10000000"     # 10 second I/O timeout
      - "-reconnect"
      - "1"
      - "-tcp_nodelay"
      - "1"
```

## Troubleshooting Steps

### 1. Check Basic Connectivity
```bash
# Test RTSP stream with FFmpeg directly
ffmpeg -rtsp_transport tcp -i rtsp://camera-ip:554/stream -t 10 -f null -
```

### 2. Monitor BirdNET-Go Logs
Look for these patterns in logs:
- `unhealthy stream detected` - Stream health monitoring triggered
- `restart_count` - Should increment with each restart attempt
- `circuit breaker opened` - Too many consecutive failures

### 3. Verify Camera Settings
- Ensure RTSP is enabled on camera
- Check authentication credentials
- Verify stream URL format
- Confirm camera is accessible on network

### 4. Test Different Transports
```yaml
realtime:
  rtsp:
    transport: tcp    # Try: tcp, udp, udp_multicast, http
```

### 5. Enable Debug Logging
Check FFmpeg errors by temporarily changing log level:
```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-loglevel"
      - "warning"     # Change from "error" to see more details
```

## Configuration Examples

### Complete Example for Problematic Cameras
```yaml
realtime:
  rtsp:
    urls:
      - "rtsp://admin:password@192.168.1.100:554/stream1"
      - "rtsp://admin:password@192.168.1.101:554/stream1"
    transport: tcp
    health:
      healthydatathreshold: 90   # Allow 90 seconds without data
      monitoringinterval: 30     # Check every 30 seconds
    ffmpegparameters:
      - "-stimeout"
      - "5000000"               # 5 second socket timeout
      - "-timeout"
      - "10000000"              # 10 second I/O timeout
      - "-reconnect"
      - "1"                     # Enable reconnection
      - "-reconnect_at_eof"
      - "1"                     # Reconnect at EOF
      - "-tcp_nodelay"
      - "1"                     # Disable Nagle's algorithm
      - "-rw_timeout"
      - "8000000"               # 8 second read/write timeout
```

### Minimal Configuration for Stable Cameras
```yaml
realtime:
  rtsp:
    urls:
      - "rtsp://admin:password@192.168.1.100:554/stream1"
    transport: tcp
    health:
      healthydatathreshold: 60
      monitoringinterval: 30
    # No custom FFmpeg parameters needed for stable cameras
```

### High-Performance Configuration
```yaml
realtime:
  rtsp:
    urls:
      - "rtsp://admin:password@192.168.1.100:554/stream1"
    transport: tcp
    health:
      healthydatathreshold: 30   # Quick failure detection
      monitoringinterval: 15     # Frequent health checks
    ffmpegparameters:
      - "-probesize"
      - "32"                    # Fast stream detection
      - "-analyzeduration"
      - "1000000"               # Quick analysis
      - "-tcp_nodelay"
      - "1"                     # Low latency
```

## When to Use Custom Parameters

### Use Custom Parameters When:
- Camera reboots don't properly close TCP connections
- Network has high latency or packet loss
- Streams frequently disconnect and don't recover
- You need faster failure detection
- Camera has specific RTSP implementation quirks

### Don't Use Custom Parameters When:
- Default settings work fine
- You're unsure about the impact
- Camera manufacturer provides specific recommendations
- Network is stable and reliable

## Getting Help

If you're still experiencing issues:

1. **Check the GitHub Issues**: Search for similar problems in the [BirdNET-Go GitHub repository](https://github.com/tphakala/birdnet-go/issues)

2. **Enable Debug Logging**: Add `-loglevel warning` to see detailed FFmpeg output

3. **Test with FFmpeg Directly**: Verify the stream works outside of BirdNET-Go

4. **Provide Logs**: Include relevant log entries when reporting issues

5. **Camera Information**: Specify camera make/model and firmware version

Remember: Start with conservative settings and gradually adjust based on your specific camera and network conditions.