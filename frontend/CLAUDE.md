# Frontend Development Guide - Svelte 5

## Tech stack

Project is based on

- Svelte 5
- Typescript
- Tanstack Query
- Vite build tool
- Vitest testing framework
- Tailwind 3 CSS styling
- daisyUI 4 components

**Techstack reference documentation**

- Svelte 5 https://svelte.dev/docs/svelte/llms-small.txt, read this when working on Svelte code

## Structure

```
frontend/
├── src/lib/
│   ├── components/{charts,data,forms,media,ui}/  # UI components
│   ├── features/{analytics,detections,settings}/ # Feature components
│   ├── pages/                                    # Page components
│   ├── stores/                                   # State management
│   └── utils/                                    # Utilities
├── tools/                                        # Debug scripts
└── dist/                                         # Build output
```

## Available Task commands

Project uses `task` for building etc.
In most cases task frontend-dev is not required as developer has hot reloading server running with `air`

```bash
task: Available tasks for this project:
* download-avicommons-data:       Download the Avicommons latest.json data file if it doesn't exist
* frontend-build:                 Build frontend for production
* frontend-dev:                   Start frontend development server
* frontend-install:               Install frontend dependencies
* frontend-lint:                  Lint frontend code
* frontend-test:                  Run frontend tests
* frontend-test-coverage:         Run frontend tests with coverage
```

## Code Quality (Run Before Commits)

```bash
npm run check:all     # Format + lint + typecheck
npm run lint:fix      # Auto-fix linting
npm run typecheck     # TypeScript/Svelte validation
```

## Development Tools

### UI Screenshots & Testing

Use Playwright for automated screenshots and browser testing:

```bash
cd tools/
node screenshot.js http://192.168.4.152:8080/ui/dashboard
node screenshot.js http://192.168.4.152:8080/ui/analytics -o analytics.png
node screenshot.js http://192.168.4.152:8080/ui/settings -w 1920 -h 1080
```

See `tools/CLAUDE.md` for complete usage instructions.

### Legacy Tools

```bash
node tools/test-all-pages.js     # Puppeteer fallback if Playwright unavailable
node tools/debug-analytics.js
```

**Viewport Standards:**

- Desktop: 1400x1800px (default)
- Large Desktop: 1920x1080px
- Mobile: 390x844px
- Tablet: 768x1024px

## Settings Components

Use `SettingsSection` with change detection:

```svelte
<script>
  import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';

  let hasChanges = $derived(hasSettingsChanged(original, current));
</script>

<SettingsSection title="Title" {hasChanges}>
  <!-- controls -->
</SettingsSection>
```

## CSRF Protection

API requests require CSRF tokens:

```typescript
// utils/api.ts pattern
function getCsrfToken(): string | null {
  // 1. Check meta tag (primary)
  const meta = document.querySelector('meta[name="csrf-token"]');
  if (meta?.getAttribute('content')) return meta.getAttribute('content');

  // 2. Check cookie (fallback)
  const match = document.cookie.match(/csrf=([^;]+)/);
  return match?.[1] || null;
}

// Include in headers
headers.set('X-CSRF-Token', getCsrfToken());
```

## Server-Sent Events (SSE)

Use `reconnecting-eventsource` package for real-time updates with automatic reconnection handling.

```javascript
import ReconnectingEventSource from 'reconnecting-eventsource';

// Create connection with automatic reconnection
const eventSource = new ReconnectingEventSource('/api/endpoint', {
  max_retry_time: 30000, // Max 30 seconds between reconnection attempts
  withCredentials: false
});

// Handle events
eventSource.onmessage = (event) => {
  const data = JSON.parse(event.data);
  // Process data
};

// Cleanup
eventSource.close();
```

- See `/frontend/doc/reconnecting-eventsource.md` for full implementation guide
- No manual reconnection logic needed
- Automatic exponential backoff

## Guidelines

- Follow Svelte 5 patterns (runes, snippets)
- Use TypeScript for all components
- Well defined reusable components
- Organize by functionality
- Run `npm run check:all` before commits
- Address accessibility by ARIA roles, semantic markup, keyboard event handlers
- Write and run Vitest tests
- Document all components - Include comprehensive HTML comments at the top of each component describing purpose, usage, features, and props
