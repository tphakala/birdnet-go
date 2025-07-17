# NotificationBell Component

A Svelte 5 TypeScript component that displays real-time notifications with a dropdown interface and unread count badge.

## Features

- Real-time notification updates via SSE (Server-Sent Events)
- Unread notification count badge
- Dropdown panel with notification list
- Notification type icons (error, warning, info, detection, system)
- Priority-based styling and filtering
- Mark as read functionality
- Browser notification support (with permission)
- Sound notifications for high-priority items
- Debug mode for showing all notification types
- Automatic reconnection with exponential backoff
- Responsive design with mobile support

## Usage

```svelte
<script>
  import NotificationBell from '$lib/components/ui/NotificationBell.svelte';
</script>

<NotificationBell
  debugMode={false}
  onNavigateToNotifications={() => (window.location.href = '/notifications')}
/>
```

## Props

- `className` (string, optional): Additional CSS classes for the container
- `debugMode` (boolean, optional): Show all notifications including system/error messages
- `onNavigateToNotifications` (function, optional): Custom navigation handler for "View all" button

## API Integration

Uses v2 API endpoints:

- `/api/v2/notifications?limit=20&status=unread` - Fetch unread notifications
- `/api/v2/notifications/stream` - SSE endpoint for real-time updates
- `/api/v2/notifications/{id}/read` - Mark notification as read

## Notification Types

The component supports different notification types with corresponding icons:

- `error` - Error icon with red styling
- `warning` - Warning triangle with amber styling
- `info` - Info icon with blue styling
- `detection` - Star icon with green styling (bird detections)
- `system` - Gear icon with primary color styling

## Priority Levels

Notifications can have different priority levels:

- `critical` - Red badge, triggers browser notification
- `high` - Amber badge, plays sound if enabled
- `medium` - Blue badge
- `low` - Ghost badge

## Features in Detail

### Real-time Updates

The component connects to an SSE endpoint for real-time notification updates. It automatically reconnects with exponential backoff if the connection is lost.

### Filtering

- In normal mode: Shows user-facing notifications (detections, critical, high priority)
- In debug mode: Shows all notifications including system and error messages

### Sound Notifications

- Plays a sound for high-priority notifications if enabled
- Sound preference is stored in localStorage

### Browser Notifications

- Requests permission on first user interaction
- Shows browser notifications for critical priority items

### Unread Management

- Displays unread count badge (max 99+)
- Mark individual notifications as read
- Mark all as read functionality
- Syncs with other components via custom events

## Events

The component listens for:

- `notification-deleted` - Custom event to sync with other components when notifications are deleted

## Implementation Notes

1. **SSE Connection**: Maintains a persistent EventSource connection for real-time updates
2. **Reconnection**: Implements exponential backoff (1s → 30s max) for connection failures
3. **Performance**: Limits display to 20 most recent notifications
4. **Accessibility**: Full ARIA labels and keyboard navigation support
5. **Animation**: Bell wiggles when new notifications arrive

## Browser Support

- Modern browsers with EventSource support
- Notification API for browser notifications (optional)
- localStorage for preferences

## Security Considerations

- CSRF token support for API requests
- Respects browser notification permissions
- No sensitive data stored in localStorage

## Styling

The component uses DaisyUI classes and can be customized with:

- Custom `className` prop for container styling
- CSS variables for theme integration
- Responsive design with mobile-first approach

## Example with Full Header

```svelte
<header class="navbar bg-base-100">
  <div class="navbar-start">
    <!-- Mobile menu button -->
  </div>
  <div class="navbar-center">
    <h1 class="text-xl font-bold">Dashboard</h1>
  </div>
  <div class="navbar-end flex gap-2">
    <AudioLevelIndicator />
    <NotificationBell debugMode={false} />
    <ThemeToggle />
  </div>
</header>
```
