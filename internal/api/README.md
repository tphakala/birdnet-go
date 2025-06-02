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
    ├── auth/              - Authentication package
    │   ├── adapter.go     - Adapter for security package
    │   ├── middleware.go  - Authentication middleware  
    │   └── service.go     - Authentication service interface
    ├── auth.go            - Authentication endpoints and handlers
    ├── auth_test.go       - Tests for authentication endpoints
    ├── control.go         - System control actions (restart, reload model)
    ├── detections.go      - Bird detection data endpoints
    ├── integration.go     - External integration framework
    ├── integrations.go    - External service integrations
    ├── media.go           - Media (images, audio) management
    ├── range.go           - Range filter management and testing
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

The API implements a comprehensive, service-based authentication system managed by the `internal/api/v2/auth` sub-package. This package decouples authentication logic from API handlers and supports multiple authentication methods.

Key components of the `auth` package:

-   **`Service` Interface (`auth/service.go`)**: Defines the contract for authentication operations (checking access, validating tokens, basic auth, logout, etc.) and standard sentinel errors.
-   **`AuthMethod` Enum (`auth/service.go`)**: Represents different authentication methods (`Token`, `BrowserSession`, `BasicAuth`, `LocalSubnet`, `None`). Uses `go generate` with `stringer`.
-   **`SecurityAdapter` (`auth/adapter.go`)**: Implements the `Service` interface, adapting the `internal/security` package's `OAuth2Server` for core logic like session checks, token validation, and basic auth credential verification.
-   **`Middleware` (`auth/middleware.go`)**: An Echo middleware that uses the `AuthService` to enforce authentication. It checks if auth is required, attempts token and session authentication, sets context values (`isAuthenticated`, `username`, `authMethod`) on success, and handles unauthenticated requests appropriately (redirect for browsers, 401 for API clients).

**Authentication Flow:**

1.  The `Middleware` intercepts requests.
2.  It checks `AuthService.IsAuthRequired`. If not required (e.g., local subnet), proceeds with `AuthMethodNone`.
3.  If required, it checks for a `Bearer` token via `AuthService.ValidateToken`.
4.  If no valid token, it checks for a session via `AuthService.CheckAccess`.
5.  On success (token or session), it sets context and proceeds.
6.  On failure, it redirects browsers or returns a 401 error for API clients.

Protected endpoints use this auth middleware. The system handles browser clients (redirecting to login) and API clients (returning 401 JSON errors) appropriately.

### Authentication Service Interface (Deprecated - See `auth/service.go`)

The authentication service interface provides these key operations:

```go
// Service defines the interface for API service implementations.
type Service interface {
	// RegisterRoutes registers all API routes with the Echo group.
	RegisterRoutes(group *echo.Group)

	// CheckAccess validates if a request has access.
	// Returns nil on success, or an error on failure.
	CheckAccess(c echo.Context) error

	// IsAuthRequired checks if authentication is needed.
	IsAuthRequired(c echo.Context) bool

	// GetUsername retrieves the authenticated username.
	GetUsername(c echo.Context) string

	// GetAuthMethod returns the authentication method used.
	GetAuthMethod(c echo.Context) auth.AuthMethod // Use the enum type

	// ValidateToken checks if a bearer token is valid.
	// Returns nil on success, or an error on failure.
	ValidateToken(token string) error

	// AuthenticateBasic handles basic authentication.
	// Returns nil on success, or an error on failure.
	AuthenticateBasic(c echo.Context, username, password string) error

	// Logout invalidates the current session/token.
	// Returns nil on success, or an error on failure.
	Logout(c echo.Context) error
}
```

### Auth Middleware Implementation (Deprecated - See `auth/middleware.go`)

The middleware follows this decision flow:

1. Checks for Bearer token and validates if present
2. Falls back to session authentication for browser clients
3. Determines appropriate response based on client type 
4. Allows request to proceed if authentication succeeds, setting authentication details in the context

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

### System Monitoring

- `GET /api/v2/system/info` - Retrieves basic system information (OS, Arch, Uptime, etc.)
- `GET /api/v2/system/resources` - Retrieves system resource usage (CPU, Memory, Swap)
- `GET /api/v2/system/disks` - Retrieves information about disk partitions and usage
- `GET /api/v2/system/jobs` - Retrieves statistics about the analysis job queue
- `GET /api/v2/system/processes` - Retrieves information about running processes (application and children by default, or all with `?all=true`)

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
- Server-Sent Events (SSE) for real-time detection streaming
- Structured detection data with species images and metadata

### Range Filter Management

- View current range filter species count and list
- Test range filter with custom parameters (location, threshold, date)
- Rebuild range filter with current settings

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

### Range Filter API Endpoints

The API provides endpoints for managing and testing the BirdNET range filter, which filters species predictions based on geographic location and seasonal occurrence:

1. **Species Count**:
   - `GET /api/v2/range/species/count` - Returns the count of species currently included in the range filter
   - Response includes count, last updated timestamp, threshold, and location coordinates

2. **Species List**:
   - `GET /api/v2/range/species/list` - Returns the complete list of species in the current range filter
   - Each species includes label, scientific name, common name, and score
   - Response includes metadata about the filter (count, last updated, threshold, location)

3. **Range Filter Testing**:
   - `POST /api/v2/range/species/test` - Tests the range filter with custom parameters
   - Request body includes latitude, longitude, threshold, and optional date/week
   - Returns species that would be included with the test parameters
   - Useful for previewing filter results before changing settings

4. **Range Filter Rebuild**:
   - `POST /api/v2/range/rebuild` - Rebuilds the range filter using current location and threshold settings
   - Updates the species list based on current configuration
   - Returns success status and updated species count

Example test request:
```json
{
  "latitude": 60.1699,
  "longitude": 24.9384,
  "threshold": 0.01,
  "date": "2024-06-15"
}
```

Example response:
```json
{
  "species": [
    {
      "label": "Turdus merula_Eurasian Blackbird",
      "scientificName": "Turdus merula",
      "commonName": "Eurasian Blackbird",
      "score": 0.85
    }
  ],
  "count": 1,
  "threshold": 0.01,
  "location": {
    "latitude": 60.1699,
    "longitude": 24.9384
  },
  "testDate": "2024-06-15T00:00:00Z",
  "week": 24,
  "parameters": {
    "inputLatitude": 60.1699,
    "inputLongitude": 24.9384,
    "inputThreshold": 0.01,
    "inputDate": "2024-06-15"
  }
}
```

### Server-Sent Events (SSE) API Endpoints

The API provides real-time bird detection streaming via Server-Sent Events, allowing clients to receive detection data as it occurs:

1. **Detection Stream**:
   - `GET /api/v2/detections/stream` - Opens an SSE connection for real-time detection streaming
   - Sends all new bird detections as they are processed
   - Each detection includes complete note data, bird image information, and thumbnail URL
   - Connection includes periodic heartbeat messages to maintain the stream

2. **SSE Status**:
   - `GET /api/v2/sse/status` - Returns information about active SSE connections
   - Shows the number of connected clients and stream status

**SSE Event Types:**

- `connected` - Initial connection confirmation with client ID
- `detection` - New bird detection data
- `heartbeat` - Periodic keep-alive messages with timestamp and client count

**Detection Event Data Structure:**

Each detection event contains all the same data as a database entry plus additional metadata:

```json
{
  "id": 12345,
  "date": "2024-06-15",
  "time": "14:30:22",
  "source": "rtsp://camera.example.com/stream",
  "beginTime": "2024-06-15T14:30:20Z",
  "endTime": "2024-06-15T14:30:23Z",
  "speciesCode": "turdus_merula",
  "scientificName": "Turdus merula",
  "commonName": "Eurasian Blackbird",
  "confidence": 0.85,
  "verified": "unverified",
  "locked": false,
  "birdImage": {
    "url": "https://avicommons.org/...",
    "attribution": "...",
    "license": "..."
  },
  "timestamp": "2024-06-15T14:30:25Z",
  "eventType": "new_detection",
  "thumbnailUrl": "http://localhost:8080/api/v2/media/species-image?name=Turdus merula"
}
```

**Client Implementation Example:**

```javascript
const eventSource = new EventSource('/api/v2/detections/stream');

eventSource.addEventListener('connected', function(event) {
    const data = JSON.parse(event.data);
    console.log('Connected to detection stream:', data.clientId);
});

eventSource.addEventListener('detection', function(event) {
    const detection = JSON.parse(event.data);
    console.log('New detection:', detection.commonName, detection.confidence);
    // Process the detection data
    displayDetection(detection);
});

eventSource.addEventListener('heartbeat', function(event) {
    const data = JSON.parse(event.data);
    console.log('Heartbeat - clients:', data.clients);
});

eventSource.onerror = function(event) {
    console.error('SSE connection error:', event);
};
```

**Features:**

- **Real-time streaming**: Detections are broadcast immediately as they occur
- **Automatic reconnection**: Clients can implement reconnection logic on connection loss
- **Concurrent clients**: Multiple clients can connect simultaneously
- **Efficient delivery**: Uses event frequency tracking to prevent spam
- **Rich metadata**: Each detection includes species images and thumbnail URLs
- **Cross-origin support**: Includes CORS headers for web browser clients

**Notes:**

- The SSE endpoint is currently publicly accessible (no authentication required)
- In production environments, consider adding authentication for security
- The stream automatically handles client disconnections and cleanup
- Heartbeat messages are sent every 30 seconds to maintain connections
- Event frequency is controlled by the same event tracker used for other actions

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