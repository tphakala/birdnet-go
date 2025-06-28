# Notification Package

The notification package provides a centralized system for managing application notifications, including system alerts, errors, and important bird detection events.

## Features

- **Multiple notification types**: Error, Warning, Info, Detection, System
- **Priority levels**: Critical, High, Medium, Low
- **Rate limiting**: Prevents notification spam
- **Real-time broadcasting**: Subscribe to notifications via channels
- **In-memory storage**: Fast access with configurable size limits
- **Automatic cleanup**: Expired notifications are removed automatically
- **Thread-safe**: Safe for concurrent use

## Usage

### Initialization

Initialize the notification service at application startup:

```go
import "github.com/tphakala/birdnet-go/internal/notification"

// Use default configuration
notification.Initialize(nil)

// Or with custom configuration
config := &notification.ServiceConfig{
    MaxNotifications:   2000,
    CleanupInterval:    10 * time.Minute,
    RateLimitWindow:    30 * time.Second,
    RateLimitMaxEvents: 50,
}
notification.Initialize(config)
```

### Creating Notifications

Use the helper functions for common scenarios:

```go
// Error notification from an error
notification.NotifyError("database", err)

// System alert
notification.NotifySystemAlert(notification.PriorityHigh, "High CPU Usage", "CPU usage is above 80%")

// Bird detection
notification.NotifyDetection("Northern Cardinal", 0.95, map[string]interface{}{
    "location": "backyard",
    "time_of_day": "morning",
})

// Integration failure
notification.NotifyIntegrationFailure("BirdWeather", err)

// Resource alert
notification.NotifyResourceAlert("Memory", 85.5, 80.0, "%")

// Info message
notification.NotifyInfo("Update Available", "A new version of BirdNET-Go is available")

// Warning
notification.NotifyWarning("audio", "Audio Device Changed", "Audio input device has been switched")
```

### Direct Service Usage

For more control, use the service directly:

```go
service := notification.GetService()

// Create a notification with metadata
notif, err := service.Create(
    notification.TypeWarning,
    notification.PriorityMedium,
    "Disk Space Low",
    "Only 10GB of disk space remaining",
)

if err == nil && notif != nil {
    notif.WithComponent("storage").
        WithMetadata("disk_free_gb", 10).
        WithMetadata("disk_total_gb", 100).
        WithExpiry(1 * time.Hour)
    
    service.store.Update(notif)
}
```

### Subscribing to Notifications

Subscribe to receive real-time notifications:

```go
service := notification.GetService()
ch := service.Subscribe()
defer service.Unsubscribe(ch)

go func() {
    for notif := range ch {
        fmt.Printf("New notification: %s - %s\n", notif.Title, notif.Message)
    }
}()
```

### Querying Notifications

List and filter notifications:

```go
service := notification.GetService()

// Get all unread notifications
unreadNotifs, _ := service.List(&notification.FilterOptions{
    Status: []notification.Status{notification.StatusUnread},
})

// Get critical errors from the last hour
since := time.Now().Add(-1 * time.Hour)
criticalErrors, _ := service.List(&notification.FilterOptions{
    Types:      []notification.Type{notification.TypeError},
    Priorities: []notification.Priority{notification.PriorityCritical},
    Since:      &since,
})

// Get unread count
count, _ := service.GetUnreadCount()
```

### Managing Notification Status

```go
service := notification.GetService()

// Mark as read
service.MarkAsRead(notificationID)

// Mark as acknowledged
service.MarkAsAcknowledged(notificationID)

// Delete a notification
service.Delete(notificationID)
```

## Integration with Error Handler

The notification system integrates seamlessly with the enhanced error handler:

```go
// In error handling code
err := errors.New("database connection failed").
    Component("database").
    Category(errors.CategoryDatabase).
    Build()

// This will automatically create an appropriate notification
notification.NotifyError("database", err)
```

## Best Practices

1. **Initialize early**: Set up the notification service during application startup
2. **Use appropriate priorities**: Reserve Critical for urgent issues requiring immediate attention
3. **Set expiration times**: Use `WithExpiry()` for temporary notifications
4. **Add metadata**: Include relevant context in metadata for debugging
5. **Handle rate limits**: Check for rate limit errors when creating many notifications
6. **Clean up subscribers**: Always unsubscribe channels when done to prevent leaks

## Notification Types Guide

- **TypeError**: System errors, failures, exceptions
- **TypeWarning**: Potential issues, degraded performance, threshold violations
- **TypeInfo**: General information, status updates, confirmations
- **TypeDetection**: Bird detection events, rare species alerts
- **TypeSystem**: System status changes, startup/shutdown events

## Priority Levels Guide

- **PriorityCritical**: Immediate action required (system failure, data loss risk)
- **PriorityHigh**: Important but not urgent (service disruption, repeated failures)
- **PriorityMedium**: Normal priority (warnings, standard errors)
- **PriorityLow**: Informational only (status updates, confirmations)