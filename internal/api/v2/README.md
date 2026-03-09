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

| Method | Route     | Handler       | Auth | Description          |
| ------ | --------- | ------------- | ---- | -------------------- |
| GET    | `/health` | `HealthCheck` | ❌   | System health status |

### Authentication (`auth.go`)

| Method | Route          | Handler         | Auth | Description                 |
| ------ | -------------- | --------------- | ---- | --------------------------- |
| POST   | `/auth/login`  | `Login`         | ❌   | User authentication         |
| POST   | `/auth/logout` | `Logout`        | ✅   | End user session            |
| GET    | `/auth/status` | `GetAuthStatus` | ✅   | Check authentication status |

### Analytics (`analytics.go`)

| Method | Route                                 | Handler                    | Auth | Description                        |
| ------ | ------------------------------------- | -------------------------- | ---- | ---------------------------------- |
| GET    | `/analytics/species/daily`            | `GetDailySpeciesSummary`   | ❌   | Daily species detection summary    |
| GET    | `/analytics/species/summary`          | `GetSpeciesSummary`        | ❌   | Overall species statistics         |
| GET    | `/analytics/species/detections/new`   | `GetNewSpeciesDetections`  | ❌   | Recently detected new species      |
| GET    | `/analytics/species/thumbnails`       | `GetSpeciesThumbnails`     | ❌   | Species thumbnail images           |
| GET    | `/analytics/time/hourly`              | `GetHourlyAnalytics`       | ❌   | Hourly detection patterns          |
| GET    | `/analytics/time/daily`               | `GetDailyAnalytics`        | ❌   | Daily detection patterns           |
| GET    | `/analytics/time/distribution/hourly` | `GetTimeOfDayDistribution` | ❌   | Time-of-day detection distribution |

### Control Operations (`control.go`)

| Method | Route                     | Handler               | Auth | Description                    |
| ------ | ------------------------- | --------------------- | ---- | ------------------------------ |
| POST   | `/control/restart`        | `RestartAnalysis`     | ✅   | Restart analysis engine        |
| POST   | `/control/reload`         | `ReloadModel`         | ✅   | Reload BirdNET model           |
| POST   | `/control/rebuild-filter` | `RebuildFilter`       | ✅   | Rebuild range filter           |
| GET    | `/control/actions`        | `GetAvailableActions` | ✅   | List available control actions |

### Debug (`debug.go`)

| Method | Route                         | Handler                    | Auth | Description               |
| ------ | ----------------------------- | -------------------------- | ---- | ------------------------- |
| POST   | `/debug/trigger-error`        | `DebugTriggerError`        | ✅   | Trigger test error        |
| POST   | `/debug/trigger-notification` | `DebugTriggerNotification` | ✅   | Trigger test notification |
| GET    | `/debug/status`               | `DebugSystemStatus`        | ✅   | System debug information  |

### Detections (`detections.go`)

| Method | Route                         | Handler                 | Auth | Description                                |
| ------ | ----------------------------- | ----------------------- | ---- | ------------------------------------------ |
| GET    | `/detections`                 | `GetDetections`         | ❌   | List bird detections                       |
| GET    | `/detections/:id`             | `GetDetection`          | ❌   | Get specific detection                     |
| GET    | `/detections/recent`          | `GetRecentDetections`   | ❌   | Recent detections                          |
| GET    | `/detections/:id/time-of-day` | `GetDetectionTimeOfDay` | ❌   | Detection time context                     |
| DELETE | `/detections/:id`             | `DeleteDetection`       | ✅   | Delete detection record                    |
| POST   | `/detections/:id/review`      | `ReviewDetection`       | ✅   | Review/verify detection                    |
| POST   | `/detections/:id/lock`        | `LockDetection`         | ✅   | Lock detection from changes                |
| POST   | `/detections/ignore`          | `IgnoreSpecies`         | ✅   | Toggle species in ignore list (add/remove) |
| GET    | `/detections/ignored`         | `GetExcludedSpecies`    | ✅   | Get list of excluded species               |

### Integrations (`integrations.go`)

