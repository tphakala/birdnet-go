# Logger Package

This package provides a lightweight wrapper around Uber's Zap logging library to provide structured, high-performance logging for Go applications.

## Features

- Fast, structured logging with minimal allocations
- Support for both human-readable console logging and structured JSON logging
- Cross-platform support for Linux, macOS, and Windows
- Color-coded log levels for better readability (can be disabled)
- Development mode with debug-level logging
- Production mode with info-level logging
- Context-based logging with fields
- Named loggers for different components
- File output support with rotation capabilities (using lumberjack)
- **Unified initialization** with a single function for all logger types

## Package Organization

The logger package is organized into several files for maintainability:

- **config.go** - Configuration structures and helpers
- **logger.go** - Core logger implementation
- **global.go** - Global logger instance and functions
- **rotation.go** - Log rotation functionality
- **testing.go** - Testing utilities and helpers

## Basic Usage

### Import the package

```go
import "github.com/tphakala/birdnet-go/internal/logger"
```

### Use the global logger

The package provides global functions for common logging operations:

```go
// These use the default global logger
logger.Info("Application starting", "version", "1.0.0")
logger.Debug("Debug information", "details", details)
logger.Warn("Resource running low", "resource", "memory", "available", "10MB")
logger.Error("Operation failed", "error", err)
logger.Fatal("Cannot continue execution", "error", err) // Also calls os.Exit(1)
```

### Create a custom logger

You can create a custom configured logger with the unified initialization method:

```go
// Use helper methods for common configurations
config := logger.DefaultConfig()
// Or logger.ProductionConfig() for production settings

// Or configure manually
config = logger.Config{
    Level:        "info",
    JSON:         false,
    Development:  false,
    FilePath:     "", // Empty for console output
    DisableColor: false,
}

// Create a console-only logger
log, err := logger.NewLogger(config)
if err != nil {
    // handle error
}

// Create a file logger with rotation
if config.FilePath != "" {
    rotationConfig := logger.DefaultRotationConfig()
    log, err = logger.NewLogger(config, rotationConfig)
    if err != nil {
        // handle error
    }
}

log.Info("Using custom logger")
```

### Configure the global logger

You can initialize the global logger with your custom configuration:

```go
config := logger.Config{
    Level:        "info",
    JSON:         true, // Enable JSON structured logging
    Development:  false,
    FilePath:     "app.log",
    DisableColor: true,
}

err := logger.InitGlobal(config)
if err != nil {
    // handle error
}

// Now global functions will use your configuration
logger.Info("Application started with custom global logger")
```

### Structured Logging

The logger supports structured logging with key-value pairs:

```go
// Add structured context to logs
logger.Info("User logged in", 
    "user_id", 123, 
    "username", "johnsmith", 
    "login_source", "web",
)
```

### Named Loggers

You can create named loggers for different components:

```go
// Create a named logger
authLogger := logger.Named("auth")
authLogger.Info("User authenticated", "user_id", userId)

// Or with the custom logger
httpLogger := log.Named("http")
httpLogger.Info("Request processed", "method", "GET", "path", "/api/users")
```

### With Context

You can add persistent context to loggers:

```go
// Create a logger with context
userLogger := logger.With("user_id", userId, "session_id", sessionId)

// These fields will be included in every log message
userLogger.Info("Profile updated")
userLogger.Info("Settings changed", "setting", "theme", "value", "dark")
```

### Log Rotation

File rotation is supported using the lumberjack package:

```go
// Create basic configuration
config := logger.Config{
    Level:        "info",
    JSON:         false,
    Development:  false,
    FilePath:     "app.log",
    DisableColor: true,
}

// Create rotation config
rotationConfig := logger.DefaultRotationConfig()

// Create a logger with rotation in a single call
log, err := logger.NewLogger(config, rotationConfig)
if err != nil {
    // handle error
}
```

## Configuration Options

- **Level**: Sets the minimum log level ("debug", "info", "warn", "error", "dpanic", "panic", "fatal")
- **JSON**: When true, outputs structured JSON logs; when false, outputs human-readable logs
- **Development**: When true, uses development mode with debug level as default and more verbose stack traces
- **FilePath**: Path to the log file; if empty, logs go to stdout
- **DisableColor**: Disables colored output for console logging

## Testing

The logger package includes comprehensive test coverage and utilities to make testing with logs easier.

### Test Helpers

The package provides helper functions for testing:

- `CreateTestCore`: Creates a logger core that writes to a test buffer
- `NewLoggerWithCore`: Creates a logger with a custom core for testing

### Testing Your Code with the Logger

When writing tests for code that uses this logger:

```go
// Create a buffer to capture logs
buf := &bytes.Buffer{}

// Create a test logger configuration
config := logger.Config{
    Level:        "debug",
    JSON:         true, // JSON format is easier to parse in tests
    Development:  false,
    DisableColor: true,
}

// Create a logger core that writes to your buffer
core, _ := logger.CreateTestCore(config, buf)
testLogger := logger.NewLoggerWithCore(core, config)

// Use this logger in your tests
yourFunction(testLogger)

// Check log output
logOutput := buf.String()
if !strings.Contains(logOutput, "expected message") {
    t.Error("Log did not contain expected message")
}
```

### Testing with Mocked Filesystems

The tests demonstrate using `afero` for filesystem-independent testing:

```go
// Create in-memory filesystem
fs := afero.NewMemMapFs()

// Use it for testing file operations
// ...
```

### Running the Tests

Run the logger tests with:

```bash
go test github.com/tphakala/birdnet-go/internal/logger -v
```

## Good Practices

1. **Don't forget to Sync**:
   ```go
   defer logger.Sync() // Flushes buffers on program exit
   ```

2. **Use structured logging consistently**:
   ```go
   // Good
   logger.Info("User created", "id", user.ID, "name", user.Name)
   
   // Avoid
   logger.Info(fmt.Sprintf("User created: id=%d, name=%s", user.ID, user.Name))
   ```

3. **Use appropriate log levels**:
   - `Debug`: Detailed information, typically useful only for diagnosing problems
   - `Info`: Confirmation that things are working as expected
   - `Warn`: Indication that something unexpected happened, but the application can continue
   - `Error`: Error events that might still allow the application to continue running
   - `Fatal`: Severe error events that will lead the application to abort
   
4. **Create component-specific loggers**:
   ```go
   dbLogger := logger.Named("database")
   authLogger := logger.Named("auth")
   ```

5. **Use helper functions for common configurations**:
   ```go
   // For development
   config := logger.DefaultConfig()
   
   // For production
   config := logger.ProductionConfig()
   ```

## Implementation Notes

- The logger uses Uber's Zap logger internally for high performance
- The implementation supports basic cross-platform file management
- Log file rotation using the lumberjack package is provided
- Error handling follows Go conventions with `errXxx` naming pattern

## Future Enhancements

- Advanced log rotation strategies
- Multiple output destinations
- Log filtering capabilities
- Log aggregation support 