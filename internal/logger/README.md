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

## Application-wide Logging Best Practices

To maintain a unified logging approach across your application, follow these best practices:

### 1. Initialize Global Logger Early

Initialize the global logger at the application's entry point:

```go
func main() {
    // Configure and initialize the global logger early
    config := logger.Config{
        Level:        "info",
        JSON:         false,
        Development:  false,
        FilePath:     "app.log", // Empty for console only
        DisableColor: false,
    }

    if err := logger.InitGlobal(config); err != nil {
        log.Fatalf("Failed to initialize logger: %v", err)
    }
    
    // Make sure to flush buffers on exit
    defer logger.Sync()
    
    // Create a named root logger for the application
    appLogger := logger.Named("myapp")
    
    // Pass this logger to major components
    server := NewServer(appLogger.Named("server"))
    
    // Rest of your application...
}
```

Here's a real-world example from our application's root command initialization:

```go
// From cmd/root.go
func initialize() error {
    // Initialize the global logger
    config := logger.Config{
        Level:         viper.GetString("log.level"),
        Development:   viper.GetBool("debug"),
        FilePath:      viper.GetString("log.path"),
        JSON:          viper.GetBool("log.json"),
        DisableColor:  viper.GetBool("log.disable_color"),
        DisableCaller: true,
    }

    if err := logger.InitGlobal(config); err != nil {
        return fmt.Errorf("error initializing logger: %w", err)
    }

    // Create a named root logger for the application
    appLogger := logger.Named("birdnet")
    appLogger.Info("Application starting", "version", viper.GetString("version"))

    // The appLogger can now be passed to subcomponents
    
    return nil
}
```

### 2. Use Dependency Injection for Component Loggers

Always use constructor injection to pass loggers to components:

```go
// Component constructor with logger injection
func NewComponent(parentLogger *logger.Logger) *Component {
    // If a nil logger is provided, fall back to the global logger
    // but always create a properly named logger for the component
    var componentLogger *logger.Logger
    if parentLogger == nil {
        componentLogger = logger.Named("component")
    } else {
        componentLogger = parentLogger.Named("component")
    }
    
    return &Component{
        logger: componentLogger,
    }
}
```

Here's a concrete example from our HTTP server component:

```go
// From internal/httpcontroller/server.go
func NewWithLogger(settings *conf.Settings, dataStore datastore.Interface, 
    birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData, 
    controlChan chan string, proc *processor.Processor, parentLogger *logger.Logger) *Server {
    
    // Create a server-specific logger
    var serverLogger *logger.Logger
    if parentLogger != nil {
        serverLogger = parentLogger.Named("server")
    } else {
        // Fall back to global logger with proper naming if no parent logger provided
        serverLogger = logger.Named("server")
    }
    
    s := &Server{
        // Other fields...
        Logger: serverLogger,
    }
    
    // Initialize child components with child loggers
    s.Handlers = handlers.New(
        // Other parameters...
        nil,  // The handler's logger will be set below
    )
    
    // Set our custom logger in the handlers
    s.Handlers.SetLogger(s.Logger.Named("handlers"))
    
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

Example from our API initialization:

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

### 5. Avoid Logger Fallbacks

Components should always have a properly initialized logger. Checking for nil loggers and falling back to the global logger is unnecessary and creates inconsistent logging patterns:

```go
// DON'T DO THIS:
func (c *Component) LogInfo(msg string, fields ...interface{}) {
    if c.logger != nil {
        c.logger.Info(msg, fields...)
    } else {
        // Fall back to global logger
        logger.Info(msg, fields...)
    }
}

// INSTEAD, ensure loggers are always properly initialized:
func NewComponent(logger *logger.Logger) *Component {
    // If no logger is provided, get a named logger from the global instance
    if logger == nil {
        logger = logger.Named("component")
    } else {
        // Still use the component name for consistency in hierarchical logging
        logger = logger.Named("component")
    }
    
    return &Component{
        logger: logger,
    }
}

