# Route Configuration

## Application Routes

The BirdNET-Go frontend uses a Svelte 5 SPA (Single Page Application) with client-side routing served by the Go backend.

### UI Routes

All UI routes are served from the `/ui/` prefix:

- `/ui/` or `/ui/dashboard` - Dashboard
- `/ui/analytics` - Analytics overview
- `/ui/analytics/species` - Species analytics
- `/ui/search` - Search page
- `/ui/about` - About page
- `/ui/notifications` - Notifications page
- `/ui/system` - System dashboard (auth required)
- `/ui/settings` - Settings overview (auth required)
- `/ui/settings/main` - Main settings (auth required)
- `/ui/settings/audio` - Audio settings (auth required)
- `/ui/settings/detectionfilters` - Detection filters (auth required)
- `/ui/settings/integrations` - Integration settings (auth required)
- `/ui/settings/security` - Security settings (auth required)
- `/ui/settings/species` - Species settings (auth required)
- `/ui/settings/support` - Support settings (auth required)

### API Routes

All API endpoints are under `/api/v2/`:

- `/api/v2/detections` - Detection CRUD operations
- `/api/v2/analytics/*` - Analytics and statistics
- `/api/v2/settings/*` - Settings management
- `/api/v2/media/*` - Media (audio, spectrograms, images)
- `/api/v2/system/*` - System information
- `/api/v2/streams/*` - Audio streaming (HLS, levels)
- `/api/v2/auth/*` - Authentication endpoints

See `internal/api/README.md` for complete API documentation.

### Asset Routes

- `/ui/assets/*` - Static assets (images, icons, sounds) served from `frontend/static/`
- Frontend JS/CSS embedded in Go binary at build time

## Architecture

### Go Backend

The server implementation is in `internal/api/`:

- `server.go` - Main HTTP server (Echo framework)
- `spa.go` - SPA handler for client-side routing
- `static.go` - Static file serving for embedded assets
- `v2/` - API v2 handlers and routes

### Frontend (Svelte 5)

- `App.svelte` - Main application component with client-side routing
- Routes are determined by `window.location.pathname`
- Components are lazy-loaded based on current route
- Server configuration passed via `window.BIRDNET_CONFIG`

## Configuration Passing

Server configuration is passed from Go to Svelte via `window.BIRDNET_CONFIG`:

```javascript
window.BIRDNET_CONFIG = {
  csrfToken: '{{.CSRFToken}}',
  security: {
    enabled: {{.Security.Enabled}},
    accessAllowed: {{.Security.AccessAllowed}}
  },
  version: '{{.Settings.Version}}',
  currentPath: window.location.pathname
};
```

## Authentication

Protected routes require authentication:

- Settings pages require authentication
- System dashboard requires authentication
- Auth state is managed via session cookies
- Local subnet bypass available for trusted networks

See `internal/api/auth/` for authentication implementation details.