| Method | Route                                        | Handler                         | Auth | Description                           |
| ------ | -------------------------------------------- | ------------------------------- | ---- | ------------------------------------- |
| GET    | `/integrations/mqtt/status`                  | `GetMQTTStatus`                 | ✅   | MQTT connection status                |
| POST   | `/integrations/mqtt/test`                    | `TestMQTTConnection`            | ✅   | Test MQTT connection                  |
| POST   | `/integrations/mqtt/homeassistant/discovery` | `TriggerHomeAssistantDiscovery` | ✅   | Trigger Home Assistant MQTT discovery |
| GET    | `/integrations/birdweather/status`           | `GetBirdWeatherStatus`          | ✅   | BirdWeather integration status        |
| POST   | `/integrations/birdweather/test`             | `TestBirdWeatherConnection`     | ✅   | Test BirdWeather connection           |
| POST   | `/integrations/weather/test`                 | `TestWeatherConnection`         | ✅   | Test weather provider connection      |

### Media (`media.go`)

| Method | Route                                | Handler                  | Auth | Description                       |
| ------ | ------------------------------------ | ------------------------ | ---- | --------------------------------- |
| GET    | `/media/audio/:filename`             | `ServeAudioClip`         | ❌   | Serve audio file                  |
| GET    | `/media/spectrogram/:filename`       | `ServeSpectrogram`       | ❌   | Serve spectrogram image           |
| GET    | `/media/audio`                       | `ServeAudioByQueryID`    | ❌   | Serve audio by detection ID       |
| GET    | `/media/species-image`               | `GetSpeciesImage`        | ❌   | Get species thumbnail image       |
| GET    | `/media/species-image/info`          | `GetSpeciesImageInfo`    | ❌   | Get species image attribution     |
| GET    | `/media/image/:scientific_name`      | `ServeSpeciesImageProxy` | ❌   | Serve cached bird image (proxy)   |
| GET    | `/media/bird-image/:scientific_name` | `ServeSpeciesImageProxy` | ❌   | Alias for image proxy endpoint    |
| GET    | `/spectrogram/:id/status`            | `GetSpectrogramStatus`   | ❌   | Get spectrogram generation status |

### Notifications (`notifications.go`)

| Method | Route                              | Handler                            | Auth | Description                                                                                                                                                   |
| ------ | ---------------------------------- | ---------------------------------- | ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GET    | `/notifications/stream`            | `StreamNotifications`              | ✅⚡ | SSE notification & toast stream (authenticated)                                                                                                               |
| GET    | `/notifications`                   | `GetNotifications`                 | ✅   | List notifications                                                                                                                                            |
| GET    | `/notifications/:id`               | `GetNotification`                  | ✅   | Get specific notification                                                                                                                                     |
| PUT    | `/notifications/:id/read`          | `MarkNotificationRead`             | ✅   | Mark notification as read                                                                                                                                     |
| PUT    | `/notifications/:id/acknowledge`   | `MarkNotificationAcknowledged`     | ✅   | Acknowledge notification                                                                                                                                      |
| DELETE | `/notifications/:id`               | `DeleteNotification`               | ✅   | Delete notification                                                                                                                                           |
| GET    | `/notifications/unread/count`      | `GetUnreadCount`                   | ✅   | Count unread notifications                                                                                                                                    |
| POST   | `/notifications/test/new-species`  | `CreateTestNewSpeciesNotification` | ✅   | Create test new-species notification                                                                                                                          |
| GET    | `/notifications/check-ntfy-server` | `CheckNtfyServer`                  | ✅   | Probe NTFY host for HTTPS/HTTP connectivity. Query: `host=<hostname[:port]>`. Response: `{"recommended":"https\|http\|unreachable","https":bool,"http":bool}` |

### Range Filter (`range.go`)

| Method | Route                  | Handler                      | Auth | Description                         |
| ------ | ---------------------- | ---------------------------- | ---- | ----------------------------------- |
| GET    | `/range/species/count` | `GetRangeFilterSpeciesCount` | ❌   | Species count with range filter     |
| GET    | `/range/species/list`  | `GetRangeFilterSpeciesList`  | ❌   | Species list with taxonomy groups   |
| GET    | `/range/species/csv`   | `GetRangeFilterSpeciesCSV`   | ❌   | Export species list as CSV download |
| POST   | `/range/species/test`  | `TestRangeFilter`            | ❌   | Test range filter configuration     |
| POST   | `/range/rebuild`       | `RebuildRangeFilter`         | ❌   | Rebuild range filter data           |

### Search (`search.go`)

| Method | Route     | Handler        | Auth | Description                    |
| ------ | --------- | -------------- | ---- | ------------------------------ |
| POST   | `/search` | `HandleSearch` | ❌   | Search detections with filters |