// Then simply use the component's logger directly:
func (c *Component) DoSomething() {
    c.logger.Info("Operation started")
    // ...
    c.logger.Info("Operation completed")
}
```

Real-world example from our HTTP server initialization:

```go
// From internal/httpcontroller/server_logger.go
func (s *Server) initLogger() {
    // ...
    
    // Only initialize logger if not already set
    // This allows NewWithLogger to set a proper parent logger
    if s.Logger == nil {
        // Use the global logger with a component name instead of creating a new one
        // This ensures consistent logging behavior across the application
        s.Logger = logger.GetGlobal().Named("http")
        
        if s.Logger == nil {
            log.Fatal("Failed to get global logger")
        }
    }
    
    // ...
}
```

### 6. Use Component-Specific Helper Methods

Create component-specific logging methods to ensure consistent logging:

```go
func (s *Server) createRequestLogger(c echo.Context) *logger.Logger {
    requestID := c.Response().Header().Get("X-Request-ID")
    return s.logger.With(
        "request_id", requestID,
        "client_ip", c.RealIP(),
        "method", c.Request().Method,
        "path", c.Path(),
    )
}
```

### 7. Complete Example: Logger Propagation Through Components

Here's a complete example of propagating a logger through multiple components:

```go
// Application initialization
func main() {
    // Initialize global logger
    config := logger.DefaultConfig()
    if err := logger.InitGlobal(config); err != nil {
        log.Fatalf("Failed to initialize logger: %v", err)
    }
    defer logger.Sync()
    
    // Create the root application logger
    appLogger := logger.Named("app")
    
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
    if logger == nil {
        logger = logger.Named("server")
    }
    
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
    // Ensure there's always a valid logger
    if logger == nil {
        logger = logger.Named("api")
    } else {
        logger = logger.Named("api")
    }
    
    return &API{
        logger: logger,
    }
}
```

### 8. Migrating from Global Logger to Dependency Injection

When refactoring an existing application to use proper logger dependency injection:

1. **Start at the application entry point**:
   ```go
   // In your main or initialization function
   appLogger := logger.Named("app")
   ```

2. **Add logger parameters to constructors**:
   ```go
   // Before
   func NewComponent(config Config) *Component
   
   // After
   func NewComponent(config Config, logger *logger.Logger) *Component
   ```

3. **Maintain backward compatibility** by providing overloaded constructors:
   ```go
   // Legacy constructor
   func New(settings *conf.Settings, ...) *Server {
       // Create instance without explicit logger
       return NewWithLogger(settings, ..., nil)
   }
   
   // New constructor with logger parameter
   func NewWithLogger(settings *conf.Settings, ..., parentLogger *logger.Logger) *Server {
       // Create server with proper logger hierarchy
   }
   ```

4. **Gradually replace global logger usage** with component loggers:
   ```go
   // Before
   logger.Info("Operation completed", "duration", duration)
   
   // After
   s.logger.Info("Operation completed", "duration", duration)
   ```

5. **Update component initialization methods** to respect logger hierarchy:
   ```go
   // Example from our server's logger initialization
   func (s *Server) initLogger() {
       // Only initialize if not already provided by constructor
       if s.Logger == nil {
           s.Logger = logger.GetGlobal().Named("http")
       }
       
       // The rest of the method that uses s.Logger
       // ...
   }
   ```

This migration approach allows for incremental improvements without breaking existing code.

## Common Issues and Solutions

### 1. Logger is nil

**Problem**: When using a logger from a component, you get a nil pointer panic:
```
panic: runtime error: invalid memory address or nil pointer dereference
```

**Solution**: Always initialize a component's logger, even when a parent logger is not provided:

```go
func NewComponent(parentLogger *logger.Logger) *Component {
    var componentLogger *logger.Logger
    if parentLogger == nil {
        // Fall back to a named global logger
        componentLogger = logger.Named("component")
    } else {
        // Create a child of the parent logger
        componentLogger = parentLogger.Named("component")
    }
    
    return &Component{
        logger: componentLogger,
    }
}
```

### 2. Inconsistent Logger Names

**Problem**: Log messages have inconsistent component names or missing context.

**Solution**: Establish and follow a naming convention for loggers:

```go
// Main app logger
appLogger := logger.Named("app")

