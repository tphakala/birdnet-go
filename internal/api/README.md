# BirdNet-Go API Package

This package implements the HTTP-based RESTful API for the BirdNET-Go application, providing endpoints for bird detection data management, analytics, system control, and more.

## Package Structure

```text
internal/api/
└── v2/
    ├── analytics.go       - Analytics and statistics endpoints
    ├── analytics_test.go  - Tests for analytics endpoints
    ├── api.go             - Main API controller and route initialization
    ├── api_test.go        - Tests for main API functionality
    ├── auth.go            - Authentication endpoints and middleware
    ├── auth_test.go       - Tests for authentication endpoints
    ├── control.go         - System control actions (restart, reload model)
    ├── detections.go      - Bird detection data endpoints
    ├── integration.go     - External integration framework
    ├── integrations.go    - External service integrations
    ├── media.go           - Media (images, audio) management
    ├── middleware.go      - Custom middleware functions
    ├── settings.go        - Application settings management
    ├── streams.go         - Real-time data streaming
    ├── system.go          - System information and monitoring
    └── weather.go         - Weather data related to detections
```

## API Controller

The API is organized around a central `Controller` struct in `v2/api.go` that manages all endpoints and dependencies. It's initialized with:

- Echo web framework instance
- Datastore interface for database operations
- Application settings
- Bird image cache for species images
- Sun calculator for daylight information
- Control channel for system commands
- Logger for API operations

## API Versions

Currently, the package implements version 2 (`v2`) of the API with all endpoints under the `/api/v2` prefix.

## Authentication

The API implements authentication via:

- Login/logout functionality
- Session-based authentication
- Auth middleware for protected endpoints
- Bearer token support for programmatic API access

Protected endpoints require authentication, while some endpoints like health checks and basic detection queries are publicly accessible.

## Key Features

### Bird Detection Management

- List, retrieve, and search detections
- Manage detection verification status
- Add comments to detections
- Lock/unlock detections to prevent modifications
- Ignore specific species

### Analytics

- Statistics on detections by species, time, and confidence
- Trends and patterns in detection data

### System Control

- Restart analysis processes
- Reload detection models
- Rebuild detection filters

### Settings Management

- View and update application configuration
- Manage analysis parameters
- Configure external integrations

### Media Access

- Retrieve bird images by scientific name
- Access detection audio samples
- Generate and view spectrograms for detections

### Weather Integration

- Weather conditions for detections
- Daylight information

### Real-time Data Streaming

- WebSocket connections for live detection updates
- Event-based notification system

## API Design Principles

### Route Organization

The API follows a consistent pattern for organizing routes:

1. **Group-Based Structure**: Routes are organized by feature into logical groups:
   ```go
   analyticsGroup := c.Group.Group("/analytics")
   speciesGroup := analyticsGroup.Group("/species")
   ```

2. **Feature-Based Modules**: Each feature has its own file (e.g., `analytics.go`, `detections.go`) containing:
   - Route initialization function (e.g., `initAnalyticsRoutes`)
   - Handler methods
   - Feature-specific data types and utilities

3. **Access Control**: Each route group applies appropriate middleware:
   ```go
   // Public routes
   c.Group.GET("/detections", c.GetDetections) 
   
   // Protected routes
   detectionGroup := c.Group.Group("/detections", c.AuthMiddleware)
   ```

### Media Access API Endpoints

The API provides several endpoints for accessing media related to bird detections:

1. **Species Images**:
   - `GET /api/v2/media/species-image?name={scientificName}` - Retrieves an image for a bird species using its scientific name
   - Redirects to the appropriate image from configured providers (e.g., AviCommons)
   - Falls back to a placeholder if no image is available

2. **Audio Clips**:
   - `GET /api/v2/audio/{id}` - Retrieves the audio clip for a detection by ID
   - `GET /api/v2/media/audio/{filename}` - Retrieves an audio clip by filename (legacy endpoint)
   - `GET /api/v2/media/audio?id={id}` - Convenience endpoint that redirects to ID-based endpoint

3. **Spectrograms**:
   - `GET /api/v2/spectrogram/{id}?width={width}` - Generates a spectrogram for a detection by ID
   - `GET /api/v2/media/spectrogram/{filename}?width={width}` - Generates a spectrogram by filename (legacy endpoint)
   - The width parameter is optional and defaults to 800px

All media endpoints use secure file access through the SecureFS implementation which prevents path traversal attacks.

### Middleware Implementation

The API uses a combination of standard Echo middleware and custom middleware for specific functionality:

1. **Standard Middleware**:
   - Logger - For request logging
   - Recover - For panic recovery
   - CORS - For cross-origin resource sharing

2. **Custom Middleware**:
   - AuthMiddleware - Handles both session-based and token-based authentication
   - Rate limiting for public endpoints

Middleware is defined in the dedicated `middleware.go` file to maintain clean separation of concerns.

### Handler Implementation

Handlers follow a consistent pattern:

1. **Method Signature**: Each handler is a method on the Controller struct:
   ```go
   func (c *Controller) GetDetection(ctx echo.Context) error {
   ```

2. **Parameter Validation**: Always validate and sanitize input parameters:
   ```go
   if id <= 0 {
       return c.HandleError(ctx, err, "Invalid detection ID", http.StatusBadRequest)
   }
   ```

3. **Error Handling**: Use the standardized error handler:
   ```go
   return c.HandleError(ctx, err, "Failed to fetch detection", http.StatusInternalServerError)
   ```

4. **Response Structure**: Return JSON responses with consistent structures:
   ```go
   return ctx.JSON(http.StatusOK, detection)
   ```

5. **Logging**: Handlers utilize structured logging via `c.apiLogger`:
   - Log entry points with `Info` level, including relevant request parameters (e.g., date, species, ID) and context (IP address, request path).
   - Log successful outcomes with `Info` level, summarizing results (e.g., number of records fetched, action completed).
   - Log validation errors or expected issues (e.g., resource not found, invalid parameters) with `Warn` level.
   - Log unexpected errors (e.g., database failures, internal processing errors) with `Error` level, including the underlying error message and relevant context.
   - Use `c.Debug` for verbose debugging information during development.
   - Example:
     ```go
     if c.apiLogger != nil {
         c.apiLogger.Info("Handling request for detection", "detection_id", id, "ip", ctx.RealIP(), "path", ctx.Request().URL.Path)
     }
     // ... processing ...
     if err != nil {
         if c.apiLogger != nil {
             c.apiLogger.Error("Failed to fetch detection from datastore", "detection_id", id, "error", err.Error(), "ip", ctx.RealIP(), "path", ctx.Request().URL.Path)
         }
         return c.HandleError(ctx, err, "Database error", http.StatusInternalServerError)
     }
     if c.apiLogger != nil {
         c.apiLogger.Info("Successfully retrieved detection", "detection_id", id, "ip", ctx.RealIP(), "path", ctx.Request().URL.Path)
     }
     ```

### Settings Management

The API includes comprehensive endpoints for managing application settings:

1. **Settings Routes**:
   - `GET /api/v2/settings` - Retrieves all application settings
   - `GET /api/v2/settings/:section` - Retrieves settings for a specific section (e.g., birdnet, webserver)
   - `PUT /api/v2/settings` - Updates multiple settings sections with complete replacement
   - `PATCH /api/v2/settings/:section` - Updates a specific settings section with partial replacement

2. **Concurrency Safety**:
   - All settings operations are protected by a read-write mutex
   - Read operations acquire a read lock, allowing concurrent reads
   - Write operations acquire a write lock, ensuring exclusive access
   - This prevents race conditions when multiple clients update settings simultaneously

3. **Dynamic Field Updates**:
   - Settings updates use reflection to safely update only allowed fields
   - Updates can be applied at any nesting level in the settings structure
   - The allowed fields map defines which settings can be modified via the API

4. **Asynchronous Reconfigurations**:
   - When important settings change, reconfigurations are triggered asynchronously
   - This prevents long-running operations from blocking API responses
   - A small delay is added between configuration actions to avoid overwhelming the system

### Best Practices for API Development

1. **Route Naming**:
   - Use nouns for resources (e.g., `/detections`, `/analytics`)
   - Use HTTP methods to indicate actions (GET, POST, PUT, DELETE)
   - Maintain consistency in naming patterns

2. **Handler Organization**:
   - Each handler should have a clear single responsibility
   - Document the endpoint path in a comment before each handler
   - Group related functionality in the same file
   - Add function explanation comments describing purpose and parameters

3. **Middleware Application**:
   - Apply authentication middleware at the group level
   - Use route-specific middleware only when needed
   - Consider performance implications of middleware order

4. **Response Consistency**:
   - Use standardized response formats across all handlers
   - Include proper HTTP status codes
   - Return appropriate error messages with helpful context
   - Include correlation IDs for error tracking

## Error Handling

The API provides standardized error responses:

```json
{
  "error": "Error type or source",
  "message": "Human-readable error message",
  "code": 400,
  "correlation_id": "ab12xy89"
}
```