### Settings (`settings.go`)

| Method | Route                      | Handler                 | Auth | Description                    |
| ------ | -------------------------- | ----------------------- | ---- | ------------------------------ |
| GET    | `/settings`                | `GetAllSettings`        | ✅   | Get all configuration settings |
| GET    | `/settings/locales`        | `GetLocales`            | ✅   | Get available locales          |
| GET    | `/settings/imageproviders` | `GetImageProviders`     | ✅   | Get image provider options     |
| GET    | `/settings/systemid`       | `GetSystemID`           | ✅   | Get system identifier          |
| GET    | `/settings/:section`       | `GetSectionSettings`    | ✅   | Get specific settings section  |
| PUT    | `/settings`                | `UpdateSettings`        | ✅   | Update all settings            |
| PATCH  | `/settings/:section`       | `UpdateSectionSettings` | ✅   | Update settings section        |

**Quiet Hours** (`settings_audio.go`): The `realtime` settings section includes quiet hours configuration for both individual RTSP streams (`realtime.rtsp.streams[].quietHours`) and the sound card (`realtime.audio.quietHours`). Each `QuietHoursConfig` supports:

- `enabled` (bool): Enable/disable quiet hours for this source
- `mode` (`"fixed"` | `"solar"`): Time window mode
- `startTime` / `endTime` (string, `"HH:MM"`): Fixed mode start/end times
- `startEvent` / `endEvent` (`"sunrise"` | `"sunset"`): Solar mode events
- `startOffset` / `endOffset` (int, -180 to 180): Minutes offset from solar event

### Filesystem (`filesystem.go`)

| Method | Route                | Handler            | Auth | Description                                              |
| ------ | -------------------- | ------------------ | ---- | -------------------------------------------------------- |
| GET    | `/filesystem/browse` | `BrowseFileSystem` | ✅   | Browse files and directories with secure path validation |

### Species (`species.go`)

| Method | Route                      | Handler               | Auth | Description                                                       |
| ------ | -------------------------- | --------------------- | ---- | ----------------------------------------------------------------- |
| GET    | `/species`                 | `GetSpeciesInfo`      | ❌   | Get extended species information including rarity status          |
| GET    | `/species/all`             | `GetAllSpecies`       | ❌   | Get all BirdNET species labels (not filtered by location)         |
| GET    | `/species/taxonomy`        | `GetSpeciesTaxonomy`  | ❌   | Get detailed taxonomy data with subspecies and hierarchy          |
| GET    | `/species/:code/thumbnail` | `GetSpeciesThumbnail` | ❌   | Get bird thumbnail image by species code (redirects to image URL) |

### Server-Sent Events (`sse.go`)

| Method | Route                 | Handler             | Auth | Description                  |
| ------ | --------------------- | ------------------- | ---- | ---------------------------- |
| GET    | `/detections/stream`  | `StreamDetections`  | ❌⚡ | Real-time detection stream   |
| GET    | `/soundlevels/stream` | `StreamSoundLevels` | ❌⚡ | Real-time audio level stream |
| GET    | `/sse/status`         | `GetSSEStatus`      | ❌   | SSE connection status        |

### Audio Level SSE (`audio_level.go`)

| Method | Route                  | Handler            | Auth | Description               |
| ------ | ---------------------- | ------------------ | ---- | ------------------------- |
| GET    | `/streams/audio-level` | `StreamAudioLevel` | ❌   | Real-time audio level SSE |

**Features:**

- Real-time audio level data for UI audio indicators (0-100 with clipping detection)
- Automatic source anonymization for unauthenticated clients
- Connection limiting: up to 5 concurrent connections per client IP (allows multiple browser tabs)
- Maximum connection duration: 30 minutes
- Heartbeat interval: 10 seconds

**Event Format:**

```json
{
  "type": "audio-level",
  "levels": {
    "source_id_1": {
      "level": 45,
      "name": "Audio Source Name",
      "source": "source_id_1",
      "clipping": false
    }
  }
}
```

### HLS Streaming (`audio_hls.go`)

