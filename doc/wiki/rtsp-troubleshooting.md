# RTSP Troubleshooting Guide

This guide covers common RTSP streaming issues and their solutions in BirdNET-Go, including advanced configuration options for problematic cameras.

## Table of Contents

- [Common RTSP Issues](#common-rtsp-issues)
- [Using `.local` (mDNS) hostnames in containers](#using-local-mdns-hostnames-in-containers)
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
Use custom FFmpeg parameters to add connection timeouts and failure detection strategies.

### High Restart Count with Circuit Breaker

**Symptoms:**

- Frequent stream restarts
- Circuit breaker opens after consecutive failures
- Streams get throttled or stopped completely

**Root Cause:**
Network issues, authentication problems, or incompatible camera settings causing repeated failures.

**Solution:**
Check network connectivity, verify RTSP credentials, and adjust health monitoring thresholds.

## Using `.local` (mDNS) hostnames in containers

A camera or microphone addressed as `rtsp://cam.local/...` is resolved by multicast DNS, which a container cannot do itself. The image resolves `.local` names through the host's avahi-daemon, so two read-only host directories are mounted into the container:

| Mount                                       | Purpose                                                                                                                                                                             |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-v /run/avahi-daemon:/run/avahi-daemon:ro` | Avahi's simple socket: `.local` name resolution (FFmpeg, MQTT, webhooks, native ingest).                                                                                            |
| `-v /run/dbus:/run/dbus:ro`                 | The host's system D-Bus: DNS-SD service discovery (browsing `_rtsp._tcp` services and reading their TXT records) through Avahi's D-Bus API, which the simple socket does not offer. |

Rules that apply to both:

- Mount the **directory**, not the socket file. Restarting avahi-daemon or dbus creates a new socket inode that a file bind mount never follows.
- Always `:ro`, and never add `:z` or `:Z`: they relabel the host's directory and can stop avahi-daemon or dbus from using it.
- Without avahi-daemon on the host, `.local` names cannot be resolved in the container, even if the host does it through systemd-resolved (`MulticastDNS=yes`), because the container cannot reach resolved's stub. Install `avahi-daemon` (and set `MulticastDNS=no` in resolved to avoid two responders), or use the device's IP address or a router DNS name with a DHCP reservation. Names that unicast DNS already resolves keep working either way.

How each deployment gets the mounts:

- **Docker (`install.sh`):** added to the unit on install and update when the host socket exists (`/run/avahi-daemon/socket`, `/run/dbus/system_bus_socket`). Re-run the update after installing avahi.
- **Docker compose and Portainer:** enabled by default in the compose files. Rootful Docker creates a missing directory (harmless); rootless Docker without avahi or D-Bus must remove the line, or the container will not start.
- **Podman:** shipped commented out in the quadlet and compose files, because Podman refuses to start a container whose bind source is missing. `podman-install.sh` enables them when the sockets exist (re-run it if avahi is removed). D-Bus authenticates by uid, so under rootless Podman it only works with `UserNS=keep-id` (`userns_mode: keep-id` in compose). Without keep-id the client fails with `Failed to create client object: An unexpected D-Bus error occurred`; `podman-install.sh` therefore enables the D-Bus line only together with keep-id.
- **Unraid:** the two advanced path settings in the template.

Security trade-off of the D-Bus mount: the container can talk to any system bus service its uid is allowed to call by D-Bus policy and polkit, as an ordinary uid with no active session. That is more exposure than the Avahi socket alone. To opt out, remove the `/run/dbus` line; `.local` name resolution does not depend on it. SELinux-enforcing hosts (Fedora, RHEL) are expected to deny the container access to both sockets; the options are `SecurityLabelDisable=true` (drops SELinux separation for the container, at your own risk), a local policy module, or IP addresses.

Check from the host:

```bash
docker exec birdnet-go getent hosts cam.local
```

If it prints nothing, the stream health view and the log show a DNS failure with a `.local` specific hint. Restart the container if the host's directory was recreated (for example after reinstalling avahi).

## Health Monitoring Configuration

BirdNET-Go includes configurable health monitoring for RTSP streams:

```yaml
realtime:
  rtsp:
    health:
      healthydatathreshold: 60 # seconds before stream considered unhealthy
      monitoringinterval: 30 # health check interval in seconds
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
      healthydatathreshold: 120 # Allow 2 minutes without data
      monitoringinterval: 60 # Check every minute
```

For fast networks requiring quick recovery:

```yaml
realtime:
  rtsp:
    health:
      healthydatathreshold: 30 # Only allow 30 seconds without data
      monitoringinterval: 15 # Check every 15 seconds
```

## Advanced FFmpeg Parameters

The `ffmpegparameters` setting allows you to add custom FFmpeg command-line arguments for problematic cameras.

### FFmpeg Version Compatibility

**Important:** FFmpeg parameter names and defaults have changed across versions:

- **FFmpeg 5.0+**: Uses `-timeout` for socket timeouts (current)
- **FFmpeg 4.x and earlier**: Used `-stimeout` for socket timeouts (deprecated)

**Check your FFmpeg version:**

```bash
ffmpeg -version
```

If using an older version, you may need to use `-stimeout` instead of `-timeout`.

### Testing and Verification

All parameters in this guide have been tested with:

- **FFmpeg version**: 5.1.6-0+deb12u1
- **Test stream**: Live RTSP camera stream
- **Verification method**: Direct FFmpeg command testing

Parameters marked as ✅ **Verified** have been tested and confirmed working.
Parameters marked as ❌ **Not Available** have been tested and confirmed not supported for RTSP.

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
      - "-timeout"
      - "5000000" # 5 second socket timeout (in microseconds)
      - "-buffer_size"
      - "65536" # 64KB buffer size for better data handling
```

**Important Notes:**

- **FFmpeg 5.0+ uses `-timeout`** instead of the older `-stimeout` option
- **Parameter placement is crucial**: The `-timeout` parameter must be placed **before** the RTSP URL in the command line for proper functionality
- **Default timeout values:**
  - `-timeout`: 0 (no timeout, infinite wait)
  - `-listen_timeout`: -1 (infinite timeout)
- **Units are microseconds** (1 second = 1,000,000 microseconds)
- **Timeout functionality verified**: When a timeout occurs, FFmpeg will exit with "Connection timed out" error

**Example:**

```bash
ffmpeg -rtsp_transport tcp -timeout 5000000 -i rtsp://usser:password@192.168.1.100/stream -vn -f s16le -ar 48000 -ac 1 -f null -
```

### FFmpeg Parameter Defaults Reference

#### RTSP-Specific Parameters (tested with FFmpeg 5.1.6)

| Parameter             | Default Value       | Description                     | Units        | RTSP Support    |
| --------------------- | ------------------- | ------------------------------- | ------------ | --------------- |
| `-timeout`            | 0 (infinite)        | Socket I/O timeout for RTSP     | microseconds | ✅ **Verified** |
| `-listen_timeout`     | -1 (infinite)       | Connection timeout              | seconds      | ✅ **Verified** |
| `-buffer_size`        | -1 (system default) | Socket buffer size              | bytes        | ✅ **Verified** |
| `-probesize`          | 5000000             | Probe size for stream detection | bytes        | ✅ **Verified** |
| `-analyzeduration`    | 5000000             | Analysis duration               | microseconds | ✅ **Verified** |
| `-reorder_queue_size` | -1 (auto)           | Packet reorder buffer size      | packets      | ✅ **Verified** |
| `-rtsp_transport`     | 0 (auto)            | RTSP transport protocol         | flags        | ✅ **Verified** |
| `-rtsp_flags`         | 0 (none)            | RTSP flags                      | flags        | ✅ **Verified** |
| `-min_port`           | 5000                | Minimum local UDP port          | integer      | ✅ **Verified** |
| `-max_port`           | 65000               | Maximum local UDP port          | integer      | ✅ **Verified** |

#### Parameters NOT Available for RTSP (tested)

| Parameter             | Reason                           | Alternative            |
| --------------------- | -------------------------------- | ---------------------- |
| `-stimeout`           | **Deprecated** in FFmpeg 5.0+    | Use `-timeout` instead |
| `-rw_timeout`         | Global option, not RTSP-specific | Use `-timeout`         |
| `-reconnect`          | HTTP/HTTPS only                  | Not available for RTSP |
| `-reconnect_at_eof`   | HTTP/HTTPS only                  | Not available for RTSP |
| `-reconnect_streamed` | HTTP/HTTPS only                  | Not available for RTSP |
| `-tcp_nodelay`        | TCP protocol only                | Not available for RTSP |

**Common timeout values:**

- 1 second = 1,000,000 microseconds
- 5 seconds = 5,000,000 microseconds
- 10 seconds = 10,000,000 microseconds

#### RTSP-Specific Settings

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-rtsp_flags"
      - "prefer_tcp" # Force TCP transport (helpful for firewall issues)
      - "-listen_timeout"
      - "10" # Connection timeout in seconds (default: -1, infinite)
```

**Note:** Reconnection parameters (`-reconnect`, `-reconnect_at_eof`, etc.) are **not available for RTSP** - they only work with HTTP/HTTPS protocols.

#### Stream Detection and Buffering

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-probesize"
      - "1000000" # 1MB probe size for faster detection (default: 5000000)
      - "-analyzeduration"
      - "1000000" # 1 second analysis duration (default: 5000000)
      - "-reorder_queue_size"
      - "1000" # Packet reorder buffer size (default: -1, auto)
```

**Note:** TCP-specific parameters like `-tcp_nodelay` are **not available for RTSP**.

#### Complete Example for Connection Issues

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-timeout"
      - "5000000" # 5 second socket timeout
      - "-buffer_size"
      - "65536" # 64KB buffer size
      - "-rtsp_flags"
      - "prefer_tcp" # Force TCP transport
      - "-probesize"
      - "2000000" # 2MB probe size for faster detection
      - "-analyzeduration"
      - "2000000" # 2 second analysis duration
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
      - "-timeout"
      - "5000000" # 5 second timeout
```

### Dahua Cameras

Often benefit from increased timeouts:

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-timeout"
      - "15000000" # 15 second timeout
      - "-buffer_size"
      - "131072" # 128KB buffer size for better handling
```

### Generic IP Cameras with Poor TCP Handling

For cameras that don't properly close connections:

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-timeout"
      - "5000000" # 5 second socket timeout
      - "-buffer_size"
      - "65536" # 64KB buffer size
      - "-rtsp_flags"
      - "prefer_tcp" # Force TCP transport
      - "-listen_timeout"
      - "10" # 10 second connection timeout
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
    transport: tcp # Try: tcp, udp, udp_multicast, http
```

### 5. Enable Debug Logging

Check FFmpeg errors by temporarily changing log level:

```yaml
realtime:
  rtsp:
    ffmpegparameters:
      - "-loglevel"
      - "warning" # Change from "error" to see more details
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
      healthydatathreshold: 90 # Allow 90 seconds without data
      monitoringinterval: 30 # Check every 30 seconds
    ffmpegparameters:
      - "-timeout"
      - "5000000" # 5 second socket timeout
      - "-buffer_size"
      - "65536" # 64KB buffer size
      - "-rtsp_flags"
      - "prefer_tcp" # Force TCP transport
      - "-listen_timeout"
      - "15" # 15 second connection timeout
      - "-probesize"
      - "2000000" # 2MB probe size
      - "-analyzeduration"
      - "2000000" # 2 second analysis duration
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
      healthydatathreshold: 30 # Quick failure detection
      monitoringinterval: 15 # Frequent health checks
    ffmpegparameters:
      - "-timeout"
      - "3000000" # 3 second timeout for quick failure detection
      - "-probesize"
      - "1000000" # Fast stream detection (1MB)
      - "-analyzeduration"
      - "1000000" # Quick analysis (1 second)
      - "-buffer_size"
      - "32768" # 32KB buffer for lower latency
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