The correlation ID allows tracking specific errors across logs and systems.

## Developer Usage

### Dependencies

The API package requires:

1. Echo web framework
2. Access to a datastore implementation
3. Application configuration
4. Other internal services like image provider

### Initialization

To initialize the API in your application:

```go
import (
    "github.com/labstack/echo/v4"
    "github.com/tphakala/birdnet-go/internal/api"
    "github.com/tphakala/birdnet-go/internal/conf"
    "github.com/tphakala/birdnet-go/internal/datastore"
    "github.com/tphakala/birdnet-go/internal/imageprovider"
    "github.com/tphakala/birdnet-go/internal/suncalc"
)

func setupAPI() {
    // Initialize echo
    e := echo.New()
    
    // Get dependencies
    ds := datastore.NewSQLiteDatastore("path/to/database")
    settings := conf.LoadSettings("path/to/config")
    imageCache := imageprovider.NewBirdImageCache()
    sunCalc := suncalc.New(settings.Location.Latitude, settings.Location.Longitude)
    controlChan := make(chan string)
    
    // Create API controller
    apiController := api.New(e, ds, settings, imageCache, sunCalc, controlChan, nil)
    
    // Start the server
    e.Start(":8080")
}
```

### Extending the API

To add new endpoints:

1. Create a new file in the `v2` directory for your feature
2. Add appropriate route initialization in the relevant `init*Routes` function
3. Implement handler functions as methods on the `Controller` struct
4. Update this README to document your new functionality

### Cross-Platform Considerations

The API is designed to be compatible with Linux, macOS, and Windows. File paths and system operations are handled in a platform-independent way.

## Security Best Practices and Common Pitfalls

When working with the API code, be mindful of these important considerations:

### Security Best Practices

#### Authentication and Authorization

- Always use the `AuthMiddleware` for protected routes
- Validate tokens properly with appropriate expiration and refresh mechanics
- Use proper session management for browser-based access
- Implement fine-grained authorization checks within handlers

#### Path Traversal Protection

- Always sanitize and validate file paths in requests
- Use filepath.Clean() to normalize paths
- Never concatenate user input directly into file paths
- Verify paths don't escape intended directories using path validation
- Example:
  ```go
  // INCORRECT
  file := fmt.Sprintf("/some/dir/%s", userInput)
  
  // CORRECT
  if strings.Contains(userInput, "..") || strings.Contains(userInput, "/") {
      return errors.New("invalid filename")
  }
  file := filepath.Join("/some/dir", filepath.Base(userInput))
  ```

#### Protecting Public Heavy API Routes

- Implement rate limiting for publicly accessible endpoints, especially analytics and data-heavy routes
- Consider pagination for large data sets to prevent resource exhaustion
- Add query complexity analysis to prevent expensive operations
- Track and log unusual patterns that could indicate abuse
- Implement caching strategies for frequently requested data
- Consider implementing token-based access with usage quotas even for public endpoints
- Example:
  ```go
  // INCORRECT
  analyticsGroup.GET("/species-summary", c.GetSpeciesSummary)
  
  // CORRECT
  analyticsGroup.GET("/species-summary", c.GetSpeciesSummary, middleware.RateLimiter(middleware.NewRateLimiterMemoryStore(20)))
  
  // In handler implementation
  func (c *Controller) GetSpeciesSummary(ctx echo.Context) error {
      // Parse request parameters
      limit, err := strconv.Atoi(ctx.QueryParam("limit"))
      if err != nil || limit <= 0 {
          limit = 100 // Default limit
      }
      if limit > 1000 {
          limit = 1000 // Cap maximum to prevent abuse
      }
      
      // Use caching when appropriate
      cacheKey := fmt.Sprintf("species-summary-%d", limit)
      if cached, found := c.cache.Get(cacheKey); found {
          return ctx.JSON(http.StatusOK, cached)
      }
      
      // Rest of implementation
      // ...
  }
  ```

#### Sensitive Data Handling

- Never log sensitive information such as passwords, tokens, or PII
- Mask sensitive data in error messages and logs
- Use dedicated logging middleware to automatically redact sensitive fields
- Example:
  ```go
  // INCORRECT
  c.logger.Printf("Login attempt with credentials: %s:%s", username, password)
  
  // CORRECT
  c.logger.Printf("Login attempt for user: %s", username)
  ```

### Error Handling

#### Always Check I/O Operation Errors

