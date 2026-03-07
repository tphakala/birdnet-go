# System Monitor Package

The `monitor` package collects system resource metrics (CPU, memory, disk) and publishes them to the alerting engine via `alerting.TryPublish()`. It does **not** evaluate thresholds or generate notifications directly — that responsibility belongs to user-configurable alert rules in the `alerting` package.

## Overview

The system monitor is a pure metric collector. It periodically samples resource usage using [gopsutil](https://github.com/shirou/gopsutil) and publishes `AlertEvent` values to the alerting engine. The alerting engine evaluates these metrics against user-defined rules to decide whether to fire notifications.

## Architecture

```
┌─────────────────────┐
│   System Monitor    │
│                     │
│ ┌─────────────────┐ │
│ │ Polling Loop    │ │──► CPU % ──► alerting.TryPublish()
│ │ (configurable)  │ │──► Mem % ──► alerting.TryPublish()
│ └─────────────────┘ │──► Disk % ─► alerting.TryPublish()
│                     │      (per mount point)
└─────────────────────┘
```

## Configuration

```yaml
realtime:
  monitoring:
    enabled: true         # Enable/disable monitoring
    checkinterval: 60     # Check interval in seconds

    cpu:
      enabled: true

    memory:
      enabled: true

    disk:
      enabled: true
      paths:              # Disk paths to monitor
        - "/"
        - "/home"
```

Thresholds are **not** configured here. Define alert rules in the alerting engine configuration to set warning/critical thresholds.

## Multi-Path Disk Monitoring

- Multiple disk paths can be monitored simultaneously
- Paths sharing the same mount point are grouped to avoid duplicate metrics
- Each mount point produces an independent `AlertEvent` with a `path` property
- The alerting engine's `MetricTracker` uses path-qualified keys to isolate per-disk ring buffers

### Automatic Path Detection

When disk monitoring is enabled, the monitor auto-detects paths critical to BirdNET-Go:

- **Database path** (if SQLite is enabled)
- **Audio clips path** (if export is enabled)
- **Configuration directory**
- **Container volumes** (`/data`, `/config` in Docker/Podman)

Auto-detected paths are added at runtime only (not persisted to config). To make them permanent, add them to your `config.yaml`.

## Usage

```go
config := conf.Get()
systemMonitor := monitor.NewSystemMonitor(config)
systemMonitor.Start()
defer systemMonitor.Stop()
```

## API Reference

```go
// Create new monitor instance
func NewSystemMonitor(config *conf.Settings) *SystemMonitor

// Start periodic metric collection
func (m *SystemMonitor) Start()

// Stop monitoring
func (m *SystemMonitor) Stop()

// Get list of monitored disk paths
func (m *SystemMonitor) GetMonitoredPaths() []string
```

## Logging

The monitor logs at several levels:

- **Info**: Startup, configuration, path detection
- **Warn**: Monitoring disabled
- **Error**: Failed metric collection
- **Debug**: Individual check results, mount point grouping