| Method | Route                                  | Handler            | Auth | Description                   |
| ------ | -------------------------------------- | ------------------ | ---- | ----------------------------- |
| POST   | `/streams/hls/:sourceID/start`         | `StartHLSStream`   | ✅   | Start HLS stream for source   |
| POST   | `/streams/hls/:sourceID/stop`          | `StopHLSStream`    | ✅   | Stop HLS stream               |
| POST   | `/streams/hls/heartbeat`               | `HLSHeartbeat`     | ❌   | Keep HLS stream alive         |
| GET    | `/streams/hls/status`                  | `GetHLSStatus`     | ❌   | Get status of all HLS streams |
| GET    | `/streams/hls/:sourceID/playlist.m3u8` | `ServeHLSPlaylist` | ❌   | Get HLS playlist              |
| GET    | `/streams/hls/:sourceID/*`             | `ServeHLSContent`  | ❌   | Serve HLS segments and init   |

**Start HLS Stream:**

- `POST /api/v2/streams/hls/{URL-encoded-sourceID}/start`
- Optional query param: `?force=true` to restart an existing stream

**Start HLS Stream Response:**

```json
{
  "status": "ready",
  "source": "rtsp%3A%2F%2Fcamera.local%3A554%2Fstream",
  "playlist_url": "/api/v2/streams/hls/rtsp%3A%2F%2Fcamera.local%3A554%2Fstream/playlist.m3u8",
  "active_clients": 1,
  "playlist_ready": true
}
```

**Heartbeat Request:**

```json
{
  "source_id": "rtsp://camera.local:554/stream",
  "client_id": "optional-client-id"
}
```

**Status Response:**

```json
{
  "streams": [
    {
      "status": "active",
      "source": "rtsp%3A%2F%2Fcamera.local%3A554%2Fstream",
      "playlist_url": "/api/v2/streams/hls/...",
      "active_clients": 2,
      "playlist_ready": true
    }
  ],
  "count": 1
}
```

**Features:**

- FFmpeg-based HLS streaming with AAC audio encoding
- Automatic stream cleanup after 5 minutes of inactivity
- Stream reuse: existing healthy streams are reused for new clients
- Client tracking with heartbeat-based keep-alive
- Secure file serving with path validation
- Cross-platform support (FIFO on Unix, stdin pipe on Windows)
- Configurable bitrate (16-320 kbps), sample rate, and segment length

**Configuration (via settings):**

- `BitRate`: Audio bitrate in kbps (default: 128, range: 16-320)
- `SampleRate`: Audio sample rate in Hz (default: 48000)
- `SegmentLength`: HLS segment duration in seconds (default: 2, range: 1-30)
- `FfmpegLogLevel`: FFmpeg log level (default: "warning")

### Stream Health Monitoring (`streams_health.go`)

| Method | Route                    | Handler                   | Auth | Description                                               |
| ------ | ------------------------ | ------------------------- | ---- | --------------------------------------------------------- |
| GET    | `/streams/health`        | `GetAllStreamsHealth`     | ✅   | Get detailed health status of all RTSP streams            |
| GET    | `/streams/health/:url`   | `GetStreamHealth`         | ✅   | Get detailed health status of a specific RTSP stream      |
| GET    | `/streams/status`        | `GetStreamsStatusSummary` | ✅   | Get high-level summary of all stream statuses with counts |
| GET    | `/streams/health/stream` | `StreamHealthUpdates`     | ✅⚡ | Real-time stream health updates via SSE                   |

### Quiet Hours Status (`quiet_hours.go`)

| Method | Route                         | Handler               | Auth | Description                                               |
| ------ | ----------------------------- | --------------------- | ---- | --------------------------------------------------------- |
| GET    | `/streams/quiet-hours/status` | `GetQuietHoursStatus` | ✅   | Get current quiet hours suppression state for all sources |

**Response Format:**

```json
{
  "anyActive": true,
  "soundCardSuppressed": false,
  "suppressedStreams": {
    "rtsp://camera1.local:554/stream": true
  }
}
```

- `anyActive`: true if any source (sound card or stream) is currently suppressed by quiet hours
- `soundCardSuppressed`: true if the sound card is currently in quiet hours
- `suppressedStreams`: map of stream URLs to their suppression state (only suppressed streams included)

### Support (`support.go`)

| Method | Route                   | Handler               | Auth | Description                      |
| ------ | ----------------------- | --------------------- | ---- | -------------------------------- |
| POST   | `/support/generate`     | `GenerateSupportDump` | ✅   | Generate support diagnostic dump |
| GET    | `/support/download/:id` | `DownloadSupportDump` | ✅   | Download support dump            |
| GET    | `/support/status`       | `GetSupportStatus`    | ✅   | Support system status            |

