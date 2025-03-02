# HTTP Controller Handlers Package

## Overview

The `handlers` package within `httpcontroller` is responsible for handling HTTP requests in the BirdNET-Go application. It provides a collection of handler functions that process incoming requests, interact with the database and other services, and return appropriate responses.

This package is a core component of the web interface and API of BirdNET-Go, providing the connection between the HTTP routes and the underlying application logic.

## Structure

The handler package is organized as follows:

- **`handlers.go`**: Core handler structure and common functionality
- **`detections.go`**: Handlers for bird detection related endpoints
- **`dashboard.go`**: Handlers for the main dashboard view
- **`media.go`**: Handlers for serving media content (spectrograms, audio clips)
- **`sse.go`**: Server-Sent Events implementation for real-time updates
- **`settings.go`**: Handlers for application settings management
- **`statistics.go`**: Handlers for statistical data endpoints
- **`species.go`**: Handlers for species-related endpoints
- **`weather.go`**: Handlers for weather information
- **`mqtt.go`**: Handlers for MQTT messaging 
- **`audio_level_sse.go`**: Real-time audio level updates via SSE
- **`utils.go`**: Utility functions used by handlers

## Core Components

### Handlers Struct

The main `Handlers` struct (defined in `handlers.go`) is the central component that holds dependencies and common functionality. It includes:

```go
type Handlers struct {
    baseHandler
    DS                datastore.Interface
    Settings          *conf.Settings
    DashboardSettings *conf.Dashboard
    BirdImageCache    *imageprovider.BirdImageCache
    SSE               *SSEHandler
    SunCalc           *suncalc.SunCalc
    AudioLevelChan    chan myaudio.AudioLevelData
    OAuth2Server      *security.OAuth2Server
    controlChan       chan string
    notificationChan  chan Notification
    CloudflareAccess  *security.CloudflareAccess
    debug             bool
    Server            interface{ IsAccessAllowed(c echo.Context) bool }
}
```

### Error Handling

The package implements a robust error handling system with a custom `HandlerError` type:

```go
type HandlerError struct {
    Err     error
    Message string
    Code    int
}
```

This allows for consistent error handling throughout the application, with appropriate HTTP status codes and user-friendly messages.

### Server-Sent Events (SSE)

The package includes a full implementation of Server-Sent Events for real-time updates:

- `SSEHandler`: Manages client connections and broadcasts notifications
- `Notification`: Represents messages sent to clients

## Key Functionalities

### Bird Detection Handlers

The package provides handlers for:
- Retrieving bird detections (hourly, by species, or via search)
- Getting detection details
- Serving detection media (audio clips and spectrograms)
- Managing detection reviews and verification

### Dashboard

Provides data for the main dashboard, including:
- Top bird sightings
- Hourly occurrence statistics
- Recent detections

### Settings Management

Comprehensive handlers for application settings, covering:
- Audio configuration
- BirdNET parameters
- Detection filters
- Integrations
- Security settings

### Media Serving

Handlers for serving media assets:
- Audio clips of bird sounds
- Spectrograms of detections
- Bird images

### Real-time Updates

Support for real-time updates via Server-Sent Events (SSE):
- Detection notifications
- Audio level updates
- System status

## Usage Examples

### Handler Registration

Handlers are registered with routes in the parent `httpcontroller` package:

```go
// Partial routes (HTMX responses)
s.partialRoutes = map[string]PartialRouteConfig{
    "/api/v1/detections":         {Path: "/api/v1/detections", Handler: h.WithErrorHandling(h.Detections)},
    "/api/v1/detections/recent":  {Path: "/api/v1/detections/recent", Handler: h.WithErrorHandling(h.RecentDetections)},
    "/api/v1/detections/details": {Path: "/api/v1/detections/details", Handler: h.WithErrorHandling(h.DetectionDetails)},
    // ... more routes ...
}
```

### Error Handling Wrapper

Handlers use a consistent error handling approach with the `WithErrorHandling` wrapper:

```go
func (h *Handlers) WithErrorHandling(fn func(echo.Context) error) echo.HandlerFunc {
    return func(c echo.Context) error {
        err := fn(c)
        if err != nil {
            return h.HandleError(err, c)
        }
        return nil
    }
}
```

### Implementing a New Handler

When implementing a new handler, follow this pattern:

```go
func (h *Handlers) MyNewHandler(c echo.Context) error {
    // Parse and validate request parameters
    // ...
    
    // Perform business logic with dependencies
    data, err := h.DS.SomeDataOperation()
    if err != nil {
        return h.NewHandlerError(err, "Failed to perform operation", http.StatusInternalServerError)
    }
    
    // Return appropriate response
    return c.Render(http.StatusOK, "templateName", data)
    // or
    return c.JSON(http.StatusOK, data)
}
```

## Best Practices

When working with the handlers package:

1. Use the `WithErrorHandling` wrapper for consistent error handling
2. Leverage the `NewHandlerError` method for creating handler errors
3. Keep handler methods focused on request/response processing
4. Put business logic in the appropriate service packages
5. Use appropriate HTTP status codes for responses
6. Document new handlers and their parameters
7. Use the provided dependencies instead of creating new ones

## Dependencies

The handlers package relies on several other packages:

- `echo`: Web framework for HTTP handling
- `datastore`: Database access interface
- `conf`: Configuration settings
- `imageprovider`: Bird image management
- `security`: Authentication and authorization
- `suncalc`: Sun event calculations
- `myaudio`: Audio processing

## Cross-Platform Compatibility

The handlers package is designed to be compatible with:

- Linux
- macOS
- Windows

No platform-specific code is used in the handlers themselves, ensuring consistent operation across all supported platforms. 