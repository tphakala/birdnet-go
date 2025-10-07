# Notification Package Telemetry Integration

## Overview

The notification package includes comprehensive privacy-first telemetry integration for monitoring operational health, debugging issues, and improving reliability. All telemetry is **optional** and **privacy-preserving by default**.

## Features

- **Circuit Breaker Monitoring** - Track provider health and recovery
- **Webhook Error Reporting** - HTTP failures, timeouts, network issues
- **Initialization Errors** - Configuration and setup problems
- **Worker Panic Recovery** - Catch and report crashes
- **Rate Limiting Alerts** - Detect configuration issues or spam

## Architecture

### No Circular Imports

The notification package defines the `TelemetryReporter` interface, which is implemented by the telemetry package via dependency injection:

```
notification (defines interface) → telemetry (implements)
```

This clean separation allows telemetry to be optional and prevents circular dependencies.

### Privacy-First Design

All telemetry is scrubbed before reporting:

- ✅ **URLs anonymized** - Webhook endpoints hashed via `privacy.AnonymizeURL()`
- ✅ **Credentials removed** - Auth tokens never logged (only type: bearer/basic/custom)
- ✅ **Messages scrubbed** - File paths, secrets removed via `privacy.ScrubMessage()`
- ✅ **Context filtered** - Cancellations not reported as errors
- ✅ **Metadata opt-in** - Detection data excluded by default

**Privacy Implementation:** The privacy package provides comprehensive scrubbing of sensitive data. See [../../privacy/README.md](../../privacy/README.md) for details on:
- URL anonymization patterns and algorithms
- Message scrubbing rules (file paths, credentials, tokens)
- Testing privacy compliance
- Adding new privacy rules

## Configuration

### TelemetryConfig

Telemetry is controlled globally through the Sentry setting. There are no granular per-event-type controls.

```go
type TelemetryConfig struct {
    Enabled                  bool    // Master switch (controlled by global Sentry setting)
    RateLimitReportThreshold float64 // Minimum drop rate % to trigger reporting (default: 50.0)
}
```

### Default Configuration

```go
config := notification.DefaultTelemetryConfig()
// Enabled: true (controlled by global Sentry setting)
// RateLimitReportThreshold: 50.0 (report when drop rate > 50%)
```

**Note:** When telemetry is enabled, all notification events are reported. There are no separate flags for circuit breakers, API errors, panics, or rate limiting. Detection metadata is never included to maintain privacy.

## Integration

### 1. Service Setup

```go
// Create telemetry reporter (implemented by telemetry package)
reporter := telemetry.NewNotificationReporter(settings.Sentry.Enabled)

// Create telemetry integration
config := notification.DefaultTelemetryConfig()
telemetryIntegration := notification.NewNotificationTelemetry(&config, reporter)

// Inject into service
service := notification.NewService(serviceConfig)
service.SetTelemetry(telemetryIntegration)
```

### 2. Circuit Breaker Setup

```go
// Circuit breakers get telemetry from service
circuitBreaker := notification.NewPushCircuitBreaker(cbConfig, metrics, "webhook-1")
circuitBreaker.SetTelemetry(service.GetTelemetry())
```

### 3. Webhook Provider Setup

```go
// Webhook providers get telemetry from service
webhookProvider := notification.NewWebhookProvider(name, enabled, endpoints, types, template)
webhookProvider.SetTelemetry(service.GetTelemetry())
```

## Telemetry Events

### 1. Circuit Breaker State Transitions

**When:** Circuit breaker changes state (closed → open, open → half-open, etc.)

**Data Reported:**
- Provider name
- Old and new state
- Consecutive failure count
- Time in previous state
- Configuration (thresholds, timeouts)

**Severity:**
- `warning` - Circuit opens (provider failing)
- `info` - Circuit closes (recovery)

**Example:**
```json
{
  "message": "Circuit breaker state transition: closed → open",
  "level": "warning",
  "tags": {
    "component": "notification",
    "provider": "webhook-primary",
    "old_state": "closed",
    "new_state": "open",
    "consecutive_failures": "5"
  },
  "contexts": {
    "circuit_breaker": {
      "failure_threshold": 5,
      "timeout_seconds": 30,
      "time_in_previous_state_seconds": 120
    }
  }
}
```

### 2. Webhook Request Errors

**When:** HTTP request fails (network error, timeout, 4xx/5xx status)

**Data Reported:**
- Provider name and type
- HTTP status code (if applicable)
- Endpoint hash (URL anonymized)
- Method (POST/PUT/PATCH)
- Auth type (bearer/basic/custom - never credentials)
- Error classification (timeout, cancellation)

**Severity:**
- `critical` - Timeouts (network/provider issues)
- `error` - 5xx server errors
- `warning` - 4xx client errors (config issues)
- `info` - Context cancellation (filtered out, not reported)

**Privacy:**
- ✅ URL: `https://hooks.slack.com/services/T00/B00/xyz` → `webhook_a1b2c3d4`
- ✅ Auth: Only type logged, never tokens
- ✅ Response body: Truncated and scrubbed

**Example:**
```json
{
  "message": "Webhook request timed out",
  "level": "critical",
  "tags": {
    "component": "notification",
    "provider": "webhook-primary",
    "provider_type": "webhook",
    "status_code": "0",
    "method": "POST",
    "auth_type": "bearer",
    "endpoint_hash": "webhook_a1b2c3d4",
    "is_timeout": "true"
  }
}
```

### 3. Provider Initialization Errors

