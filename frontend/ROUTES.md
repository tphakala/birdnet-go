# Route Configuration

The frontend is a single page application. The Go server answers every page URL with the same HTML shell, and the Svelte app picks the page from the URL path. This file lists the routes the app knows and how the server treats them.

## How routing works

- `src/App.svelte` holds `routeConfigs` (route name, page id, title key, component name), a path-to-route map built from `uiPath()` segments, `findRouteConfig()` to look a config up by route name, `loadComponent()` to load a page with a dynamic import, and `handleRouting()` to choose the route for the current path.
- `src/lib/stores/navigation.svelte.ts` is the navigation store. It keeps `currentPath` (without any reverse proxy prefix) and `currentSearch`, normalizes every path to start with `/ui/`, and drives the History API (`navigate()` pushes an entry, `redirect()` replaces it). Paths are built with `uiPath()` or `Array.join()` instead of string literals so a proxy `sub_filter` rewrite cannot corrupt them.
- Every page except the dashboard is lazy loaded the first time its route is visited. The dashboard is bundled with the app for the first paint.
- Behind a reverse proxy the browser path carries the proxy prefix; the store strips it for routing and `buildAppUrl()` adds it back for the address bar.

## UI routes

All pages live under the `/ui/` prefix. `/` redirects to `/ui/dashboard` on the server.

| Path                            | Route name               | Component                                   | Notes                                                                           |
| ------------------------------- | ------------------------ | ------------------------------------------- | ------------------------------------------------------------------------------- |
| `/ui`, `/ui/`, `/ui/dashboard`  | `dashboard`              | `features/dashboard/pages/DashboardPage`    |                                                                                 |
| `/ui/login`                     | `login`                  | `LoginModal` as a full-screen form          | Shown to visitors who are not allowed in; see below                             |
| `/ui/live-stream`               | `live-stream`            | `features/live-stream/pages/LiveStreamPage` |                                                                                 |
| `/ui/notifications`             | `notifications`          | `views/Notifications`                       |                                                                                 |
| `/ui/detections`                | `detections`             | `views/Detections`                          |                                                                                 |
| `/ui/detections/:id`            | `detection-detail`       | `views/DetectionDetail`                     | `:id` must be numeric, otherwise the path is a 404                              |
| `/ui/search`                    | `search`                 | `views/Search`                              |                                                                                 |
| `/ui/analytics`                 | (redirect)               |                                             | Replaced by `/ui/analytics/summary`; a legacy `?tab=` maps to the matching view |
| `/ui/analytics/summary`         | `analytics-summary`      | `features/analytics/pages/SummaryPage`      |                                                                                 |
| `/ui/analytics/activity`        | `analytics-activity`     | `features/analytics/pages/ActivityPage`     |                                                                                 |
| `/ui/analytics/advanced`        | (redirect)               |                                             | Retired; replaced by `/ui/analytics/activity`                                   |
| `/ui/analytics/trends`          | `analytics-trends`       | `features/analytics/pages/TrendsPage`       |                                                                                 |
| `/ui/analytics/biodiversity`    | `analytics-biodiversity` | `features/analytics/pages/BiodiversityPage` |                                                                                 |
| `/ui/analytics/nocturnal`       | `analytics-nocturnal`    | `features/analytics/pages/NocturnalPage`    |                                                                                 |
| `/ui/analytics/weather`         | `analytics-weather`      | `features/analytics/pages/WeatherPage`      |                                                                                 |
| `/ui/analytics/soundscape`      | `analytics-soundscape`   | `features/analytics/pages/SoundscapePage`   |                                                                                 |
| `/ui/analytics/review`          | `analytics-review`       | `features/analytics/pages/ReviewPage`       |                                                                                 |
| `/ui/analytics/species`         | `species`                | `features/analytics/pages/Species`          |                                                                                 |
| `/ui/about`                     | `about`                  | `views/About`                               |                                                                                 |
| `/ui/help`                      | `help`                   | `views/Help`                                |                                                                                 |
| `/ui/help/report-bug`           | `report-bug`             | `views/ReportBug`                           |                                                                                 |
| `/ui/system`                    | `system`                 | `views/System`                              | Server-side auth                                                                |
| `/ui/system/database`           | `system`                 | `views/System`                              | Server-side auth; section of the system page                                    |
| `/ui/system/terminal`           | `system`                 | `views/System`                              | Server-side auth; section of the system page                                    |
| `/ui/system/inference`          | `system`                 | `views/System`                              | Server-side auth; section of the system page                                    |
| `/ui/system/import-export`      | `system`                 | `views/System`                              | Server-side auth; section of the system page                                    |
| `/ui/system/health`             | `system-health`          | `views/SystemHealth`                        | Server-side auth                                                                |
| `/ui/settings`                  | `settings`               | `views/Settings`                            | Server-side auth; shows the `main` section without redirecting                  |
| `/ui/settings/analysis`         | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/main`             | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/userinterface`    | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/audio`            | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/detectionfilters` | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/integrations`     | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/security`         | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/species`          | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/notifications`    | `settings`               | `views/Settings`                            | Server-side auth                                                                |
| `/ui/settings/support`          | `settings`               | `views/Settings`                            | Server-side auth                                                                |