### System Information (`system.go`)

| Method | Route                            | Handler                   | Auth | Description                          |
| ------ | -------------------------------- | ------------------------- | ---- | ------------------------------------ |
| GET    | `/system/info`                   | `GetSystemInfo`           | ✅   | General system information           |
| GET    | `/system/resources`              | `GetResourceInfo`         | ✅   | Resource usage information           |
| GET    | `/system/disks`                  | `GetDiskInfo`             | ✅   | Disk usage information               |
| GET    | `/system/jobs`                   | `GetJobQueueStats`        | ✅   | Job queue statistics                 |
| GET    | `/system/processes`              | `GetProcessInfo`          | ✅   | Process information                  |
| GET    | `/system/temperature/cpu`        | `GetSystemCPUTemperature` | ✅   | CPU temperature                      |
| GET    | `/system/audio/devices`          | `GetAudioDevices`         | ✅   | Available audio devices              |
| GET    | `/system/audio/active`           | `GetActiveAudioDevice`    | ✅   | Active audio device                  |
| GET    | `/system/audio/equalizer/config` | `GetEqualizerConfig`      | ✅   | Audio equalizer filter configuration |

### Events (`events.go`, `events_aggregation.go`)

Registered under the system route group. All endpoints require authentication.

| Method | Route                        | Handler                | Auth | Description                                               |
| ------ | ---------------------------- | ---------------------- | ---- | --------------------------------------------------------- |
| GET    | `/system/events/detections`  | `GetDetectionEvents`   | ✅   | Detection lifecycle events aggregated into hourly buckets |
| GET    | `/system/events/operational` | `GetOperationalEvents` | ✅   | Operational log events (from application and audio logs)  |

**Query Parameters:**

- `GET /system/events/detections`: `date` (YYYY-MM-DD, defaults to today)
- `GET /system/events/operational`: `date` (YYYY-MM-DD, defaults to today), `level` (DEBUG/INFO/WARN/ERROR, defaults to INFO)

### Weather (`weather.go`)

| Method | Route                         | Handler                   | Auth | Description                         |
| ------ | ----------------------------- | ------------------------- | ---- | ----------------------------------- |
| GET    | `/weather/daily/:date`        | `GetDailyWeather`         | ❌   | Daily weather data                  |
| GET    | `/weather/hourly/:date`       | `GetHourlyWeatherForDay`  | ❌   | Hourly weather for day              |
| GET    | `/weather/hourly/:date/:hour` | `GetHourlyWeatherForHour` | ❌   | Specific hour weather               |
| GET    | `/weather/detection/:id`      | `GetWeatherForDetection`  | ❌   | Weather for detection time          |
| GET    | `/weather/latest`             | `GetLatestWeather`        | ❌   | Latest weather data                 |
| GET    | `/weather/sun/:date`          | `GetSunTimes`             | ❌   | Sun times (sunrise/sunset) for date |

### Alert Rules (`alerts.go`)

Requires enhanced (v2) database. Returns 409 Conflict if not available.

| Method | Route                          | Handler                  | Auth | Description              |
| ------ | ------------------------------ | ------------------------ | ---- | ------------------------ |
| GET    | `/alerts/schema`               | `GetAlertSchema`         | ❌   | Alert schema for UI      |
| GET    | `/alerts/rules`                | `ListAlertRules`         | ❌   | List rules (filterable)  |
| GET    | `/alerts/rules/:id`            | `GetAlertRule`           | ❌   | Get single rule          |
| GET    | `/alerts/rules/export`         | `ExportAlertRules`       | ✅   | Export rules as JSON     |
| GET    | `/alerts/history`              | `ListAlertHistory`       | ❌   | List history (paginated) |
| POST   | `/alerts/rules`                | `CreateAlertRule`        | ✅   | Create rule              |
| PUT    | `/alerts/rules/:id`            | `UpdateAlertRule`        | ✅   | Replace rule             |
| PATCH  | `/alerts/rules/:id/toggle`     | `ToggleAlertRule`        | ✅   | Enable/disable rule      |
| DELETE | `/alerts/rules/:id`            | `DeleteAlertRule`        | ✅   | Delete rule              |
| POST   | `/alerts/rules/:id/test`       | `TestAlertRule`          | ✅   | Test-fire rule           |
| POST   | `/alerts/rules/reset-defaults` | `ResetDefaultAlertRules` | ✅   | Re-seed built-in rules   |
| POST   | `/alerts/rules/import`         | `ImportAlertRules`       | ✅   | Import rules from JSON   |
| DELETE | `/alerts/history`              | `ClearAlertHistory`      | ✅   | Delete all history       |

