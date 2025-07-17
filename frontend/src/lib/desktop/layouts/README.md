# Layout Components

This directory contains the main layout components for the BirdNET-Go Svelte UI, which runs at `/ui/` routes alongside the existing HTMX interface.

## Components

### RootLayout.svelte

The main layout wrapper that provides the overall application structure with:

- DaisyUI drawer layout for responsive navigation
- Header with search, notifications, audio level indicator, and theme toggle
- Sidebar navigation with collapsible sections
- Global loading indicator
- Login modal placeholder
- CSRF token management
- Theme persistence

**Usage:**

```svelte
<RootLayout
  title="Page Title"
  currentPage="dashboard"
  securityEnabled={false}
  accessAllowed={true}
  version="1.0.0"
>
  <!-- Page content -->
</RootLayout>
```

### Sidebar.svelte

Navigation sidebar component with:

- Responsive drawer behavior (mobile) and fixed sidebar (desktop)
- Route-based active state highlighting
- Collapsible Analytics and Settings sections
- Security-based menu visibility
- Login/logout functionality
- Version display

**Props:**

- `securityEnabled`: Whether security features are enabled
- `accessAllowed`: Whether the user has access to admin features
- `version`: Application version to display
- `currentRoute`: Current route path for active state
- `onNavigate`: Optional navigation handler

### Header.svelte

Top header bar with:

- Mobile menu toggle button
- Page title display
- Centered search box (dashboard only)
- Audio level indicator
- Notification bell with dropdown
- Theme toggle (desktop only)

**Props:**

- `title`: Page title to display
- `currentPage`: Current page for conditional features
- `securityEnabled`: Security state
- `accessAllowed`: Access state
- `showSidebarToggle`: Show/hide menu toggle
- `onSidebarToggle`: Handler for menu toggle
- `onSearch`: Search handler
- `onNavigate`: Navigation handler

## Route Structure

The new Svelte UI uses `/ui/` prefixed routes:

- `/ui/` or `/ui/dashboard` - Dashboard
- `/ui/analytics` - Analytics overview
- `/ui/analytics/species` - Species analytics
- `/ui/search` - Search page
- `/ui/about` - About page
- `/ui/system` - System dashboard
- `/ui/settings/*` - Settings pages

## Integration with App.svelte

The `App.svelte` component:

1. Determines the current route from the URL
2. Sets appropriate page title and route state
3. Wraps all content in `RootLayout`
4. Renders the appropriate page component

## Theme Management

Themes are managed through:

- localStorage persistence
- Immediate application on page load
- Integration with DaisyUI's theme system
- Theme toggle component in header

## Security Integration

The layout respects security settings:

- Admin menu items hidden when security enabled and no access
- Login/logout buttons shown based on auth state
- Auth state managed via auth store

## Mobile Responsiveness

- Drawer overlay on mobile devices
- Fixed sidebar on desktop (lg breakpoint)
- Responsive header elements
- Touch-friendly navigation
