# BirdNet-Go API Package

This package implements the HTTP server and RESTful API for the BirdNET-Go application, providing endpoints for bird detection data management, analytics, system control, and more.

## Package Structure

```text
internal/api/
├── config.go              - Server configuration from settings
├── server.go              - Main HTTP server (Echo framework wrapper)
├── spa.go                 - Single Page Application handler
├── static.go              - Static file server for frontend assets
├── auth/                  - Authentication service package
│   ├── adapter.go         - Adapter for security package (OAuth2Server)
│   ├── authmethod_string.go - Auto-generated AuthMethod stringer
│   ├── middleware.go      - Authentication middleware enforcer
│   └── service.go         - Authentication service interface & AuthMethod enum
├── middleware/            - HTTP middleware
│   ├── compression.go     - Gzip compression middleware (SSE-aware)
│   ├── csrf.go            - CSRF token validation middleware
│   ├── logging.go         - Structured request logging middleware
│   └── security.go        - CORS, HSTS, secure headers middleware
└── v2/                    - API v2 handlers and routes
    ├── api.go             - API controller and route initialization
    ├── analytics.go       - Analytics and statistics endpoints
    ├── audio_hls.go       - HLS audio streaming endpoints
    ├── audio_level.go     - Real-time audio level SSE streaming
    ├── auth.go            - Authentication endpoints (login, logout, status)
    ├── constants.go       - API-wide constants
    ├── control.go         - System control actions (restart, reload, rebuild)
    ├── debug.go           - Debug/testing endpoints
    ├── detections.go      - Bird detection CRUD endpoints
    ├── dynamic_thresholds.go - Dynamic detection threshold logic
    ├── filesystem.go      - Secure filesystem browsing endpoint
    ├── integrations.go    - External service integrations (MQTT, BirdWeather, Weather)
    ├── media.go           - Media serving (audio, spectrograms, species images)
    ├── notifications.go   - Notification management & SSE stream
    ├── range.go           - Range filter management and testing
    ├── search.go          - Detection search with filtering
    ├── settings.go        - Application settings management & hot reload
    ├── settings_audio.go  - Audio settings hot reload logic
    ├── species.go         - Species information endpoints
    ├── species_taxonomy.go - Species taxonomy data endpoints
    ├── sse.go             - Server-Sent Events broadcaster
    ├── streams_health.go  - RTSP stream health monitoring
    ├── support.go         - Support diagnostic bundle generation
    ├── system.go          - System information and resource monitoring
    ├── toast_helpers.go   - Toast notification helpers
    ├── utils.go           - Utility functions
    └── weather.go         - Weather data endpoints
```

## Architecture Overview

The API package is split into two layers:

1. **HTTP Server Layer** (`internal/api/`) - Manages the Echo framework, middleware, static files, and SPA routing
2. **API Controller Layer** (`internal/api/v2/`) - Handles all REST API endpoints under `/api/v2`

## HTTP Server

The main `Server` struct in `server.go` wraps the Echo framework and provides:

- Middleware stack (recovery, logging, CORS, gzip, security headers)
- Static file serving for frontend assets
- SPA routing for Svelte frontend
- TLS/AutoTLS support
- Graceful shutdown

### Server Initialization

The server uses functional options pattern for configuration:

```go
import (
    "github.com/tphakala/birdnet-go/internal/api"
    "github.com/tphakala/birdnet-go/internal/conf"
)

// Create server with options
server, err := api.New(
    settings,
    api.WithDataStore(dataStore),
    api.WithBirdImageCache(imageCache),
    api.WithProcessor(processor),
    api.WithMetrics(metrics),
    api.WithControlChannel(controlChan),
    api.WithAudioLevelChannel(audioLevelChan),
)
if err != nil {
    return err
}

// Start server (non-blocking)
server.Start()

// Later: graceful shutdown
server.Shutdown()
```

### Available Options

| Option | Purpose |
|--------|---------|
| `WithLogger(logger)` | Set standard logger |
| `WithDataStore(ds)` | Set database interface |
| `WithBirdImageCache(cache)` | Set species image cache |
| `WithSunCalc(sc)` | Set sun calculator |
| `WithProcessor(proc)` | Set analysis processor |
| `WithOAuth2Server(oauth)` | Set OAuth2 server |
| `WithMetrics(m)` | Set observability metrics |
| `WithControlChannel(ch)` | Set control signal channel |
| `WithAudioLevelChannel(ch)` | Set audio level channel |