**Query Parameters:**

- `GET /alerts/rules`: `object_type`, `enabled` (true/false), `built_in` (true/false)
- `GET /alerts/history`: `rule_id`, `limit` (default 50), `offset`

### Insights (`insights.go`)

Requires enhanced (v2) database. Returns 409 Conflict if not available.

| Method | Route                               | Handler                    | Auth | Description                              |
| ------ | ----------------------------------- | -------------------------- | ---- | ---------------------------------------- |
| GET    | `/insights/expected-today`          | `getExpectedToday`         | ❌   | Species expected today based on history  |
| GET    | `/insights/expected-today/regional` | `getExpectedTodayRegional` | ❌   | Regional eBird observations near station |
| GET    | `/insights/phantom-species`         | `getPhantomSpecies`        | ❌   | Frequent but low-confidence detections   |
| GET    | `/insights/dawn-chorus`             | `getDawnChorus`            | ❌   | Dawn chorus timing analysis              |
| GET    | `/insights/migration`               | `getMigration`             | ❌   | New arrivals and gone-quiet species      |
| GET    | `/dashboard/kpis`                   | `getDashboardKPIs`         | ❌   | Dashboard headline metrics and streak    |

**Query Parameters:**

- All insights endpoints accept optional `model_id` query parameter to filter by BirdNET model

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
// Use structured logging with type-safe field constructors
c.log.Info("operation completed",
    logger.String("key", value),
    logger.String("ip", ctx.RealIP()))
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
- Stream health streams: 5 requests/minute per IP (authenticated)
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
```

## Stream Health Monitoring API

The Stream Health API provides comprehensive real-time monitoring of RTSP stream status, leveraging the FFmpeg error detection system from PR #1380. These endpoints are designed to be safe for use in monitoring dashboards and provide actionable diagnostics for troubleshooting stream issues.

### Key Features

- **Real-time Health Status**: Monitor connection state, data flow, and process health
- **Detailed Error Diagnostics**: Get user-friendly error messages with troubleshooting steps
- **Error History**: Track last 10 errors per stream for pattern analysis
- **State Transitions**: See recent state changes (idle → starting → running → circuit_open, etc.)
- **Circuit Breaker Status**: Understand why streams are not attempting to reconnect
- **Credential Safety**: All URLs are sanitized before being returned in responses

### Example Responses

#### GET /api/v2/streams/health

Returns health information for all configured RTSP streams as an array:

```json
[
  {
    "url": "rtsp://camera1.local:554/stream",
    "is_healthy": true,
    "process_state": "running",
    "last_data_received": "2025-10-12T14:30:45Z",
    "time_since_data_seconds": 2.5,
    "restart_count": 0,
    "total_bytes_received": 1048576,
    "bytes_per_second": 128000.5,
    "is_receiving_data": true
  },
  {
    "url": "rtsp://camera2.local:554/stream",
    "is_healthy": false,
    "process_state": "circuit_open",
    "last_data_received": null,
    "restart_count": 5,
    "error": "RTSP stream not found (404)",
    "total_bytes_received": 0,
    "bytes_per_second": 0,
    "is_receiving_data": false,
    "last_error_context": {
      "error_type": "rtsp_404",
      "primary_message": "method DESCRIBE failed: 404 Not Found",
      "user_facing_msg": "📹 RTSP stream not found (404)\n   The RTSP server responded with 404 Not Found during DESCRIBE method.",
      "troubleshooting_steps": [
        "Check if the stream name is correct (case-sensitive)",
        "Verify the stream path in your RTSP URL",
        "List available streams on the RTSP server",
        "Confirm the stream is published and active"
      ],
      "timestamp": "2025-10-12T14:28:10Z",
      "target_host": "camera2.local",
      "target_port": 554,
      "http_status": 404,
      "rtsp_method": "DESCRIBE",
      "should_open_circuit": true,
      "should_restart": false
    },
    "error_history": [
      {
        "error_type": "rtsp_404",
        "primary_message": "method DESCRIBE failed: 404 Not Found",
        "timestamp": "2025-10-12T14:28:10Z"
      }
    ],
    "state_history": [
      {
        "from_state": "starting",
        "to_state": "circuit_open",
        "timestamp": "2025-10-12T14:28:10Z",
        "reason": "permanent failure detected"
      }
    ]
  }
]

