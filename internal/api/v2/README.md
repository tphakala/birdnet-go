# BirdNET-Go API v2 Documentation

## Overview

The API v2 provides comprehensive access to BirdNET-Go's bird detection and monitoring capabilities through REST endpoints and real-time streams. All endpoints are prefixed with `/api/v2`.

## Endpoint Registration Pattern

### Standard Registration
All endpoints follow this registration pattern in their respective `init*Routes()` functions:

```go
func (c *Controller) initExampleRoutes() {
    // Public endpoints (no authentication required)
    c.Group.GET("/path", c.HandlerFunction)
    c.Group.POST("/path", c.HandlerFunction)
    
    // Protected endpoints (authentication required)
    c.Group.POST("/protected-path", c.HandlerFunction, c.getEffectiveAuthMiddleware())
    
    // Alternative auth pattern (deprecated, use above)
    c.Group.POST("/legacy-auth", c.HandlerFunction, c.AuthMiddleware)
    
    // Rate-limited endpoints (typically for streams)
    rateLimiterConfig := middleware.RateLimiterConfig{
        Skipper: middleware.DefaultSkipper,
        Store: middleware.NewRateLimiterMemoryStoreWithConfig(
            middleware.RateLimiterMemoryStoreConfig{Rate: rate.Limit(1), Burst: 5},
        ),
    }
    c.Group.GET("/stream", c.StreamHandler, middleware.RateLimiterWithConfig(rateLimiterConfig))
}
```

### Authentication Middleware

Use `c.getEffectiveAuthMiddleware()` for new protected endpoints. This automatically selects the appropriate authentication method.

### Route Initialization

All route initialization functions are called from `api.go:initRoutes()`:

```go
routeInitializers := []struct {
    name string
    fn   func()
}{
    {"example routes", c.initExampleRoutes},
    // Add new route groups here
}
```

## Complete API Endpoints

### Core API (`api.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/health` | `HealthCheck` | ❌ | System health status |

### Authentication (`auth.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| POST | `/auth/login` | `Login` | ❌ | User authentication |
| POST | `/auth/logout` | `Logout` | ✅ | End user session |
| GET | `/auth/status` | `GetAuthStatus` | ✅ | Check authentication status |

### Analytics (`analytics.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/analytics/species/daily` | `GetDailySpeciesSummary` | ❌ | Daily species detection summary |
| GET | `/analytics/species/summary` | `GetSpeciesSummary` | ❌ | Overall species statistics |
| GET | `/analytics/species/detections/new` | `GetNewSpeciesDetections` | ❌ | Recently detected new species |
| GET | `/analytics/species/thumbnails` | `GetSpeciesThumbnails` | ❌ | Species thumbnail images |
| GET | `/analytics/time/hourly` | `GetHourlyAnalytics` | ❌ | Hourly detection patterns |
| GET | `/analytics/time/daily` | `GetDailyAnalytics` | ❌ | Daily detection patterns |
| GET | `/analytics/time/distribution/hourly` | `GetTimeOfDayDistribution` | ❌ | Time-of-day detection distribution |

### Control Operations (`control.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| POST | `/control/restart` | `RestartAnalysis` | ✅ | Restart analysis engine |
| POST | `/control/reload` | `ReloadModel` | ✅ | Reload BirdNET model |
| POST | `/control/rebuild-filter` | `RebuildFilter` | ✅ | Rebuild range filter |
| GET | `/control/actions` | `GetAvailableActions` | ✅ | List available control actions |

### Debug (`debug.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| POST | `/debug/trigger-error` | `DebugTriggerError` | ✅ | Trigger test error |
| POST | `/debug/trigger-notification` | `DebugTriggerNotification` | ✅ | Trigger test notification |
| GET | `/debug/status` | `DebugSystemStatus` | ✅ | System debug information |

### Detections (`detections.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/detections` | `GetDetections` | ❌ | List bird detections |
| GET | `/detections/:id` | `GetDetection` | ❌ | Get specific detection |
| GET | `/detections/recent` | `GetRecentDetections` | ❌ | Recent detections |
| GET | `/detections/:id/time-of-day` | `GetDetectionTimeOfDay` | ❌ | Detection time context |
| DELETE | `/detections/:id` | `DeleteDetection` | ✅ | Delete detection record |
| POST | `/detections/:id/review` | `ReviewDetection` | ✅ | Review/verify detection |
| POST | `/detections/:id/lock` | `LockDetection` | ✅ | Lock detection from changes |
| POST | `/detections/ignore` | `IgnoreSpecies` | ✅ | Add species to ignore list |