## API Controller

The v2 API is organized around a central `Controller` struct in `v2/api.go` that manages all REST endpoints. Dependencies are injected from the parent server.

## API Versions

Currently, the package implements version 2 (`v2`) of the API with all endpoints under the `/api/v2` prefix.

## Authentication

The API implements a comprehensive, service-based authentication system managed by the `internal/api/auth` package. This package decouples authentication logic from API handlers and supports multiple authentication methods.

Key components of the `auth` package:

- **`Service` Interface (`auth/service.go`)**: Defines the contract for authentication operations (checking access, validating tokens, basic auth, logout, etc.) and standard sentinel errors.
- **`AuthMethod` Enum (`auth/service.go`)**: Represents different authentication methods (`Token`, `BrowserSession`, `BasicAuth`, `LocalSubnet`, `None`). Uses `go generate` with `stringer`.
- **`SecurityAdapter` (`auth/adapter.go`)**: Implements the `Service` interface, adapting the `internal/security` package's `OAuth2Server` for core logic like session checks, token validation, and basic auth credential verification.
- **`Middleware` (`auth/middleware.go`)**: An Echo middleware that uses the `AuthService` to enforce authentication. It checks if auth is required, attempts token and session authentication, sets context values (`isAuthenticated`, `username`, `authMethod`) on success, and handles unauthenticated requests appropriately (redirect for browsers, 401 for API clients).

**Authentication Flow:**

1.  The `Middleware` intercepts requests.
2.  It checks `AuthService.IsAuthRequired`. If not required (e.g., local subnet), proceeds with `AuthMethodNone`.
3.  If required, it checks for a `Bearer` token via `AuthService.ValidateToken`.
4.  If no valid token, it checks for a session via `AuthService.CheckAccess`.
5.  On success (token or session), it sets context and proceeds.
6.  On failure, it redirects browsers or returns a 401 error for API clients.

Protected endpoints use this auth middleware. The system handles browser clients (redirecting to login) and API clients (returning 401 JSON errors) appropriately.

### Authentication Service Interface

The authentication service interface in `auth/service.go` provides these key operations:

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

### Auth Middleware Implementation

The middleware in `auth/middleware.go` follows this decision flow:

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
- Species taxonomy and classification data

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
- `GET /api/v2/system/temperature` - Retrieves system temperature sensors (CPU, GPU, etc.)
- `GET /api/v2/system/audio-devices` - Lists available audio input devices

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

- Server-Sent Events (SSE) for real-time detection streaming
- Audio level SSE streaming for visualization
- Stream health SSE for monitoring RTSP sources
- Notification SSE for real-time alerts
- HLS audio streaming for live audio playback
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
const eventSource = new EventSource("/api/v2/detections/stream");

eventSource.addEventListener("connected", function (event) {
  const data = JSON.parse(event.data);
  console.log("Connected to detection stream:", data.clientId);
});

eventSource.addEventListener("detection", function (event) {
  const detection = JSON.parse(event.data);
  console.log("New detection:", detection.commonName, detection.confidence);
  // Process the detection data
  displayDetection(detection);
});

eventSource.addEventListener("heartbeat", function (event) {
  const data = JSON.parse(event.data);
  console.log("Heartbeat - clients:", data.clients);
});