**When:** Provider creation fails (template parsing, validation, secret resolution)

**Data Reported:**
- Provider name and type
- Error type (template_parse, validation, secret_resolution)
- Scrubbed error message

**Severity:** `error` (prevents provider from working)

**Privacy:**
- ✅ Error messages scrubbed (paths, secrets removed)
- ✅ Template content not included
- ✅ Secret paths/values not included

**Example:**
```json
{
  "message": "Provider initialization failed: template parse error",
  "level": "error",
  "tags": {
    "component": "notification",
    "provider": "webhook-custom",
    "provider_type": "webhook",
    "error_type": "template_parse"
  }
}
```

### 4. Worker Panic Recovery

**When:** Worker goroutine panics and is recovered

**Data Reported:**
- Worker type (detection_consumer, resource_consumer)
- Panic value (scrubbed)
- Stack trace (scrubbed for privacy)
- Worker state (events processed/dropped)

**Severity:** `critical` (worker crashed but recovered)

**Privacy:**
- ✅ Stack traces scrubbed (detection metadata removed)
- ✅ Panic values scrubbed

**Example:**
```json
{
  "message": "Worker panic recovered: runtime error",
  "level": "critical",
  "tags": {
    "component": "notification",
    "worker_type": "detection_consumer",
    "panic_type": "string"
  },
  "contexts": {
    "worker_state": {
      "events_processed": 1523,
      "events_dropped": 12
    }
  }
}
```

### 5. Rate Limit Exceeded

**When:** Sustained high drop rate detected (>50%)

**Data Reported:**
- Dropped event count
- Drop rate percentage
- Rate limiter configuration
- Window size

**Severity:** `warning` (indicates config issue or spam)

**Example:**
```json
{
  "message": "Notification rate limit exceeded: 150 events dropped (60.0% drop rate)",
  "level": "warning",
  "tags": {
    "component": "notification",
    "subsystem": "rate_limiter",
    "drop_rate": "60.0"
  },
  "contexts": {
    "rate_limiter": {
      "window_seconds": 60,
      "max_events": 100,
      "dropped_count": 150,
      "drop_rate_percent": 60.0
    }
  }
}
```

## Testing

### Mock Reporter for Testing

```go
type MockReporter struct {
    capturedEvents []Event
}

func (m *MockReporter) CaptureEvent(message, level string, tags, contexts) {
    m.capturedEvents = append(m.capturedEvents, Event{message, level, tags, contexts})
}

// In tests
reporter := &MockReporter{enabled: true}
telemetry := notification.NewNotificationTelemetry(&config, reporter)

// Trigger event
circuitBreaker.Call(ctx, failingFunc)

// Verify
assert.Len(t, reporter.capturedEvents, 1)
assert.Equal(t, "warning", reporter.capturedEvents[0].level)
```

### Disabling Telemetry

Telemetry is controlled globally through the Sentry configuration:

```go
// Disable all telemetry
config := notification.DefaultTelemetryConfig()
config.Enabled = false

// Or use noop reporter (does nothing)
reporter := notification.NewNoopTelemetryReporter()
```

## Privacy Compliance Checklist

- ✅ **No PII** - User data never sent
- ✅ **No URLs** - Endpoints anonymized via SHA256 hash
- ✅ **No credentials** - Auth tokens/passwords never logged
- ✅ **Scrubbed messages** - File paths, secrets removed
- ✅ **Filtered events** - Context cancellations not reported
- ✅ **No metadata** - Detection details never included
- ✅ **Provider names only** - Never full endpoint URLs

## Performance Impact

- **Minimal overhead** - Telemetry calls are fast and non-blocking
- **Lazy evaluation** - Events only built if telemetry enabled
- **No synchronous I/O** - Reporter implementations should be async
- **Circuit breaker aware** - Won't spam during failures

## Troubleshooting

### Telemetry Not Reporting

1. Check if telemetry is enabled:
   ```go
   if telemetry.IsEnabled() {
       // Should return true
   }
   ```

2. Verify reporter is set:
   ```go
   if service.GetTelemetry() == nil {
       // Telemetry not injected
   }
   ```

3. Check configuration flags:
   ```go
   config := service.GetTelemetry().config
   // Verify specific reports are enabled
   ```

### Too Many Events

Telemetry is designed to be quiet during normal operation:

- Circuit breaker: Only state changes (not every failure)
- Webhook errors: Only actual failures (not cancellations)
- Rate limiting: Only sustained high drop rates (>50%)
- Panics: Should be rare in production

If seeing too many events, check for:
- Misconfigured providers (causing repeated failures)
- Network issues (causing timeouts)
- Rate limits too low (causing drops)

## Implementation Status

### ✅ Completed (Stages 1-5)

- Circuit breaker telemetry
- Webhook error reporting
- Provider initialization errors
- Worker panic recovery
- Rate limiter alerts

### 📋 Future Enhancements (Optional)

- Resource exhaustion (memory, store capacity)
- Performance metrics (slow processing)
- Provider-specific metrics
- Aggregated health dashboard

## See Also

- [internal/telemetry/README.md](../../telemetry/README.md) - Core telemetry system
- [internal/privacy/README.md](../../privacy/README.md) - Privacy scrubbing
- [Circuit Breaker Documentation](DOS_PROTECTION.md)
- [Webhook Provider Documentation](WEBHOOK.md)

---

**Last Updated:** 2025-10-07
**Status:** Production Ready (Stages 1-5 Complete)