### Integrations (`integrations.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/integrations/mqtt/status` | `GetMQTTStatus` | ✅ | MQTT connection status |
| POST | `/integrations/mqtt/test` | `TestMQTTConnection` | ✅ | Test MQTT connection |
| GET | `/integrations/birdweather/status` | `GetBirdWeatherStatus` | ✅ | BirdWeather integration status |
| POST | `/integrations/birdweather/test` | `TestBirdWeatherConnection` | ✅ | Test BirdWeather connection |
| POST | `/integrations/weather/test` | `TestWeatherConnection` | ✅ | Test weather provider connection |

### Media (`media.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/media/audio/:filename` | `ServeAudioClip` | ❌ | Serve audio file |
| GET | `/media/spectrogram/:filename` | `ServeSpectrogram` | ❌ | Serve spectrogram image |
| GET | `/media/audio` | `ServeAudioByQueryID` | ❌ | Serve audio by detection ID |
| GET | `/media/species-image` | `GetSpeciesImage` | ❌ | Get species thumbnail image |

### Notifications (`notifications.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/notifications/stream` | `StreamNotifications` | ✅⚡ | SSE notification & toast stream (authenticated) |
| GET | `/notifications` | `GetNotifications` | ❌ | List notifications |
| GET | `/notifications/:id` | `GetNotification` | ❌ | Get specific notification |
| PUT | `/notifications/:id/read` | `MarkNotificationRead` | ❌ | Mark notification as read |
| PUT | `/notifications/:id/acknowledge` | `MarkNotificationAcknowledged` | ❌ | Acknowledge notification |
| DELETE | `/notifications/:id` | `DeleteNotification` | ❌ | Delete notification |
| GET | `/notifications/unread/count` | `GetUnreadCount` | ❌ | Count unread notifications |

### Range Filter (`range.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/range/species/count` | `GetRangeFilterSpeciesCount` | ❌ | Species count with range filter |
| GET | `/range/species/list` | `GetRangeFilterSpeciesList` | ❌ | Species list with range filter |
| POST | `/range/species/test` | `TestRangeFilter` | ❌ | Test range filter configuration |
| POST | `/range/rebuild` | `RebuildRangeFilter` | ❌ | Rebuild range filter data |

### Search (`search.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| POST | `/search` | `HandleSearch` | ❌ | Search detections with filters |

### Settings (`settings.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/settings` | `GetAllSettings` | ✅ | Get all configuration settings |
| GET | `/settings/locales` | `GetLocales` | ✅ | Get available locales |
| GET | `/settings/imageproviders` | `GetImageProviders` | ✅ | Get image provider options |
| GET | `/settings/systemid` | `GetSystemID` | ✅ | Get system identifier |
| GET | `/settings/:section` | `GetSectionSettings` | ✅ | Get specific settings section |
| PUT | `/settings` | `UpdateSettings` | ✅ | Update all settings |
| PATCH | `/settings/:section` | `UpdateSectionSettings` | ✅ | Update settings section |

### Filesystem (`filesystem.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/filesystem/browse` | `BrowseFileSystem` | ✅ | Browse files and directories with secure path validation |

### Species (`species.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/species` | `GetSpeciesInfo` | ❌ | Get extended species information including rarity status |
| GET | `/species/taxonomy` | `GetSpeciesTaxonomy` | ❌ | Get detailed taxonomy data with subspecies and hierarchy |
| GET | `/species/:code/thumbnail` | `GetSpeciesThumbnail` | ❌ | Get bird thumbnail image by species code (redirects to image URL) |

### Server-Sent Events (`sse.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/detections/stream` | `StreamDetections` | ❌⚡ | Real-time detection stream |
| GET | `/soundlevels/stream` | `StreamSoundLevels` | ❌⚡ | Real-time audio level stream |
| GET | `/sse/status` | `GetSSEStatus` | ❌ | SSE connection status |

### Streams (`streams.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/streams/audio-level` | `HandleAudioLevelStream` | ✅ | Audio level stream |
| GET | `/streams/notifications` | `HandleNotificationsStream` | ✅ | Notification stream |

### Support (`support.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| POST | `/support/generate` | `GenerateSupportDump` | ✅ | Generate support diagnostic dump |
| GET | `/support/download/:id` | `DownloadSupportDump` | ✅ | Download support dump |
| GET | `/support/status` | `GetSupportStatus` | ✅ | Support system status |

### System Information (`system.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/system/info` | `GetSystemInfo` | ✅ | General system information |
| GET | `/system/resources` | `GetResourceInfo` | ✅ | Resource usage information |
| GET | `/system/disks` | `GetDiskInfo` | ✅ | Disk usage information |
| GET | `/system/jobs` | `GetJobQueueStats` | ✅ | Job queue statistics |
| GET | `/system/processes` | `GetProcessInfo` | ✅ | Process information |
| GET | `/system/temperature/cpu` | `GetSystemCPUTemperature` | ✅ | CPU temperature |
| GET | `/system/audio/devices` | `GetAudioDevices` | ✅ | Available audio devices |
| GET | `/system/audio/active` | `GetActiveAudioDevice` | ✅ | Active audio device |

