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

## Package Organization

The logger package is organized into several files for maintainability:

- **config.go** - Configuration structures and helpers
- **logger.go** - Core logger implementation
- **rotation.go** - Log rotation functionality
- **testing.go** - Testing utilities and helpers

## Basic Usage

### Import the package

```go
import "github.com/tphakala/birdnet-go/internal/logger"
```

### Create and use a logger

The package provides a simple way to create and use a logger:

```go
// Create a new logger with default configuration
config := logger.DefaultConfig()
log, err := logger.NewLogger(config)
if err != nil {
    // handle error
}

// Use the logger
log.Info("Application starting", "version", "1.0.0")
log.Debug("Debug information", "details", details)
log.Warn("Resource running low", "resource", "memory", "available", "10MB")
log.Error("Operation failed", "error", err)
log.Fatal("Cannot continue execution", "error", err) // Also calls os.Exit(1)
```

### Create a custom logger

You can create a custom configured logger:

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

### Share a logger across components

Create a root logger and share it across your application:

```go
config := logger.Config{
    Level:        "info",
    JSON:         true, // Enable JSON structured logging
    Development:  false,
    FilePath:     "app.log",
    DisableColor: true,
}

// Create a root logger
rootLogger, err := logger.NewLogger(config)
if err != nil {
    // handle error
}

// Pass it to components
component1 := NewComponent1(rootLogger.Named("component1"))
component2 := NewComponent2(rootLogger.Named("component2"))

// Components can use their loggers
// component1.DoSomething() will log with "component1" prefix
// component2.DoSomething() will log with "component2" prefix
```

### Structured Logging

The logger supports structured logging with key-value pairs:

```go
// Add structured context to logs
log.Info("User logged in", 
    "user_id", 123, 
    "username", "johnsmith", 
    "login_source", "web",
)
```

### Named Loggers

You can create named loggers for different components:

```go
// Create a named logger
authLogger := log.Named("auth")
authLogger.Info("User authenticated", "user_id", userId)

// You can create nested names
httpLogger := log.Named("http")
apiLogger := httpLogger.Named("api") // Will log as "http.api"
apiLogger.Info("Request processed", "method", "GET", "path", "/api/users")
```

### With Context

You can add persistent context to loggers:

```go
// Create a logger with context
userLogger := log.With("user_id", userId, "session_id", sessionId)

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
- **ForceJSONFile**: When true, forces JSON format for file output regardless of the JSON setting
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
   defer log.Sync() // Flushes buffers on program exit
   ```

2. **Use structured logging consistently**:
   ```go
   // Good
   log.Info("User created", "id", user.ID, "name", user.Name)
   
   // Avoid
   log.Info(fmt.Sprintf("User created: id=%d, name=%s", user.ID, user.Name))
   ```

3. **Use appropriate log levels**:
   - `Debug`: Detailed information, typically useful only for diagnosing problems
   - `Info`: Confirmation that things are working as expected
   - `Warn`: Indication that something unexpected happened, but the application can continue
   - `Error`: Error events that might still allow the application to continue running
   - `Fatal`: Severe error events that will lead the application to abort
   
4. **Create component-specific loggers**:
   ```go
   dbLogger := log.Named("database")
   authLogger := log.Named("auth")
   ```

5. **Use helper functions for common configurations**:
   ```go
   // For development
   config := logger.DefaultConfig()
   
   // For production
   config := logger.ProductionConfig()
   ```

## Sensitive Data Handling

The logger package includes built-in protection against accidental logging of sensitive information such as passwords, tokens, cookies, and API keys.

### How It Works

All logging methods (Debug, Info, Warn, Error, Fatal) automatically:

1. Scan log messages for common patterns of sensitive data and redact them
2. Examine field keys for sensitive keywords (like "password", "token", etc.) and redact their values
3. Replace sensitive information with "[REDACTED]" to maintain log readability

### Example

```go
// Original code
log.Info("User authenticated", 
    "username", "john.doe",
    "token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "session_id", "abcdef123456")

// What gets logged
// {"level":"info","ts":1632512345.6789,"msg":"User authenticated","username":"john.doe","token":"[REDACTED]","session_id":"[REDACTED]"}
```

