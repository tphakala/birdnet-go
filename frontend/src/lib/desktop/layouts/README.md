# Layout Components

This directory contains the layout components for the BirdNET-Go Svelte 5 desktop and tablet UI: the page shell (`RootLayout`), the header and the navigation sidebar.

## Components

### RootLayout.svelte

The layout wrapper that `App.svelte` puts around every page. It provides:

- A drawer layout: a fixed sidebar from the `lg` breakpoint (1024 px) up, and an overlay drawer below it, toggled by the header menu button
- The header (`DesktopHeader`) and the sidebar (`DesktopSidebar`)
- The offline banner and the restart-required banner above the page content
- The page content area (`#mainContent`, a 12-column grid inside a `max-w-7xl` container) that renders the `children` snippet
- A global toast container
- An empty `<dialog id="loginModal">` placeholder; the real login modal is rendered by the sidebar
- On mount: initializes the auth store, fetches the restart status (skipped while authentication is required and not yet granted), restores the saved theme from `localStorage`, and redirects to HTTPS when `data-https-redirect="true"` is set on the document element (not on `localhost` or `127.0.0.1`)

**Props:**

- `title`: page title shown in the header
- `currentPage`: page id; the search box is shown when it is `dashboard` or `detections`
- `currentPath`: current path, used by the sidebar for active highlighting (defaults to `/ui/<currentPage>`)
- `securityEnabled`, `accessAllowed`: security state, passed to the header and sidebar
- `version`: version string shown in the sidebar footer
- `authConfig`: enabled login methods, passed to the login modal
- `onNavigate`: navigation callback; without it, navigation is a full page load
- `className`, `children`

**Usage:**

```svelte
<RootLayout
  title="Page Title"
  currentPage="dashboard"
  currentPath="/ui/dashboard"
  {securityEnabled}
  {accessAllowed}
  {version}
  {authConfig}
  onNavigate={navigate}
>
  <!-- Page content -->
</RootLayout>
```

### DesktopHeader.svelte

The top bar, laid out as three sections:

- Left: a menu button that toggles the drawer (hidden at `lg` and wider) and the page title
- Center: the search box when `showSearch` is true, otherwise a spacer that keeps the layout stable
- Right: the quiet hours indicator, the audio level indicator, the notification bell, and the settings menu (shown from the `md` breakpoint up, it holds the theme toggle)

**Props:**

- `title`, `currentPage`
- `showSidebarToggle`, `showSearch`, `showAudioLevel`, `showNotifications`: show or hide the matching element (all default to true)
- `debugMode`: passed to the notification bell
- `securityEnabled`, `accessAllowed`: passed to the settings menu
- `onSidebarToggle`, `onSearch`, `onNavigate`: callbacks; without `onNavigate` the header uses the navigation store
- `className`

### DesktopSidebar.svelte

The navigation sidebar, 256 px wide, or 64 px when collapsed. It contains:

- A logo button that goes to the dashboard, and a collapse toggle (desktop only). The collapsed state is kept in the `sidebar` store and persisted in `localStorage` under `sidebar-collapsed`.
- A Dashboard item, and a Live Audio item when `hasLiveAudioAccess()` is true
- Two flat sections, rendered for every visitor: Explore (Summary, Species, Search) and Patterns (Activity, Trends, Nocturnal, Biodiversity). The analytics links carry the current analytics filter query string. The weather, soundscape and review analytics pages are not in the menu.
- When security is off or access is allowed: collapsible System, Help (Help & Support, About, Report Bug, and an external Ask a Question link to the discussions) and Settings sections
- A footer with Logout (security enabled and access allowed) or Login (security enabled and access not allowed), and the version, which links to the repository when the sidebar is expanded

Behavior:

- The active item is derived from `currentRoute`, after resolving settings aliases. The System, Help and Settings sections expand automatically when the route is inside them, and collapse when the sidebar collapses.
- When collapsed, items show only icons with fixed-position tooltips, and each collapsible section opens a flyout submenu.
- Navigating to the dashboard resets the persisted date to today.
- The Login button opens `LoginModal` with the current path and query as the post-login redirect.

**Props:**

- `securityEnabled`, `accessAllowed`, `version`, `authConfig`
- `currentRoute`: current path for active highlighting
- `onNavigate`: navigation callback; without it the sidebar uses the navigation store
- `className`

### CollapsibleNavSection.svelte, NavFlatItem.svelte, NavSectionHeader.svelte

Building blocks used by the sidebar:

- `CollapsibleNavSection`: a section with an expandable list when the sidebar is expanded, or an icon with a flyout menu when it is collapsed. Items are internal buttons (`NavButtonItem`) or external links (`NavLinkItem`).
- `NavFlatItem`: a single navigation button with an icon, a label and an active state; icon-only with a tooltip when collapsed.
- `NavSectionHeader`: a small uppercase group label that becomes screen-reader-only when the sidebar is collapsed.

## Route Structure

All pages live under the `/ui/` prefix. [`frontend/ROUTES.md`](../../../../ROUTES.md) is the authoritative table of routes, their components and how the server treats them; do not copy that list here.

What `src/App.svelte` does with a route:

1. Maps the current path (from the navigation store) to a route config, which gives the route name, page id and title key. Unknown paths render the 404 page (ROUTES.md lists the exceptions under `/ui/system/` and `/ui/settings/`).
2. Loads the page component on demand. The dashboard is imported statically; the other pages are lazy loaded.
3. Passes the title, page id, current path, security state, version, login configuration and a navigate callback to `RootLayout`, and renders the page component as its content.
4. Renders a full-screen login form instead of `RootLayout` when the login route is active for a visitor who is not allowed in, or when private mode is on and the visitor is not authenticated.

## Theme Management

- `RootLayout` restores the saved theme from `localStorage` on mount
- The theme toggle is in the header settings menu, which is shown from the `md` breakpoint up
- The theme is applied through the `data-theme` attribute on the document element

## Security Integration

- The System, Help and Settings sections are hidden when security is enabled and access is not allowed
- The footer shows Login or Logout based on the security state
- Auth state is managed by the auth store, initialized in `RootLayout`

## Responsive Behavior

- The sidebar is an overlay drawer below the `lg` breakpoint and a fixed column from `lg` up
- The sidebar collapse toggle is available only from `lg` up
- The settings menu in the header appears from `md` up