**Note:** Returns an array to handle cases where multiple streams with different credentials point to the same RTSP URL (which would have identical sanitized URLs).
```

#### GET /api/v2/streams/health/:url

Get health for a specific stream (URL must be URL-encoded):

```bash
# Example: Get health for rtsp://camera1.local:554/stream
curl "http://localhost:8080/api/v2/streams/health/rtsp%3A%2F%2Fcamera1.local%3A554%2Fstream"
```

Returns the same structure as a single stream from the `/streams/health` endpoint.

#### GET /api/v2/streams/status

Returns a high-level summary for dashboard displays:

```json
{
  "total_streams": 3,
  "healthy_streams": 2,
  "unhealthy_streams": 1,
  "streams_summary": [
    {
      "url": "rtsp://camera1.local:554/stream",
      "is_healthy": true,
      "process_state": "running",
      "time_since_data_seconds": 2.5
    },
    {
      "url": "rtsp://camera2.local:554/stream",
      "is_healthy": false,
      "process_state": "circuit_open",
      "last_error_type": "rtsp_404"
    },
    {
      "url": "rtsp://camera3.local:554/stream",
      "is_healthy": true,
      "process_state": "running",
      "time_since_data_seconds": 1.2
    }
  ],
  "timestamp": "2025-10-12T14:30:00Z"
}
```

### Process States

The `process_state` field can have these values:

- `idle`: Stream created but not yet started
- `starting`: FFmpeg process is being launched
- `running`: Stream is active and processing audio
- `restarting`: Restart has been requested
- `backoff`: Waiting before restart (exponential backoff)
- `circuit_open`: Circuit breaker is open (permanent failure detected, waiting for cooldown)
- `stopped`: Stream has been permanently stopped

### Error Types

The API reports these error types (from PR #1380):

| Error Type                | Permanent | Description                       |
| ------------------------- | --------- | --------------------------------- |
| `connection_timeout`      | No        | Connection timed out (will retry) |
| `rtsp_404`                | Yes       | Stream not found (404)            |
| `connection_refused`      | Yes       | Connection refused by server      |
| `auth_failed`             | Yes       | Authentication required (401)     |
| `auth_forbidden`          | Yes       | Access forbidden (403)            |
| `no_route`                | Yes       | No route to host                  |
| `dns_resolution_failed`   | Yes       | DNS lookup failed                 |
| `network_unreachable`     | No        | Network unreachable (transient)   |
| `operation_not_permitted` | Yes       | Operation not permitted           |
| `ssl_error`               | Yes       | SSL/TLS error                     |
| `rtsp_503`                | No        | Service unavailable (503)         |
| `invalid_data`            | No        | Invalid/corrupted data            |
| `eof`                     | No        | Unexpected end of file            |
| `protocol_error`          | Yes       | Protocol not supported            |

### Real-Time Stream Health Updates (SSE)

#### GET /api/v2/streams/health/stream

**Authentication:** Required
**Rate Limit:** 5 connections per minute per IP
**Connection Duration:** Maximum 30 minutes

Establishes a Server-Sent Events (SSE) connection that pushes real-time updates when stream health changes. This is more efficient than polling for monitoring dashboards that need immediate notification of stream issues.

**Event Types:**

- `stream_added` - New stream detected
- `stream_removed` - Stream configuration removed
- `state_change` - Process state changed (e.g., running → circuit_open)
- `health_recovered` - Stream returned to healthy state
- `health_degraded` - Stream became unhealthy
- `error_detected` - New error occurred
- `stream_restarted` - Restart count increased
- `data_flow_resumed` - Data started flowing again
- `data_flow_stopped` - Data flow stopped
- `status_update` - General status update
- `heartbeat` - Keep-alive message (sent every 30 seconds)
- `connected` - Initial connection established

**Event Format:**

```json
event: stream_health
data: {
  "url": "rtsp://camera1.local:554/stream",
  "is_healthy": false,
  "process_state": "circuit_open",
  "last_data_received": null,
  "restart_count": 3,
  "error": "RTSP stream not found (404)",
  "total_bytes_received": 0,
  "bytes_per_second": 0,
  "is_receiving_data": false,
  "last_error_context": {
    "error_type": "rtsp_404",
    "primary_message": "method DESCRIBE failed: 404 Not Found",
    "user_facing_msg": "📹 RTSP stream not found (404)\n   The RTSP server responded with 404 Not Found during DESCRIBE method.",
    "troubleshooting_steps": [
      "Check if the stream name is correct (case-sensitive)",
      "Verify the stream path in your RTSP URL"
    ],
    "timestamp": "2025-10-12T14:28:10Z",
    "should_open_circuit": true,
    "should_restart": false
  },
  "event_type": "error_detected"
}
```

**Connection Example (JavaScript/Browser):**

```javascript
const eventSource = new EventSource("/api/v2/streams/health/stream", {
  withCredentials: true, // Include authentication cookies
});