### Recognized Sensitive Data

The logger automatically recognizes and redacts:

- Authentication tokens (Bearer tokens, JWTs)
- API keys
- Passwords
- Cookie values
- CSRF tokens
- Session IDs
- Any field with names containing words like "secret", "key", "auth", etc.

### Custom Handling

If you need to customize sensitive data handling for specific use cases, you can use the exposed functions directly:

```go
// Redact sensitive data from a string
safeMessage := logger.RedactSensitiveData(potentiallyUnsafeMessage)

// Redact sensitive values from key-value pairs
safeFields := logger.RedactSensitiveFields(fields)
```

### Security Considerations

While the automatic redaction provides a good baseline of protection, it's still recommended to:

1. Be mindful about what you log, especially in production environments
2. Never design your code to intentionally log sensitive data
3. Consider using dedicated security tools for comprehensive security analysis
4. Regularly audit your logs to ensure sensitive data is not being inadvertently exposed

## Application-wide Logging Best Practices

To maintain a unified logging approach across your application, follow these best practices:

### 1. Initialize the Logger at the Component Level

Initialize logger instances in the components that need them and pass them to child components:

```go
// In a component package (e.g., realtime.go)
func SomeFunction(settings *conf.Settings) error {
    // Create a logger instance with configuration from settings
    config := logger.Config{
        Level:         viper.GetString("log.level"),
        Development:   settings.Debug,
        FilePath:      viper.GetString("log.path"),
        JSON:          viper.GetBool("log.json"),
        DisableColor:  viper.GetBool("log.disable_color"),
        DisableCaller: true,
    }

    log, err := logger.NewLogger(config)
    if err != nil {
        return fmt.Errorf("error initializing logger: %w", err)
    }
    
    // Create a root application logger that will be the parent for all component loggers
    appLogger := log.Named("app")
    appLogger.Info("Application starting", "version", viper.GetString("version"))
    
    // Create a component-specific logger as a child of the app logger
    componentLogger := appLogger.Named("component")
    
    // Use the logger...
    componentLogger.Info("Component initialized")
    
    // Rest of your function...
}
```

Here's a real-world example from the application's real-time analysis module:

```go
// From internal/analysis/realtime.go
func RealtimeAnalysis(settings *conf.Settings, notificationChan chan handlers.Notification) error {
    // Initialize a new logger instance with dual-format logging
    config := logger.Config{
        Level:         viper.GetString("log.level"),
        Development:   settings.Debug,
        FilePath:      viper.GetString("log.path"),
        JSON:          false,                   // Use human-readable format for console
        ForceJSONFile: true,                    // Force JSON format for file output
        DisableColor:  viper.GetBool("log.disable_color"),
        DisableCaller: true,
    }

    // Create a new logger instance, this will be passed to all components
    logger, err := logger.NewLogger(config)
    if err != nil {
        return fmt.Errorf("error initializing logger: %w", err)
    }

    // Create a component logger for the realtime analyzer
    coreLogger := logger.Named("core")
    coreLogger.Info("Starting real-time analysis")
    
    // ... rest of the function ...
}
```

With this configuration:
1. Console output is human-readable for easier debugging during development
2. File output is structured JSON for better integration with log analysis tools like Grafana Loki, ELK stack, etc.

### 2. Use Dependency Injection for Component Loggers

Always use constructor injection to pass loggers to components:

```go
// Component constructor with logger injection
func NewComponent(parentLogger *logger.Logger) *Component {
    // Create a properly named logger for the component
    componentLogger := parentLogger.Named("component")
    
    return &Component{
        logger: componentLogger,
    }
}
```

Here's a concrete example from the HTTP server component:

```go
// From internal/httpcontroller/server.go
func NewWithLogger(settings *conf.Settings, dataStore datastore.Interface, 
    birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData, 
    controlChan chan string, proc *processor.Processor, parentLogger *logger.Logger) *Server {
    
    // Create a server-specific logger
    serverLogger := parentLogger.Named("server")
    
    s := &Server{
        // Other fields...
        Logger: serverLogger,
    }
    
    // Initialize child components with child loggers
    s.Handlers = handlers.New(
        // Other parameters...
        serverLogger.Named("handlers"),  // Pass a child logger to handlers
    )
    
    // Rest of initialization...
    return s
}
```