// Component loggers - use consistent naming patterns
serverLogger := appLogger.Named("server")
apiLogger := serverLogger.Named("api") // Results in "app.server.api"

// Alternative - use direct dot notation for clarity
databaseLogger := logger.Named("app.database")
```

### 3. Missing Component Hierarchy

**Problem**: Logs don't show clear component relationships, making it difficult to trace request flows.

**Solution**: Use a consistent hierarchical naming pattern and pass parent loggers to child components:

```go
// Setup:
appLogger := logger.Named("app")
serverLogger := appLogger.Named("server")

// Usage in a handler method:
func (s *Server) HandleRequest(req *Request) {
    // Create a request-specific logger
    reqLogger := s.logger.With(
        "request_id", req.ID,
        "method", req.Method,
        "path", req.Path,
    )
    
    // Pass this logger to any components handling this request
    result, err := s.processor.Process(req, reqLogger.Named("processor"))
    
    // Now all logs from the processor will have the request context
    // and will show "app.server.processor" as the component
}
```

### 4. Duplicate Log Messages

**Problem**: The same event is being logged multiple times, often with different formats.

**Solution**: Ensure each component only logs events it directly handles, and pass the logger to subcomponents:

```go
// Before - logging in multiple places
func (s *Server) ProcessRequest(req *Request) {
    s.logger.Info("Processing request", "id", req.ID)
    
    // Processor also logs the same thing
    result := s.processor.Process(req)
    
    s.logger.Info("Request processed", "id", req.ID)
    return result
}

// After - pass context-aware logger to subcomponents
func (s *Server) ProcessRequest(req *Request) {
    reqLogger := s.logger.With("request_id", req.ID)
    reqLogger.Info("Processing request")
    
    // Pass the logger with request context
    result := s.processor.ProcessWithLogger(req, reqLogger.Named("processor"))
    
    reqLogger.Info("Request processed")
    return result
}
```

### 5. Logging Structure Doesn't Match Component Structure

**Problem**: The logger naming hierarchy doesn't match the actual component hierarchy in the code.

**Solution**: Make your logger naming reflect your application's architectural structure:

```go
// Application structure:
// - App
//   - API Server
//     - Auth Service
//     - User Service
//   - Background Processor

// Logger hierarchy should match:
appLogger := logger.Named("app")
apiLogger := appLogger.Named("api")
authLogger := apiLogger.Named("auth")
userLogger := apiLogger.Named("user")
procLogger := appLogger.Named("processor")
```

### 6. Logger Fallback Anti-patterns

**Problem**: Components checking for nil loggers and falling back to different loggers.

**Solution**: Standardize logger initialization in constructors and initialization methods:

```go
// Anti-pattern
func (c *Component) LogError(err error) {
    if c.logger != nil {
        c.logger.Error("Error occurred", "error", err)
    } else if globalLogger != nil {
        globalLogger.Error("Component error", "error", err)
    } else {
        log.Printf("Error: %v", err)
    }
}

// Better approach
func NewComponent(cfg Config, parentLogger *logger.Logger) *Component {
    // Standard logger initialization pattern
    var componentLogger *logger.Logger
    if parentLogger != nil {
        componentLogger = parentLogger.Named("component")
    } else {
        componentLogger = logger.Named("component")
    }
    
    return &Component{
        config: cfg,
        logger: componentLogger,
    }
}

// Then simply use the component's logger directly
func (c *Component) LogError(err error) {
    c.logger.Error("Error occurred", "error", err)
}
```

By consistently following these patterns, your application will have cleaner, more maintainable logging that correctly reflects your application structure.

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
                logger.Sync() // Flush logs before exit
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