eventSource.onerror = function (event) {
  console.error("SSE connection error:", event);
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

### HLS Audio Streaming API Endpoints

The API provides HTTP Live Streaming (HLS) for real-time audio from configured sources:

1. **Start Stream**:
   - `POST /api/v2/streams/hls/start` - Starts an HLS stream for a specified audio source
   - Request body includes `sourceUrl` (RTSP URL) and optional parameters
   - Returns stream ID and playlist URL

2. **Stop Stream**:
   - `POST /api/v2/streams/hls/stop` - Stops an active HLS stream
   - Request body includes `streamId`

3. **Get Playlist**:
   - `GET /api/v2/streams/hls/:streamId/playlist.m3u8` - Returns the HLS playlist for a stream
   - Standard HLS format compatible with video.js and other players

4. **Get Segment**:
   - `GET /api/v2/streams/hls/:streamId/:segment` - Returns individual HLS segments

5. **Heartbeat**:
   - `POST /api/v2/streams/hls/heartbeat` - Keep stream alive (prevents auto-shutdown)

**Features:**

- FFmpeg-based transcoding for broad compatibility
- Automatic stream cleanup on client disconnect
- Configurable segment duration and playlist length

### RTSP Stream Health Monitoring API Endpoints

The API provides comprehensive health monitoring for RTSP audio streams:

1. **Stream Health Summary**:
   - `GET /api/v2/streams/health` - Returns health status for all configured streams
   - Includes connection status, error counts, and last activity timestamps

2. **Individual Stream Health**:
   - `GET /api/v2/streams/health/:name` - Returns detailed health for a specific stream
   - Includes error history, circuit breaker status, and recovery attempts

3. **Stream Health SSE**:
   - `GET /api/v2/streams/health/stream` - Real-time health updates via SSE
   - Broadcasts status changes as they occur

**Health Status Types:**

- `healthy` - Stream is connected and receiving data
- `degraded` - Stream has intermittent issues but is recovering
- `unhealthy` - Stream is disconnected or has persistent errors
- `unknown` - Stream status cannot be determined

### Audio Level Streaming API Endpoints

Real-time audio level monitoring via Server-Sent Events:

1. **Audio Level Stream**:
   - `GET /api/v2/streams/audio-level` - Opens an SSE connection for real-time audio levels
   - Returns normalized audio levels (0.0-1.0) for visualization
   - Includes peak detection and RMS values

**Features:**

- Connection limits to prevent resource exhaustion
- Anonymized client tracking
- Automatic cleanup on disconnect

### Filesystem Browsing API Endpoints

Secure filesystem browsing for configuration and file selection:

1. **Browse Directory**:
   - `GET /api/v2/filesystem/browse?path={path}` - Lists directory contents
   - Returns files and subdirectories with metadata
   - Protected endpoint requiring authentication

**Security:**

- Path traversal protection via SecureFS
- Restricted to allowed directories only
- Validates paths against configured roots

### Debug API Endpoints

Debug endpoints for testing and development (protected):

1. **Trigger Test Notification**:
   - `POST /api/v2/debug/notification` - Sends a test notification

2. **Trigger Test Error**:
   - `POST /api/v2/debug/error` - Triggers a test error for error handling verification

3. **Debug Status**:
   - `GET /api/v2/debug/status` - Returns debug mode status and configuration

**Notes:**

- All debug endpoints require authentication
- Only available when debug mode is enabled

### Middleware Implementation

The `internal/api/middleware/` package provides HTTP middleware used by the server:

| Middleware | File | Purpose |
|------------|------|---------|
| Recovery | Echo built-in | Panic recovery |
| RequestLogger | `logging.go` | Structured request logging |
| CORS | `security.go` | Cross-origin resource sharing |
| BodyLimit | `security.go` | Request body size limits |
| Gzip | `compression.go` | Response compression (auto-skips SSE) |
| SecureHeaders | `security.go` | Security headers (HSTS, X-Frame-Options) |
| CSRF | `csrf.go` | CSRF token validation for state-changing operations |

The API uses authentication middleware from `auth/middleware.go` which handles Bearer token and session-based authentication.

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

5. **Logging**: Handlers utilize structured logging via `c.log` (cached `logger.Logger`):
   - Log entry points with `Info` level, including relevant request parameters (e.g., date, species, ID) and context (IP address, request path).
   - Log successful outcomes with `Info` level, summarizing results (e.g., number of records fetched, action completed).
   - Log validation errors or expected issues (e.g., resource not found, invalid parameters) with `Warn` level.
   - Log unexpected errors (e.g., database failures, internal processing errors) with `Error` level, including the underlying error message and relevant context.
   - Use `Debug` for verbose debugging information during development.
   - Example:
     ```go
     c.log.Info("handling request for detection",
         logger.String("detection_id", id),
         logger.String("ip", ctx.RealIP()),
         logger.String("path", ctx.Request().URL.Path))

     // ... processing ...
     if err != nil {
         c.log.Error("failed to fetch detection from datastore",
             logger.String("detection_id", id),
             logger.Error(err),
             logger.String("ip", ctx.RealIP()))
         return c.HandleError(ctx, err, "Database error", http.StatusInternalServerError)
     }

     c.log.Info("successfully retrieved detection",
         logger.String("detection_id", id))
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

5. **Hot Reload Support**:
   The following settings are automatically applied at runtime without restart:

   | Category | Action | Notification |
   |----------|--------|--------------|
   | BirdNET model | `reload_birdnet` | ✅ |
   | Range filter | `rebuild_range_filter` | ✅ |
   | Species intervals | `update_detection_intervals` | ✅ |
   | MQTT | `reconfigure_mqtt` | ✅ |
   | BirdWeather | `reconfigure_birdweather` | ✅ |
   | RTSP sources | `reconfigure_rtsp_sources` | ✅ |
   | Telemetry | `reconfigure_telemetry` | ✅ |
   | Species tracking | `reconfigure_species_tracking` | ✅ |
   | Audio/Equalizer | Various | ✅ |

   **Web server settings** (port, TLS, etc.) require a restart - users are notified via toast.

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

1. Echo web framework (managed internally)
2. Access to a datastore implementation
3. Application configuration (`conf.Settings`)
4. Optional services: image cache, processor, metrics

### Initialization

See [Server Initialization](#server-initialization) above for the recommended approach using functional options.

### Runtime Server Selection

The HTTP server is configured via:

```yaml
webserver:
  enabled: true
  port: "8080"
  debug: false
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
- Use the dedicated sensitive field constructors from the logger package
- These constructors automatically sanitize data using the privacy package
- Example:

  ```go
  // INCORRECT - exposes credentials
  c.log.Info("login attempt", logger.String("username", username), logger.String("password", password))

  // CORRECT - use semantic sensitive field constructors
  c.log.Info("login attempt", logger.Username(username))

  // Available sensitive field constructors:
  // logger.Username(value)           - hashes username for safe log correlation
  // logger.Password(value)           - always returns [REDACTED]
  // logger.Token(key, value)         - redacts with length hint: [TOKEN:len=N]
  // logger.URL(key, value)           - sanitizes URLs
  // logger.CredentialURL(key, value) - sanitizes URLs with embedded credentials
  // logger.SanitizedString(key, val) - applies full privacy scrubbing
  // logger.SanitizedError(err)       - scrubs error messages
  // logger.Credential(key)           - marks field as [REDACTED]
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
      c.log.Warn("failed to set write deadline", logger.Error(err))
      return err
  }
  if err := conn.WriteMessage(messageType, payload); err != nil {
      c.log.Warn("failed to write message", logger.Error(err))
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
      c.log.Warn("failed to set read deadline", logger.Error(err))
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

Each module has comprehensive test coverage with various test file patterns. Run tests with:

```bash
go test -v ./internal/api/...
go test -v ./internal/api/v2/...
go test -v ./internal/api/auth/...
```

### Test File Categories

| Pattern | Purpose |
|---------|---------|
| `*_test.go` | Standard unit tests for each handler/feature |
| `*_integration_test.go` | Integration tests combining multiple components |
| `*_edge_test.go` | Edge case and boundary condition tests |
| `*_concurrent_test.go` | Concurrency and race condition tests |
| `*_malformed_test.go` | Malformed input validation tests |
| `*_malicious_test.go` | Security and attack scenario tests |
| `*_extreme_test.go` | Extreme value and stress tests |

### Testing Best Practices

1. **Mock Dependencies**: Use mock implementations of datastore and other dependencies
2. **Test Both Success and Failure Paths**: Ensure error handling works correctly
3. **Validate Response Structures**: Ensure JSON responses match expected formats
4. **Test Middleware Behavior**: Verify auth middleware correctly allows/denies requests
5. **Use Table-Driven Tests**: For testing multiple input scenarios
6. **Race Detection**: Run tests with `-race` flag during development

## Security Considerations

- All sensitive endpoints require authentication
- Use HTTPS in production
- The API implements CORS middleware
- Authentication is required for system control operations
- Properly manage API tokens with appropriate expiration policies
- Implement rate limiting for public endpoints