- Check and handle all errors from I/O operations like SetWriteDeadline, WriteMessage, Write
- Log errors with appropriate context to help with debugging
- Consider adding recovery mechanisms for failed I/O operations
- Example:
  ```go
  // INCORRECT
  conn.SetWriteDeadline(time.Now().Add(writeWait))
  conn.WriteMessage(messageType, payload)
  
  // CORRECT
  if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
      c.logger.Printf("Failed to set write deadline: %v", err)
      return err
  }
  if err := conn.WriteMessage(messageType, payload); err != nil {
      c.logger.Printf("Failed to write message: %v", err)
      return err
  }
  ```

#### Check Connection Deadline Errors

- Always check errors from SetReadDeadline and SetWriteDeadline
- Handle timeouts properly to prevent resource leaks
- Consider implementing retry mechanisms for transient errors
- Example:
  ```go
  // INCORRECT
  conn.SetReadDeadline(time.Now().Add(pongWait))
  
  // CORRECT
  if err := conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
      c.logger.Printf("Failed to set read deadline: %v", err)
      return err
  }
  ```

#### Use errors.As for Type Assertions

- When checking for specific error types, use errors.As() to handle wrapped errors
- This ensures compatibility with error wrapping patterns
- Example:
  ```go
  // INCORRECT
  if sqlErr, ok := err.(*sqlite3.Error); ok && sqlErr.Code == sqlite3.ErrConstraint {
      // Handle constraint violation
  }
  
  // CORRECT
  var sqlErr *sqlite3.Error
  if errors.As(err, &sqlErr) && sqlErr.Code == sqlite3.ErrConstraint {
      // Handle constraint violation
  }
  ```

### Concurrency Safety

- Use appropriate synchronization primitives (mutexes, channels) for shared resources
- Consider using sync/atomic for simple counter operations
- Design handlers to be stateless where possible to avoid concurrency issues
- Identify and protect critical sections in your code
- Test under high concurrency to identify race conditions
- Use `go build -race` during development to detect data races
- Example:
  ```go
  // INCORRECT
  count++
  
  // CORRECT - Using mutex
  mu.Lock()
  count++
  mu.Unlock()
  
  // CORRECT - Using atomic
  atomic.AddInt64(&count, 1)
  ```

### Coding Style and Performance

#### API Version Management

- Keep version-specific code within its own package (e.g., v2)
- Create a clean abstraction between version implementations
- Consider compatibility for clients migrating between versions

#### Parameter Passing

- Avoid copying heavy parameters; use pointers for large structs
- Consider the cost of copies when designing function signatures
- Example:
  ```go
  // INCORRECT - Copies the entire large struct
  func ProcessDetection(detection LargeDetectionStruct) error {
      // ...
  }
  
  // CORRECT - Passes a pointer, avoiding a copy
  func ProcessDetection(detection *LargeDetectionStruct) error {
      // ...
  }
  ```

#### Control Flow

- Prefer switch statements over complex if/else trees for better readability and performance
- Use switch with no expression for boolean logic chains
- Example:
  ```go
  // INCORRECT - Complex if/else tree
  if status == "pending" {
      // Handle pending
  } else if status == "processing" {
      // Handle processing
  } else if status == "completed" {
      // Handle completed
  } else if status == "failed" {
      // Handle failed
  } else {
      // Handle unknown
  }
  
  // CORRECT - Clean switch statement
  switch status {
  case "pending":
      // Handle pending
  case "processing":
      // Handle processing
  case "completed":
      // Handle completed
  case "failed":
      // Handle failed
  default:
      // Handle unknown
  }
  
  // CORRECT - Switch with no expression for boolean logic
  switch {
  case err != nil && isTemporary(err):
      // Handle temporary error
  case err != nil:
      // Handle permanent error
  case result == nil:
      // Handle missing result
  default:
      // Handle success
  }
  ```

## Testing

Each module has corresponding test files (`*_test.go`) for unit testing. Run tests with:

```bash
go test -v ./internal/api/v2/...
```

### Testing Best Practices

1. **Mock Dependencies**: Use mock implementations of datastore and other dependencies
2. **Test Both Success and Failure Paths**: Ensure error handling works correctly
3. **Validate Response Structures**: Ensure JSON responses match expected formats
4. **Test Middleware Behavior**: Verify auth middleware correctly allows/denies requests
5. **Use Table-Driven Tests**: For testing multiple input scenarios

## Security Considerations

- All sensitive endpoints require authentication
- Use HTTPS in production
- The API implements CORS middleware
- Authentication is required for system control operations
- Properly manage API tokens with appropriate expiration policies
- Implement rate limiting for public endpoints 