# Package Reorganization: Telemetry Split

## Overview

The telemetry package has been reorganized to better separate concerns between Prometheus metrics collection and Sentry error tracking.

## New Package Structure

### `internal/observability` (NEW)
Contains all Prometheus metrics and monitoring functionality:
- `metrics.go` - Main metrics collector and registry
- `endpoint.go` - HTTP endpoint for metrics exposure
- `debug.go` - Debug and profiling endpoints
- `metrics/` subdirectory:
  - `birdnet.go` - BirdNET-specific metrics
  - `imageprovider.go` - Image provider metrics
  - `mqtt.go` - MQTT client metrics

### `internal/telemetry` (UPDATED)
Now contains only Sentry error tracking functionality:
- `sentry.go` - Privacy-compliant Sentry integration

## Migration Details

### Files Moved
- `internal/telemetry/metrics.go` → `internal/observability/metrics.go`
- `internal/telemetry/endpoint.go` → `internal/observability/endpoint.go`
- `internal/telemetry/debug.go` → `internal/observability/debug.go`
- `internal/telemetry/metrics/*` → `internal/observability/metrics/*`

### API Changes
All Prometheus-related functionality has moved from `telemetry` to `observability`:

#### Type Changes
- `telemetry.Metrics` → `observability.Metrics`
- `telemetry.Endpoint` → `observability.Endpoint`

#### Function Changes
- `telemetry.NewMetrics()` → `observability.NewMetrics()`
- `telemetry.NewEndpoint()` → `observability.NewEndpoint()`

### Import Changes
Files that previously imported:
```go
"github.com/tphakala/birdnet-go/internal/telemetry"
```

Now import:
```go
"github.com/tphakala/birdnet-go/internal/observability"
```

## Files Updated
The following files were updated to use the new observability package:

1. `internal/analysis/realtime.go`
2. `internal/analysis/processor/processor.go`
3. `internal/imageprovider/imageprovider.go`
4. `internal/imageprovider/avicommons.go`
5. `internal/imageprovider/imageprovider_test.go`
6. `internal/mqtt/client.go`
7. `internal/mqtt/client_test.go`
8. `internal/api/v2/test_utils.go`
9. `internal/api/v2/integrations.go`
10. `internal/api/v2/analytics_test.go`
11. `internal/httpcontroller/handlers/mqtt.go`

## Configuration Impact

No configuration changes are required. The existing `realtime.telemetry` settings continue to work with the observability package.

## Benefits

1. **Clear separation of concerns**: Metrics and error tracking are now in separate packages
2. **Better naming**: `observability` is more descriptive for metrics/monitoring
3. **Maintainability**: Easier to maintain and extend each functionality independently
4. **Privacy compliance**: Sentry functionality remains isolated with clear privacy controls

## Backward Compatibility

This is an internal reorganization only. No public APIs or configuration formats have changed.