# Telemetry Package Initialization Analysis

## Overview
This document analyzes the initialization patterns in the telemetry package and its dependencies, identifying potential circular dependencies and initialization order issues.

## Package Dependencies Graph
```
telemetry → conf (for settings)
telemetry → errors (for error handling)
telemetry → events (for event bus)
telemetry → logging (for loggers)

errors → events (for event publishing)
errors → telemetry (for privacy scrubbing)

events → logging (for logging)
```

## Initialization Patterns Found

### 1. Package-Level Variables

#### telemetry package:
- `var sentryInitialized bool` - tracks Sentry initialization
- `var deferredMessages []DeferredMessage` - stores messages before Sentry init
- `var attachmentUploader *AttachmentUploader` - singleton attachment uploader
- `var telemetryWorker *TelemetryWorker` - singleton worker instance
- `var telemetryInitialized atomic.Bool` - tracks event bus integration
- `var deferredInitMutex sync.Mutex` - protects deferred initialization

#### conf package:
- `var settingsInstance *Settings` - global settings singleton
- `var once sync.Once` - ensures single initialization
- `var settingsMutex sync.RWMutex` - protects settings access

#### events package:
- `var globalEventBus *EventBus` - global event bus singleton
- `var globalMutex sync.Mutex` - protects event bus initialization
- `var hasActiveConsumers atomic.Bool` - fast path optimization

#### logging package:
- `var structuredLogger *slog.Logger` - global structured logger
- `var humanReadableLogger *slog.Logger` - global human-readable logger
- `var currentLogLevel = new(slog.LevelVar)` - dynamic log level
- `var initOnce sync.Once` - ensures single initialization

### 2. Init Functions

#### telemetry/eventbus_integration.go:
```go
func init() {
    logger = logging.ForService("telemetry-integration")
    if logger == nil {
        logger = slog.Default().With("service", "telemetry-integration")
    }
}
```

#### errors/telemetry_integration.go:
```go
func init() {
    // Initialize hasActiveReporting to false (no telemetry or hooks by default)
    hasActiveReporting.Store(false)
}
```

### 3. Deferred Initialization Pattern

The telemetry package uses a **deferred initialization pattern** to avoid circular dependencies:

1. **InitializeErrorIntegration()** - Called early, sets up error package hooks
   - Sets telemetry reporter in errors package
   - Sets privacy scrubber function
   - Can work even if Sentry is not initialized

2. **InitSentry()** - Initializes Sentry SDK
   - Requires conf.Settings to be loaded
   - Processes any deferred messages
   - Creates attachment uploader

3. **InitializeTelemetryEventBus()** - Called last, sets up event bus integration
   - Requires event bus to be initialized
   - Creates telemetry worker
   - Registers worker as event consumer
   - Sets event publisher in errors package

### 4. Circular Dependency Issues

#### Potential Circular Dependencies:
1. **telemetry ↔ errors**: 
   - telemetry imports errors for error handling
   - errors imports telemetry indirectly via SetTelemetryReporter/SetPrivacyScrubber
   - **Solution**: Uses interfaces and deferred initialization

2. **errors → events → telemetry**:
   - errors publishes to events
   - events delivers to telemetry worker
   - telemetry reports errors
   - **Solution**: Event-driven architecture breaks direct dependency

### 5. Initialization Order Requirements

Based on the analysis, the correct initialization order should be:

1. **logging.Init()** - Initialize logging first (no dependencies)
2. **conf.Load()** - Load settings (depends on logging)
3. **telemetry.InitializeErrorIntegration()** - Set up error hooks (depends on conf)
4. **events.Initialize()** - Initialize event bus (depends on logging)
5. **telemetry.InitSentry()** - Initialize Sentry (depends on conf)
6. **telemetry.InitializeTelemetryEventBus()** - Final integration (depends on all above)

### 6. Singleton Patterns

All packages use thread-safe singleton patterns:
- **conf**: Uses sync.Once and mutex-protected global variable
- **events**: Uses mutex-protected global variable with lazy initialization
- **logging**: Uses sync.Once for initialization
- **telemetry**: Uses multiple singletons with various protection mechanisms

### 7. Fast Path Optimizations

Several packages implement fast path optimizations:
- **events**: `hasActiveConsumers` atomic bool to skip processing when no consumers
- **errors**: `hasActiveReporting` atomic bool to skip reporting when disabled
- **telemetry**: `telemetryInitialized` atomic bool to track initialization state

### 8. Thread Safety

All packages implement proper thread safety:
- Mutex protection for shared state
- Atomic operations for boolean flags
- Read/write mutexes for frequently read data
- sync.Once for one-time initialization

## Recommendations

1. **Document initialization order**: Add clear documentation about the required initialization sequence
2. **Add initialization checks**: Add defensive checks in each init function to verify dependencies
3. **Consider dependency injection**: For better testability and to avoid global state
4. **Add initialization status API**: Provide methods to check initialization state of each component
5. **Improve error handling**: Better error messages when initialization order is wrong

## Potential Deadlock Scenarios

1. **Logging during initialization**: If error reporting tries to use telemetry before it's initialized
2. **Event bus circular calls**: If telemetry worker errors trigger new error events
3. **Mutex ordering**: Different lock acquisition orders could cause deadlocks

## Best Practices Observed

1. **Defensive nil checks**: Most packages check for nil loggers/settings
2. **Graceful degradation**: Systems work without telemetry enabled
3. **Deferred processing**: Messages queued when systems not ready
4. **Atomic state tracking**: Using atomic bools for lock-free state checks