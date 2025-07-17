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

## Code Quality (Run Before Commits)

```bash
npm run check:all     # Format + lint + typecheck
npm run lint:fix      # Auto-fix linting
npm run typecheck     # TypeScript/Svelte validation
```

## Development Tools

Debug scripts in `tools/` require dev server at `http://192.168.4.152:8080`:

```bash
node tools/test-all-pages.js
node tools/debug-analytics.js
```

add new puppeteer scripts in that folder, create reusable debug tools.

**Viewport Standards:**

- Desktop: 1280x1400px
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

## Guidelines

- Follow Svelte 5 patterns (runes, snippets)
- Use TypeScript for all components
- Well defined reusable components
- Organize by functionality
- Run `npm run check:all` before commits
- Address accessibility by ARIA roles, semantic markup, keyboard event handlers
- Write and run Vitest tests
- **Document all components** - Include comprehensive HTML comments at the top of each component describing purpose, usage, features, and props