### Weather (`weather.go`)

| Method | Route | Handler | Auth | Description |
|--------|-------|---------|------|-------------|
| GET | `/weather/daily/:date` | `GetDailyWeather` | ❌ | Daily weather data |
| GET | `/weather/hourly/:date` | `GetHourlyWeatherForDay` | ❌ | Hourly weather for day |
| GET | `/weather/hourly/:date/:hour` | `GetHourlyWeatherForHour` | ❌ | Specific hour weather |
| GET | `/weather/detection/:id` | `GetWeatherForDetection` | ❌ | Weather for detection time |
| GET | `/weather/latest` | `GetLatestWeather` | ❌ | Latest weather data |
| GET | `/weather/sun/:date` | `GetSunTimes` | ❌ | Sun times (sunrise/sunset) for date |

## Legend

- ✅ = Authentication required
- ❌ = No authentication required  
- ⚡ = Rate limited
- 🔒 = Admin only (subset of authenticated)

## Adding New Endpoints

### 1. Create Handler Function

```go
// HandlerName handles the endpoint description
func (c *Controller) HandlerName(ctx echo.Context) error {
    // Validate input
    // Process request
    // Return response
    return ctx.JSON(http.StatusOK, response)
}
```

### 2. Register Route

Add to appropriate `init*Routes()` function:

```go
func (c *Controller) initExampleRoutes() {
    // Choose appropriate pattern based on authentication needs
    c.Group.GET("/path", c.HandlerName)                                    // Public
    c.Group.POST("/path", c.HandlerName, c.getEffectiveAuthMiddleware())   // Protected
}
```

### 3. Add to Route Initializers

Update `api.go:initRoutes()` if creating a new route category:

```go
routeInitializers := []struct {
    name string
    fn   func()
}{
    // ... existing routes ...
    {"new category routes", c.initNewCategoryRoutes},
}
```

### 4. Update Documentation

- Add endpoint to this README.md
- Add usage examples if complex
- Update any API client documentation

## Best Practices

### Error Handling
```go
return c.HandleError(ctx, err, "Description of what failed", http.StatusBadRequest)
```

### Input Validation
```go
// Always validate user input
if param == "" {
    return c.HandleError(ctx, nil, "Parameter is required", http.StatusBadRequest)
}
```

### Logging
```go
// Use structured logging
c.logAPIRequest(ctx, slog.LevelInfo, "Operation completed", "key", value)
```

### Authentication
- Use `c.getEffectiveAuthMiddleware()` for protected endpoints
- Consider IP bypass rules for local access
- Use proper HTTP status codes (401 vs 403)

### Response Format
```go
// Consistent JSON responses
type Response struct {
    Data    interface{} `json:"data,omitempty"`
    Error   string      `json:"error,omitempty"`
    Message string      `json:"message,omitempty"`
}
```

## Security Considerations

1. **Input Validation**: All user input must be validated
2. **Path Traversal**: Use SecureFS for file access
3. **SQL Injection**: Use parameterized queries
4. **Authentication**: Protect sensitive operations
5. **Rate Limiting**: Apply to resource-intensive endpoints
6. **CORS**: Configured at the group level
7. **IP Filtering**: Available via subnet bypass settings

## Error Response Format

All API errors follow this structure:

```json
{
    "error": "Error message",
    "message": "Human-readable description", 
    "code": 400,
    "correlation_id": "abc12345"
}
```

## Rate Limiting

SSE endpoints are rate limited to prevent abuse:
- Detection streams: 10 requests/minute per IP
- Sound level streams: 10 requests/minute per IP
- Notification streams: 1 request/second, burst of 5 (authenticated)

## Server-Sent Events (SSE)

### Unified Notification Stream

The `/notifications/stream` endpoint provides both notifications and toast messages:

**Event Types:**
- `notification` - System notifications (errors, warnings, info)
- `toast` - Temporary UI messages (success, info, warning, error)
- `connected` - Connection established
- `heartbeat` - Keep-alive signal

**Authentication:** Required (uses session or bearer token)

**Toast Event Format:**
```json
{
  "id": "toast-id",
  "message": "Operation completed successfully",
  "type": "success",
  "duration": 5000,
  "component": "settings",
  "timestamp": "2024-01-01T12:00:00Z",
  "action": {
    "label": "View Details",
    "url": "/details",
    "handler": "viewDetails"
  }
}