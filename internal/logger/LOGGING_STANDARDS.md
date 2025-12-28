# BirdNET-Go Logging Standards

This document defines the logging standards, field naming conventions, and message formats used throughout the BirdNET-Go codebase.

## Table of Contents

- [Logger Initialization](#logger-initialization)
- [Field Types and Conventions](#field-types-and-conventions)
- [Field Naming Standards](#field-naming-standards)
- [Message Format Guidelines](#message-format-guidelines)
- [Module Names](#module-names)
- [Common Fields by Domain](#common-fields-by-domain)
- [Log Levels](#log-levels)
- [Anti-Patterns](#anti-patterns)

---

## Logger Initialization

### Standard Pattern

Every package should define a `GetLogger()` function:

```go
package mypackage

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the logger for this package.
// Fetched dynamically to ensure it uses the current centralized logger.
func GetLogger() logger.Logger {
    return logger.Global().Module("mypackage")
}
```

### Usage Rules

1. **Cache logger at function start** - never in loops:
   ```go
   func ProcessItems(items []Item) {
       log := GetLogger()  // Cache once at function start
       for _, item := range items {
           log.Debug("processing item", logger.String("id", item.ID))
       }
   }
   ```

2. **Use `GetLogger()` consistently** - don't create loggers directly

3. **Module names are lowercase** - use dots for hierarchy: `analysis.soundlevel`

---

## Field Types and Conventions

### Available Field Types

| Function | Go Type | JSON Output | Use Case |
|----------|---------|-------------|----------|
| `logger.String(key, val)` | string | `"key":"value"` | Text values, identifiers |
| `logger.Int(key, val)` | int | `"key":123` | Small integers |
| `logger.Int64(key, val)` | int64 | `"key":123456789` | Large integers, timestamps |
| `logger.Float64(key, val)` | float64 | `"key":1.234` | Decimal values (auto-rounded to 3 decimals) |
| `logger.Bool(key, val)` | bool | `"key":true` | Boolean flags |
| `logger.Duration(key, val)` | time.Duration | `"key":"5s"` | Time durations (config values) |
| `logger.Error(err)` | error | `"error":"message"` | Error values |
| `logger.Any(key, val)` | any | varies | Complex/dynamic data (use sparingly) |

### Numeric Time Fields

For performance metrics that need to be aggregated/graphed, use numeric fields with unit in key:

```go
// Performance metrics - use numeric with unit in key
logger.Int64("latency_ms", duration.Milliseconds())
logger.Int64("duration_ms", elapsed.Milliseconds())
logger.Float64("elapsed_seconds", elapsed.Seconds())

// Configuration values - use Duration for human readability
logger.Duration("timeout", 30*time.Second)      // "timeout":"30s"
logger.Duration("interval", 5*time.Minute)      // "interval":"5m0s"
logger.Duration("keepalive", 60*time.Second)    // "keepalive":"1m0s"
```

**Rule**: Use `_ms` or `_seconds` suffix with numeric type for metrics; use `Duration` for config values.

---

## Field Naming Standards

### General Rules

1. **Always use `snake_case`** - never camelCase or PascalCase
2. **Be specific** - avoid generic names like `value`, `data`, `info`
3. **Use consistent patterns** - follow the established conventions below

### Standard Field Patterns

| Pattern | Examples | Use Case |
|---------|----------|----------|
| `{resource}_id` | `notification_id`, `detection_id`, `tx_id` | Unique identifiers |
| `{resource}_count` | `item_count`, `error_count`, `stream_count` | Numeric counts |
| `{action}_time` | `detection_time`, `sent_time`, `start_time` | Timestamps |
| `{resource}_path` | `file_path`, `db_path`, `cert_path` | File/directory paths |
| `is_{state}` | `is_healthy`, `is_connected`, `is_enabled` | Boolean state |
| `has_{feature}` | `has_error`, `has_ca_cert`, `has_metadata` | Boolean presence |
| `{action}_ms` | `latency_ms`, `duration_ms`, `elapsed_ms` | Performance timing |
| `{action}_seconds` | `timeout_seconds`, `interval_seconds` | Longer durations |

### Standard Field Names

#### Identifiers
| Field | Type | Description |
|-------|------|-------------|
| `id` | String | Generic ID (avoid - prefer specific names) |
| `notification_id` | String | Notification identifier |
| `detection_id` | String | Detection event identifier |
| `note_id` | String | Note/clip identifier |
| `tx_id` | String | Database transaction ID |
| `event_id` | String | Event identifier |
| `client_id` | String | MQTT/OAuth client identifier |
| `user_id` | String | User identifier |
| `job_id` | String | Background job identifier |
| `trace_id` | String | Distributed trace ID |
| `correlation_id` | String | Request correlation ID |

#### Operations & Context
| Field | Type | Description |
|-------|------|-------------|
| `operation` | String | Current operation being performed |
| `component` | String | System component name |
| `category` | String | Error/event category |
| `action` | String | Specific action being taken |
| `stage` | String | Processing stage |
| `status` | String | Current status |
| `reason` | String | Explanation for an action/state |

#### Network & URLs
| Field | Type | Description |
|-------|------|-------------|
| `url` | String | Full URL |
| `uri` | String | URI path |
| `path` | String | File or URL path |
| `host` | String | Hostname |
| `ip` | String | IP address |
| `broker` | String | Message broker address |
| `base_url` | String | Base URL for API |
| `redirect_uri` | String | OAuth redirect URI |

#### Counts & Metrics
| Field | Type | Description |
|-------|------|-------------|
| `count` | Int | Generic count (prefer specific names) |
| `total` | Int | Total number of items |
| `batch_size` | Int | Items per batch |
| `attempt` | Int | Current attempt number |
| `max_attempts` | Int | Maximum retry attempts |
| `success_count` | Int | Successful operations |
| `failure_count` | Int | Failed operations |
| `rows_affected` | Int64 | Database rows affected |

#### Time & Duration
| Field | Type | Description |
|-------|------|-------------|
| `latency_ms` | Int64 | Request latency in milliseconds |
| `duration_ms` | Int64 | Operation duration in milliseconds |
| `timeout` | Duration | Timeout configuration |
| `interval` | Duration | Polling/check interval |
| `delay` | Duration | Delay before action |
| `age` | Duration | Time since creation |

#### Boolean Flags
| Field | Type | Description |
|-------|------|-------------|
| `enabled` | Bool | Feature is enabled |
| `connected` | Bool | Connection is active |
| `tls_enabled` | Bool | TLS is configured |
| `debug` | Bool | Debug mode is active |
| `retain` | Bool | MQTT retain flag |
| `partial_success` | Bool | Some operations succeeded |

---

## Message Format Guidelines

### Message Structure

Log messages should be:
- **Action-oriented** - describe what's happening
- **Lowercase** - start with lowercase letter
- **Concise** - keep under 60 characters
- **No punctuation** - no trailing periods

### Good Examples

```go
// Info - successful operations
log.Info("connected to MQTT broker", logger.String("broker", addr))
log.Info("database migration complete", logger.Int("migrations_applied", count))
log.Info("circuit breaker state transition", logger.String("from", old), logger.String("to", new))

// Debug - verbose operation tracking
log.Debug("processing detection batch", logger.Int("batch_size", len(items)))
log.Debug("cache hit", logger.String("key", key), logger.String("provider", name))
log.Debug("attempting reconnection", logger.Int("attempt", n))

// Warn - non-critical issues
log.Warn("connection attempt too recent", logger.Duration("cooldown", remaining))
log.Warn("rate limit exceeded", logger.String("provider", name))
log.Warn("deprecated configuration option", logger.String("option", key))

// Error - failures requiring attention
log.Error("database query failed", logger.Error(err), logger.String("operation", op))
log.Error("failed to publish message", logger.Error(err), logger.String("topic", topic))
log.Error("authentication failed", logger.String("ip", clientIP), logger.String("reason", reason))
```

### Message Patterns by Log Level

| Level | Pattern | Example |
|-------|---------|---------|
| Info | `{action} {object}` | `"started health check monitoring"` |
| Debug | `{action} {object}` | `"processing event batch"` |
| Warn | `{issue description}` | `"connection attempt too recent"` |
| Error | `{failed action}` | `"failed to create notification"` |

---

## Module Names

### Registered Modules

| Module | Package | Description |
|--------|---------|-------------|
| `api` | internal/api/v2 | API endpoints and routing |
| `access` | internal/api/middleware | HTTP access logging |
| `analysis` | internal/analysis | Bird sound analysis |
| `analysis.soundlevel` | internal/analysis | Sound level monitoring |
| `audio` | internal/myaudio | Audio capture and processing |
| `audio.ffmpeg` | internal/myaudio | FFmpeg stream management |
| `auth` | internal/security | Authentication |
| `backup` | internal/backup | Backup operations |
| `birdweather` | internal/birdweather | BirdWeather integration |
| `config` | internal/conf | Configuration management |
| `datastore` | internal/datastore | Database operations |
| `echo` | internal/logger | Echo web framework adapter |
| `events` | internal/events | Event bus system |
| `imageprovider` | internal/imageprovider | Bird image fetching |
| `mqtt` | internal/mqtt | MQTT client |
| `notification` | internal/notification | Notification system |
| `security` | internal/security | Security and authorization |
| `telemetry` | internal/telemetry | Error reporting and metrics |
| `weather` | internal/weather | Weather data integration |

### Module Naming Rules

1. **Lowercase only** - no uppercase letters
2. **No underscores** - use dots for hierarchy
3. **Match package name** - when possible
4. **Hierarchical** - use dots for sub-modules: `analysis.soundlevel`

---

## Common Fields by Domain

### MQTT Operations

```go
log.Info("connected to broker",
    logger.String("broker", config.Broker),
    logger.String("client_id", config.ClientID),
    logger.Bool("tls_enabled", config.TLSEnabled),
    logger.Duration("keepalive", config.KeepAlive))

log.Debug("publishing message",
    logger.String("topic", topic),
    logger.Int("qos", qos),
    logger.Bool("retain", retain),
    logger.Int("payload_size", len(payload)))
```

### Database Operations

```go
log.Debug("executing query",
    logger.String("operation", "select"),
    logger.String("table", tableName),
    logger.Int64("duration_ms", elapsed.Milliseconds()))

log.Info("migration complete",
    logger.Int("rows_affected", rows),
    logger.String("tx_id", txID),
    logger.Int64("duration_ms", elapsed.Milliseconds()))
```

### HTTP/API Operations

```go
log.Info("request",
    logger.String("method", method),
    logger.String("uri", uri),
    logger.Int("status", statusCode),
    logger.String("ip", clientIP),
    logger.Int64("latency_ms", latency.Milliseconds()))
```

### Notification Operations

```go
log.Info("notification sent",
    logger.String("notification_id", id),
    logger.String("provider", providerName),
    logger.String("species", speciesName),
    logger.Int64("latency_ms", elapsed.Milliseconds()))

log.Warn("circuit breaker open",
    logger.String("provider", name),
    logger.Int("consecutive_failures", failures),
    logger.Duration("recovery_timeout", timeout))
```

### Analysis Operations

```go
log.Info("detection processed",
    logger.String("species", species),
    logger.String("scientific_name", sciName),
    logger.Float64("confidence", confidence),
    logger.String("source", source))
```

---

## Log Levels

### When to Use Each Level

| Level | Use Case | Visible By Default |
|-------|----------|-------------------|
| **Debug** | Verbose operation tracking, diagnostics | No |
| **Info** | Normal operations, state changes | Yes |
| **Warn** | Non-critical issues, degraded operation | Yes |
| **Error** | Failures requiring attention | Yes |

### Level Guidelines

**Debug**
- Function entry/exit for complex operations
- Cache hits/misses
- Retry attempts
- Detailed state transitions
- Values of variables during processing

**Info**
- Service startup/shutdown
- Successful connections
- Configuration loaded
- Periodic status reports
- Feature enabled/disabled

**Warn**
- Deprecated feature usage
- Rate limiting triggered
- Fallback behavior activated
- Recoverable errors
- Configuration issues (non-fatal)

**Error**
- Failed operations that affect functionality
- Connection failures
- Database errors
- Authentication failures
- Resource exhaustion

---

## Anti-Patterns

### Don't Do This

```go
// BAD: Using string for errors
log.Error("failed", logger.String("error", err.Error()))
// GOOD: Use logger.Error()
log.Error("operation failed", logger.Error(err))

// BAD: camelCase field names
log.Info("done", logger.Int("itemCount", count))
// GOOD: snake_case
log.Info("done", logger.Int("item_count", count))

// BAD: Generic field names
log.Info("processed", logger.Any("data", result))
// GOOD: Specific field names
log.Info("processed", logger.String("species", result.Species), logger.Float64("confidence", result.Confidence))

// BAD: Message with dynamic content
log.Info(fmt.Sprintf("processed %d items", count))
// GOOD: Static message with fields
log.Info("batch processed", logger.Int("item_count", count))

// BAD: Inconsistent duration format
log.Info("done", logger.String("took", elapsed.String()))
// GOOD: Use Duration or numeric with unit suffix
log.Info("done", logger.Int64("duration_ms", elapsed.Milliseconds()))

// BAD: Logging in tight loops without caching
for _, item := range items {
    logger.Global().Module("pkg").Debug("processing", ...)
}
// GOOD: Cache logger before loop
log := GetLogger()
for _, item := range items {
    log.Debug("processing", ...)
}

// BAD: Mixing Duration formats for same concept
log.Info("request", logger.Duration("latency", d))  // "latency":"5ms"
log.Info("request", logger.Int64("latency_ms", d.Milliseconds()))  // "latency_ms":5
// GOOD: Pick one format and use consistently (prefer numeric for metrics)
log.Info("request", logger.Int64("latency_ms", d.Milliseconds()))
```

---

## Appendix: Field Reference Quick Lookup

### By Type

**String Fields**: `operation`, `component`, `category`, `status`, `reason`, `provider`, `broker`, `client_id`, `topic`, `url`, `uri`, `path`, `ip`, `host`, `species`, `scientific_name`, `source`, `table`, `sql`, `tx_id`, `error_type`

**Int Fields**: `count`, `batch_size`, `attempt`, `max_attempts`, `qos`, `status_code`

**Int64 Fields**: `latency_ms`, `duration_ms`, `rows_affected`, `size_bytes`, `total_bytes`

**Float64 Fields**: `confidence`, `threshold`, `bytes_per_second`, `space_saved_percent`

**Bool Fields**: `enabled`, `connected`, `tls_enabled`, `debug`, `retain`, `partial_success`, `is_healthy`

**Duration Fields**: `timeout`, `interval`, `delay`, `cooldown`, `keepalive`, `age`, `recovery_timeout`

---

*Last updated: 2025-01-01*