Component paths are relative to `src/lib/desktop/`. The system and settings sections are chosen inside `System.svelte` and `Settings.svelte` from the navigation store path, not by separate route entries. `settingsSubpages` and `systemSubpages` in `App.svelte` only select the page title for them.

### Error routes

An unknown path renders the 404 page. Unknown paths below `/ui/system/` render the system page, and unknown paths below `/ui/settings/` render the main settings section. The error pages can also be requested with an `error` query parameter: `?error=404` and `?error=500` show the matching page, and any other value shows the generic error page with that code.

### Login route

`/ui/login` renders a full-screen login form when security is enabled and the visitor is not allowed in. The server redirects a logged-out visitor of a protected page to `/login?redirect=<page>`, and the navigation store turns that into `/ui/login`. After a successful login the app returns to the validated `redirect` target, defaulting to `/ui/dashboard`. A visitor who is already allowed goes straight to that target. When `Security.PrivateMode` is on and the visitor is not authenticated, the same form is shown on every route.

## Server-side handling

The server registers the page routes in `registerSPARoutes` (`internal/api/server.go`):

- **Behind authentication:** `/ui/system`, the catch-all `/ui/system/*` (which covers `/ui/system/database`, `/ui/system/terminal`, `/ui/system/inference`, `/ui/system/import-export` and `/ui/system/health`), `/ui/settings`, the listed settings pages (`main`, `audio`, `detectionfilters`, `integrations`, `notifications`, `security`, `species`, `support`, `userinterface`), and the catch-all `/ui/settings/*`, which also covers `/ui/settings/analysis`. An unauthenticated browser request is redirected to `/login`. The check is skipped when no authentication is configured or the client qualifies for the subnet bypass.
- **Public shell:** `/login`, `/ui`, `/ui/`, `/ui/dashboard`, `/ui/detections`, `/ui/detections/:id`, `/ui/notifications`, `/ui/analytics`, the `/ui/analytics/*` pages that are listed there, `/ui/search` and `/ui/about`.
- **Public catch-all:** every other `/ui/*` path, including `/ui/login`, `/ui/live-stream`, `/ui/help`, `/ui/help/report-bug` and any other path that is not listed above.

The page shell is public so the app can load and show the login form. The v2 API is the actual security boundary: with `Security.PrivateMode` on, the API answers 401 to unauthenticated requests and the app then renders the login form.

Authentication itself is described in `internal/security/README.md` and `internal/api/auth/README.md`.

## Other URLs

- `/ui/assets/*`: the built JavaScript, CSS and static files from `frontend/dist`, served from the embedded copy or, in development, from `frontend/dist` on disk.
- `/api/v2/*`: the API the app calls. `GET /api/v2/app/config` returns the startup configuration (CSRF token, security state, version, base path).
- `/auth/:provider` and `/auth/:provider/callback`: social login, handled by the Go server, not by the SPA.

See `internal/api/v2/README.md` for the API endpoint catalog.
