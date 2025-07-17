# Route Setup Summary

## Current Route Configuration

### HTMX Routes (Original UI)

- `/` - Dashboard
- `/dashboard` - Dashboard
- `/analytics` - Analytics overview
- `/analytics/species` - Species analytics
- `/search` - Search page
- `/about` - About page
- `/notifications` - Notifications page
- `/system` - System dashboard (auth required)
- `/settings/*` - Settings pages (auth required)

### Svelte UI Routes (New UI at `/ui/`)

- `/ui` - Dashboard (redirects to dashboard)
- `/ui/` - Dashboard (redirects to dashboard)
- `/ui/dashboard` - Dashboard
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

### Asset Routes

- `/assets/*` - Original HTMX assets (CSS, JS, images)
- `/svelte-assets/*` - Svelte UI assets (JS, CSS from dist/)

## Route Handlers

### Go Backend Files

#### `htmx_routes.go`

- Defines all page routes in `pageRoutes` map
- Sets up full page route handlers
- Includes both HTMX and Svelte UI routes
- Handles authentication middleware for protected routes

#### `svelte_handler.go`

- Sets up Svelte asset serving at `/svelte-assets/*`
- Serves built files from `frontend.DistFS`
- Sets correct MIME types for JS/CSS files

#### `ui_migration.go`

- Provides migration utilities
- Allows gradual migration from HTMX to Svelte
- Supports automatic redirects (currently disabled)

### Frontend Files

#### `svelte-wrapper.html`

- Template that loads Svelte app within HTMX layout
- Dynamically loads Svelte CSS and JS
- Passes server configuration to Svelte app
- Provides `#app` mount point

#### `App.svelte`

- Main Svelte application component
- Reads server config from `window.BIRDNET_CONFIG`
- Handles client-side routing based on URL path
- Wraps content in `RootLayout` component

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

## Current Status

✅ **Working:**

- All `/ui/*` routes properly configured
- Svelte assets served correctly
- Server configuration passed to frontend
- Authentication middleware applied to protected routes
- Side-by-side operation with HTMX UI

⚠️ **Needs API v2:**

- Most Svelte components expect v2 API endpoints
- Dashboard, Analytics, Search, etc. need corresponding backend APIs
- Currently shows placeholder/loading states

## Next Steps

1. **Implement API v2 endpoints** for data fetching
2. **Test route navigation** in browser
3. **Add error handling** for missing API endpoints
4. **Consider migration strategy** for moving users to new UI