eventSource.addEventListener("stream_health", (event) => {
  const data = JSON.parse(event.data);
  console.log("Stream update:", data.event_type, data.url, data.process_state);

  // Update UI based on event type
  if (data.event_type === "error_detected") {
    showAlert(`Stream error: ${data.last_error_context.user_facing_msg}`);
  }
});

eventSource.addEventListener("heartbeat", (event) => {
  const data = JSON.parse(event.data);
  console.log("Heartbeat:", data.timestamp, "clients:", data.clients);
});

eventSource.onerror = (error) => {
  console.error("SSE connection error:", error);
  eventSource.close();
};
```

**Change Detection:**

The SSE endpoint monitors for these changes:

- Health status changes (healthy ↔ unhealthy)
- Process state transitions
- New errors detected
- Restart count increases
- Data flow status changes

Updates are sent only when changes are detected, reducing bandwidth compared to polling.

### Integration Tips

1. **Choose the Right Endpoint**:

   - Use SSE (`/streams/health/stream`) for real-time monitoring dashboards
   - Use REST polling (`/streams/status`) for periodic background checks
   - Use REST (`/streams/health/:url`) for on-demand detailed diagnostics

2. **Polling Interval (if not using SSE)**: Poll `/streams/status` every 5-10 seconds for dashboard updates
3. **Detailed Diagnostics**: Use `/streams/health` when investigating specific issues
4. **URL Encoding**: Always URL-encode the stream URL parameter for `/streams/health/:url`
5. **Credential Safety**: All URLs in responses are automatically sanitized
6. **Error History**: Use the `error_history` array to detect recurring issues
7. **Circuit Breaker**: When `process_state` is `circuit_open`, check `last_error_context.should_open_circuit` to understand why

### Frontend Integration Example

```typescript
// Svelte store for stream health monitoring
import { writable } from "svelte/store";

interface StreamStatus {
  total_streams: number;
  healthy_streams: number;
  unhealthy_streams: number;
  streams_summary: StreamSummary[];
  timestamp: string;
}

export const streamStatus = writable<StreamStatus | null>(null);

// Poll stream status every 5 seconds
export function startStreamMonitoring() {
  const pollInterval = 5000; // 5 seconds

  async function fetchStreamStatus() {
    try {
      const response = await fetch("/api/v2/streams/status");
      if (response.ok) {
        const data = await response.json();
        streamStatus.set(data);
      }
    } catch (error) {
      console.error("Failed to fetch stream status:", error);
    }
  }

  // Initial fetch
  fetchStreamStatus();

  // Poll periodically
  const intervalId = setInterval(fetchStreamStatus, pollInterval);

  // Return cleanup function
  return () => clearInterval(intervalId);
}
```

### Troubleshooting Common Issues

**Q: Stream shows `circuit_open` state and won't reconnect**
A: Check `last_error_context` for the permanent failure reason. Fix the underlying issue (e.g., correct URL, fix authentication) and either restart BirdNET-Go or wait for the circuit breaker cooldown period (30 seconds).

**Q: `time_since_data_seconds` is increasing but stream shows healthy**
A: This indicates the stream may be stalled. The health check will automatically trigger a restart when it exceeds the configured threshold (default: 60 seconds).

**Q: Getting 404 when accessing `/streams/health/:url`**
A: Ensure the URL is properly URL-encoded. Use `encodeURIComponent()` in JavaScript or equivalent in your language.

**Q: Error history is empty even though stream has errors**
A: Error history only stores errors that occurred after the FFmpeg error detection system was initialized (PR #1380). Older errors before this feature are not tracked.
