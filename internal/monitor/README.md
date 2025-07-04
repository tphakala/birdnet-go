# System Monitor Package

The `monitor` package provides system resource monitoring capabilities for BirdNET-Go, tracking CPU, memory, and disk usage with configurable thresholds and alert notifications.

## Overview

The system monitor continuously tracks resource usage and publishes events when thresholds are exceeded. It integrates with the event bus for non-blocking notifications and supports persistent alerts for critical conditions.

## Features

- **Resource Monitoring**: Tracks CPU, memory, and disk usage
- **Configurable Thresholds**: Warning and critical thresholds per resource
- **Event Bus Integration**: Non-blocking event publishing
- **Persistent Notifications**: Critical disk alerts resubmit every 30 minutes
- **Recovery Tracking**: Monitors recovery duration and sends notifications
- **Multi-Path Disk Monitoring**: Monitor multiple disk paths simultaneously
- **Dedicated Logging**: Separate `monitor.log` file for troubleshooting

## Architecture

```
┌─────────────────────┐
│   System Monitor    │
│                     │
│ ┌─────────────────┐ │
│ │ Resource Check  │ │──────► CPU Usage
│ │     Loop        │ │──────► Memory Usage
│ └─────────────────┘ │──────► Disk Usage
│                     │
│ ┌─────────────────┐ │
│ │ Threshold       │ │
│ │ Evaluation      │ │
│ └─────────────────┘ │
│                     │
│ ┌─────────────────┐ │
│ │ Event Publisher │ │──────► Event Bus
│ └─────────────────┘ │        └─► Notifications
└─────────────────────┘
```

## Configuration

System monitoring is configured through the application's configuration file:

```yaml
realtime:
  monitoring:
    enabled: true                    # Enable/disable monitoring
    checkinterval: 60               # Check interval in seconds
    
    cpu:
      enabled: true
      warning: 85.0                 # Warning threshold (%)
      critical: 95.0                # Critical threshold (%)
    
    memory:
      enabled: true
      warning: 85.0
      critical: 95.0
    
    disk:
      enabled: true
      warning: 85.0
      critical: 95.0
      # Paths MAY be quoted to avoid '/' being interpreted as an alias anchor
      paths:                        # Disk paths to monitor
        - "/"                       # Root filesystem
        - "/home"                   # Home partition
        - "/var"                    # Var partition
```

### Default Values

- Check interval: 60 seconds
- CPU thresholds: 85% warning, 95% critical
- Memory thresholds: 85% warning, 95% critical
- Disk thresholds: 85% warning, 95% critical
- Disk paths: ["/"] (defaults to root filesystem only, override with MONITOR_DISK_PATHS=)

## Usage

### Basic Integration

```go
import (
    "github.com/tphakala/birdnet-go/internal/monitor"
    "github.com/tphakala/birdnet-go/internal/conf"
)

// Create and start monitor
config := conf.Get()
systemMonitor := monitor.NewSystemMonitor(config)
systemMonitor.Start()
defer systemMonitor.Stop()
```

### Manual Resource Check

```go
// Trigger immediate resource check
systemMonitor.TriggerCheck()
```

### Get Resource Status

```go
// Get current status of all monitored resources
status := systemMonitor.GetResourceStatus()
// Returns map with resource status:
// {
//   "cpu": {
//     "current_value": "45.2%",
//     "in_warning": false,
//     "in_critical": false,
//     "last_check": "2024-01-15T10:30:00Z"
//   },
//   "disk:/var": {
//     "current_value": "92.5%",
//     "in_warning": true,
//     "in_critical": false,
//     "last_check": "2024-01-15T10:30:00Z"
//   }
// }
```

## Resource Monitoring Details

### CPU Monitoring

- Uses instant CPU percentage (no sampling delay)
- Monitors overall system CPU usage
- Suitable for detecting sustained high CPU usage

### Memory Monitoring

- Tracks virtual memory usage percentage
- Includes both RAM and swap usage
- Helps identify memory exhaustion scenarios

### Disk Monitoring (Multi-Path)

- Monitors multiple disk paths simultaneously
- Each path maintains independent alert states
- Path information included in notifications
- Logs detailed disk information for each path
- Critical alerts persist until resolved

## Alert Behavior

### Threshold Evaluation

1. **Critical State**: Resource usage ≥ critical threshold
2. **Warning State**: Resource usage ≥ warning threshold
3. **Normal State**: Resource usage < warning threshold (with 5% hysteresis)

### Hysteresis

To prevent alert flapping, the monitor implements 5% hysteresis:
- Warning clears when usage drops below (warning_threshold - 5%)
- Critical clears when usage drops below (critical_threshold - 5%)

### Persistent Notifications

Critical disk alerts have special handling:
- Initial notification sent when threshold exceeded
- Automatic resubmission every 30 minutes while critical
- 24-hour expiry time (vs 30 minutes for other alerts)
- Recovery notification includes duration in critical state

## Event Integration

The monitor publishes `ResourceEvent` objects to the event bus:

```go
type ResourceEvent interface {
    GetResourceType() string    // "cpu", "memory", "disk"
    GetCurrentValue() float64   // Current usage percentage
    GetThreshold() float64      // Threshold that was crossed
    GetSeverity() string        // "warning", "critical", "recovery"
    GetTimestamp() time.Time
    GetMetadata() map[string]interface{}
    GetPath() string           // Path for disk resources
}
```

### Event Flow