### 3. Create Hierarchical Loggers

Use descriptive namespace hierarchies to organize logs:

```go
// In API package
apiLogger := parentLogger.Named("api")
v1Logger := apiLogger.Named("v1")
authLogger := v1Logger.Named("auth")

// Alternatively, using dot notation
authLogger := parentLogger.Named("api.v1.auth")
```

Example from API initialization:

```go
// From internal/httpcontroller/server.go
func (s *Server) initializeServer() {
    // ...
    
    // Initialize the JSON API v2 with a child logger
    s.APIV2 = api.InitializeAPI(
        s.Echo,
        s.DS,
        s.Settings,
        s.BirdImageCache,
        s.SunCalc,
        s.controlChan,
        s.Logger.Named("api.v2"), // Creates a properly named child logger
    )
    
    // ...
}
```

### 4. Add Context to Loggers

Enrich loggers with context that applies to multiple log entries:

```go
// Create a request-specific logger
requestLogger := componentLogger.With(
    "request_id", requestID,
    "client_ip", clientIP,
    "method", request.Method,
)

// All messages logged with this logger will include these fields
requestLogger.Info("Request received")
requestLogger.Info("Processing request")
requestLogger.Info("Request completed", "duration_ms", duration)
```

### 5. Complete Example: Logger Propagation Through Components

Here's a complete example of propagating a logger through multiple components:

```go
// Application initialization
func main() {
    // Create a root logger
    config := logger.DefaultConfig()
    rootLogger, err := logger.NewLogger(config)
    if err != nil {
        log.Fatalf("Failed to initialize logger: %v", err)
    }
    defer rootLogger.Sync()
    
    // Create the named application logger
    appLogger := rootLogger.Named("app")
    
    // Create a server component with the app logger
    server := NewServer(appLogger.Named("server"))
    
    // The server can then pass component-specific loggers to its subcomponents
    server.Start()
}

// Server component
type Server struct {
    logger *logger.Logger
    api    *API
}

func NewServer(logger *logger.Logger) *Server {
    // Create API with a sub-logger
    api := NewAPI(logger.Named("api"))
    
    return &Server{
        logger: logger,
        api:    api,
    }
}

// API component
type API struct {
    logger *logger.Logger
}

func NewAPI(logger *logger.Logger) *API {
    return &API{
        logger: logger,
    }
}
```

### 6. Handling Nil Loggers

To ensure robustness, handle cases where a nil logger might be passed:

```go
func NewComponent(logger *logger.Logger) *Component {
    // Create a new logger if none is provided
    if logger == nil {
        // Create a new default logger
        config := logger.DefaultConfig()
        newLogger, err := logger.NewLogger(config)
        if err != nil {
            // Fall back to standard log in the worst case
            log.Println("Warning: Failed to create logger, using standard log")
            // You might want to create a wrapper around standard log that
            // implements the same interface as your logger
        } else {
            logger = newLogger.Named("component")
        }
    } else {
        logger = logger.Named("component")
    }
    
    return &Component{
        logger: logger,
    }
}
```

## Cross-Platform Considerations

The logger package is designed to work consistently across Linux, macOS, and Windows platforms. However, there are some platform-specific considerations to be aware of:

### File Paths

When specifying log file paths, use path separators that work on all platforms:

```go
// Don't use platform-specific separators
config.FilePath = "logs\\app.log" // Windows-only

// Instead, use filepath.Join for cross-platform compatibility
import "path/filepath"

config.FilePath = filepath.Join("logs", "app.log") // Works on all platforms
```

### File Permissions

File permissions work differently across platforms:

```go
// When implementing custom log file handling:
// Linux/macOS
file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

// For cross-platform compatibility, consider using constants:
const filePerms = 0644
```

### Terminal Colors

Terminal color support varies by platform and terminal emulator:

```go
// Consider offering a way to detect color support or allowing users to explicitly disable colors
config := logger.Config{
    DisableColor: runtime.GOOS == "windows" && !isColorTerminal(),
}
```

### File Locking

If implementing custom file handling, be aware that file locking mechanisms differ:

```go
// Use platform-specific file locking or a cross-platform library 
// if implementing custom log rotation or concurrent file access
```

### Line Endings

Log file line endings may be inconsistent across platforms:

```go
// When parsing log files, consider normalizing line endings
// Windows: \r\n, Unix/macOS: \n
```

### Environment Variables

Different platforms may have different environment variable conventions:

```go
// For configuration through environment variables, use uppercase with underscores
// LOG_LEVEL, LOG_FILE_PATH, etc.
```

### Process Signals

When handling signals for log rotation or graceful shutdown:

```go
// Use a cross-platform approach for handling signals
import (
    "os"
    "os/signal"
    "syscall"
)

func setupSignalHandler() {
    c := make(chan os.Signal, 1)
    
    // Different platforms support different signals
    if runtime.GOOS == "windows" {
        signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    } else {
        signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
    }
    
    go func() {
        for sig := range c {
            if sig == syscall.SIGHUP && runtime.GOOS != "windows" {
                // Rotate logs on SIGHUP (Unix/macOS only)
                // ...
            } else {
                // Handle graceful shutdown
                log.Sync() // Flush logs before exit
                os.Exit(0)
            }
        }
    }()
}
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

### Dual-Format Logging

You can configure the logger to output human-readable logs to the console while simultaneously writing structured JSON logs to a file:

```go
// Use the helper function for dual-format logging
config := logger.DualLogConfig("/path/to/app.log")
log, err := logger.NewLogger(config)
if err != nil {
    // handle error
}

// Or configure manually
config := logger.Config{
    Level:         "info",
    JSON:          false,      // Human-readable for console
    ForceJSONFile: true,       // Force JSON for file
    Development:   false,
    FilePath:      "/path/to/app.log",
    DisableColor:  false,
    DisableCaller: true,
}
log, err := logger.NewLogger(config)
if err != nil {
    // handle error
}

// Now you have:
// 1. Human-readable logs in the console:
//    2025-03-02T21:32:52  INFO  core   System details   {"os": "linux", "platform": "debian"}
//
// 2. Structured JSON logs in the file:
//    {"timestamp":"2025-03-02T21:15:07Z","level":"INFO","logger":"core.server.http.request","message":"Processed HTTP request","http":{"status":200,"method":"GET","uri":"/api/v1/top-birds?date=2025-03-02","remote_ip":"192.168.4.3","latency_ms":17.738517,"request_id":"88a8ddbe","resp_size":37461}}
```

This approach gives you the best of both worlds:
- Developer-friendly, readable logs in the console
- Machine-parsable, structured logs in files for log analysis tools
- No duplication of log infrastructure 

#### Real-World Example from BirdNET-Go

Here's how dual-format logging is used in the real-time analysis module:

```go
// From internal/analysis/realtime.go
func RealtimeAnalysis(settings *conf.Settings, notificationChan chan handlers.Notification) error {
    // Initialize a new logger instance with dual-format logging
    config := logger.Config{
        Level:         viper.GetString("log.level"),
        Development:   settings.Debug,
        FilePath:      viper.GetString("log.path"),
        JSON:          false,                   // Use human-readable format for console
        ForceJSONFile: true,                    // Force JSON format for file output
        DisableColor:  viper.GetBool("log.disable_color"),
        DisableCaller: true,
    }

    // Create a new logger instance, this will be passed to all components
    logger, err := logger.NewLogger(config)
    if err != nil {
        return fmt.Errorf("error initializing logger: %w", err)
    }

    // Create a component logger for the realtime analyzer
    coreLogger := logger.Named("core")
    coreLogger.Info("Starting real-time analysis")
    
    // ... rest of the function ...
}
```

With this configuration:
1. Console output is human-readable for easier debugging during development
2. File output is structured JSON for better integration with log analysis tools like Grafana Loki, ELK stack, etc.

// ... rest of content ... 