1. Monitor detects threshold crossing
2. Creates ResourceEvent with details
3. Publishes to event bus (non-blocking)
4. ResourceEventWorker consumes event
5. Notification created with appropriate priority

### Fallback Behavior

If event bus is unavailable, the monitor falls back to direct notification creation to ensure critical alerts are not lost.

## Logging

The monitor uses a dedicated log file: `logs/monitor.log`

### Log Levels

- **Info**: Normal operations, resource checks, threshold crossings
- **Warn**: Threshold exceeded, monitoring disabled
- **Error**: Failed resource checks, API errors
- **Debug**: Detailed check results, state transitions

### Key Log Messages

```
System monitor instance created
System monitor loop started
Disk usage check completed
Critical threshold exceeded
Resource usage recovered
System monitoring is disabled in configuration
```

## Multi-Path Disk Monitoring

The monitor supports monitoring multiple disk paths simultaneously (see 'Configuration', lines 61-69):

### Features

- Configure multiple disk paths in the `paths` array
- Each path maintains independent alert states
- Path information included in notification titles
- Separate throttling for each path's alerts
- Recovery notifications per path
- **Automatic Critical Path Detection**: Automatically monitors paths critical to BirdNET-Go operation

### Configuration Example

```yaml
disk:
  enabled: true
  warning: 85.0
  critical: 95.0
  paths:
    - "/"
    - "/home"
    - "/var"
    - "/data"
```

### Automatic Path Monitoring

When disk monitoring is enabled, the system automatically detects and monitors paths critical to application operation:

- **Database Path**: Location of `birdnet.db` (if SQLite is enabled)
- **Audio Clips Path**: Where audio clips are stored (if export is enabled)
- **Configuration Path**: Directory containing `config.yaml`
- **Container Volumes**: `/data` and `/config` when running in Docker/Podman

**Important Notes:**
- Auto-detected paths are added at runtime only (not persisted to config file)
- They are merged with your configured paths for monitoring
- The monitor log shows both configured and auto-detected paths
- To make auto-detected paths permanent, manually add them to your `config.yaml`

**Example Log Output:**
```
Disk monitoring paths configured user_configured=[] auto_detected=[/ /home/user/.config/birdnet-go ./clips] total_monitored=[/ /home/user/.config/birdnet-go ./clips]
```

### Notification Examples

- Warning: "High Disk (/home) Usage: 86.5% (threshold: 85.0%)"
- Critical: "Critical Disk (/) Usage: 95.2% (threshold: 90.0%)"
- Recovery: "Disk (/var) Usage Recovered: 78.0%"

### Path Validation

- Invalid paths are logged and skipped
- Valid paths are cached to avoid repeated filesystem checks
- Monitoring continues for all valid paths
- Duplicate paths are automatically removed

## Troubleshooting

### Monitor Not Starting

1. Check if monitoring is enabled in configuration
2. Look for "System monitoring is disabled" in `monitor.log`
3. Verify configuration file syntax

### No Notifications

1. Check `monitor.log` for threshold evaluations
2. Verify event bus is initialized
3. Check notification service logs
4. Ensure thresholds are set appropriately

### High Resource Usage

1. Monitor logs show detailed usage on each check
2. Check for resource leaks in application
3. Verify system has adequate resources
4. Consider adjusting thresholds

## Best Practices

1. **Set Appropriate Thresholds**: Consider your system's normal operating range
2. **Monitor the Monitor**: Check `monitor.log` periodically
3. **Test Alerts**: Use `TriggerCheck()` to test notification flow
4. **Plan for Growth**: Set thresholds with headroom for growth
5. **Multiple Disks**: Configure monitoring for critical mount points

## Example Scenarios

### Low Disk Space Alert

```
1. Disk usage reaches 85% → Warning notification
2. Admin acknowledges but doesn't act
3. Disk usage reaches 95% → Critical notification
4. Critical notification resubmits every 30 minutes
5. Admin frees space, usage drops to 80%
6. Recovery notification sent with duration
```

### CPU Spike Handling

```
1. CPU usage spikes to 90% → Warning notification
2. Usage fluctuates between 88-92% → No spam (throttling)
3. Usage drops to 79% → Recovery notification
4. Next spike is treated as new incident
```

## API Reference

### SystemMonitor

```go
// Create new monitor instance
func NewSystemMonitor(config *conf.Settings) *SystemMonitor

// Start monitoring
func (m *SystemMonitor) Start()

// Stop monitoring
func (m *SystemMonitor) Stop()

// Trigger immediate check
func (m *SystemMonitor) TriggerCheck()

// Get current resource status
func (m *SystemMonitor) GetResourceStatus() map[string]any

// Get list of monitored disk paths
func (m *SystemMonitor) GetMonitoredPaths() []string
```

### Configuration Structure

```go
type MonitoringConfig struct {
    Enabled       bool
    CheckInterval int
    CPU          ResourceConfig
    Memory       ResourceConfig
    Disk         DiskConfig
}

type ResourceConfig struct {
    Enabled  bool
    Warning  float64
    Critical float64
}

type DiskConfig struct {
    Enabled  bool
    Warning  float64
    Critical float64
    Paths    []string
}
```

## Future Enhancements

- [x] Multiple disk path monitoring
- [ ] Path-specific thresholds
- [ ] Network interface monitoring
- [ ] Process-specific monitoring
- [ ] Custom metric support
- [ ] Predictive alerts (trend analysis)
- [ ] Integration with external monitoring systems