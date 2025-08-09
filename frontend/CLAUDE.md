# Frontend Development Guide - Svelte 5

## Tech Stack

- **Svelte 5** with Runes (`$state`, `$derived`, `$effect`)
- **TypeScript** - NO `any` types without justification
- **Tailwind 3** + **daisyUI 4** components
- **Vite** build, **Vitest** testing
- **i18n** - Custom implementation in `@i18n`

## Critical Rules

- **NEVER use `any` type**
- **NEVER create inline SVGs** - use `$lib/utils/icons`
- **NEVER use `toISOString()` for dates** - use `getLocalDateString()`
- **Run `npm run check:all` before EVERY commit**

## Structure

```
frontend/
├── src/lib/
│   ├── components/{charts,data,forms,media,ui}/
│   ├── features/{analytics,detections,settings}/
│   ├── pages/
│   ├── stores/
│   └── utils/
└── dist/
```

## Commands

| Command               | Purpose                     | When          |
| --------------------- | --------------------------- | ------------- |
| `npm run check:all`   | Format + lint + typecheck   | Before commit |
| `npm run lint:fix`    | Auto-fix JS/TS              | After changes |
| `npm run typecheck`   | Validate types              | Before PR     |
| `npm run test:a11y`   | Accessibility tests         | Before PR     |
| `npm run analyze:all` | Circular deps + duplication | Weekly        |

## Svelte 5 Patterns

### State Management

```svelte
<script lang="ts">
  let count = $state(0); // Reactive state
  let double = $derived(count * 2); // Computed value
  let items = $state<Item[]>([]); // Typed arrays

  $effect(() => {
    // Side effects
    console.log('Count changed:', count);
  });
</script>
```

### Component Props

```svelte
<script lang="ts">
  interface Props {
    title: string;
    count?: number;
    children?: Snippet;
  }

  let { title, count = 0, children }: Props = $props();
</script>
```

### Snippets (not slots)

```svelte
<!-- Child -->
<script lang="ts">
  let { header }: { header?: Snippet } = $props();
</script>

<!-- Parent -->
<Card>
  {#snippet header()}
    <h2>Title</h2>
  {/snippet}
</Card>
{#if header}{@render header()}{/if}
```

## TypeScript Safety

### ✅ REQUIRED

```typescript
// Proper type checking
const value = map.get(key);
if (value !== undefined) {
  // Safe to use value
}

// Iterator validation
const result = iterator.next();
if (!result.done && result.value !== undefined) {
  // Safe to use result.value
}

// Nullish coalescing for defaults (preferred over logical OR)
const settings = {
  include: base.include ?? [], // Only null/undefined → []
  exclude: base.exclude ?? [], // Only null/undefined → []
  config: base.config ?? {}, // Only null/undefined → {}
};

// Use logical OR only when you want to handle falsy values
const displayName = user.name || 'Anonymous'; // Handles "", null, undefined
```

### ❌ FORBIDDEN

```typescript
const value = map.get(key) as string; // Type assertion
const value = map.get(key)!; // Non-null assertion
let data: any; // Untyped

// Avoid logical OR for object defaults (can cause issues with empty arrays/objects)
const config = base.config || {}; // ❌ Converts [] to {}, 0 to {}, etc.
```

### Nullish Coalescing vs Logical OR

```typescript
// ✅ Use ?? when you only want to handle null/undefined
const items = data.items ?? []; // Only null/undefined → []
const config = settings.config ?? {}; // Only null/undefined → {}

// ✅ Use || when you want to handle all falsy values
const displayText = input || 'Default'; // "", 0, false, null, undefined → 'Default'
const isEnabled = flag || false; // Any falsy → false

// Common mistake in settings derivation:
const bad = base.include || []; // ❌ Converts 0, "", false to []
const good = base.include ?? []; // ✅ Only null/undefined to []
```

## Icon Usage

```svelte
<script>
  import { navigationIcons, actionIcons } from '$lib/utils/icons';
</script>

<!-- ✅ Correct -->
{@html navigationIcons.close}

<!-- ❌ Wrong -->
<svg>...</svg>
```

## Logging

```typescript
import { loggers } from '$lib/utils/logger';
const logger = loggers.ui; // Once per file

logger.debug('State changed', { component: 'MyComponent' });
logger.error('Failed', error, { action: 'save' });

// NEVER log PII: emails, passwords, tokens, personal data
```

## Date/Time Handling

```typescript
import { getLocalDateString, getLocalTimeString } from '$lib/utils/date';

// ✅ Correct - local timezone
const today = getLocalDateString(); // "2024-01-15"

// ❌ Wrong - UTC conversion
const wrong = new Date().toISOString().split('T')[0];
```

## SSE (Server-Sent Events)

```typescript
import ReconnectingEventSource from 'reconnecting-eventsource';

const eventSource = new ReconnectingEventSource('/api/endpoint', {
  max_retry_time: 30000,
  withCredentials: false,
});

eventSource.onmessage = event => {
  const data = JSON.parse(event.data);
};

// Cleanup
eventSource.close();
```

## CSRF Protection

```typescript
function getCsrfToken(): string | null {
  const meta = document.querySelector('meta[name="csrf-token"]');
  if (meta?.getAttribute('content')) return meta.getAttribute('content');

  const match = document.cookie.match(/csrf=([^;]+)/);
  return match?.[1] || null;
}
```

## Accessibility Quick Reference

### Forms

```svelte
<label for="field">Label</label>
<input id="field" aria-describedby="field-help" />
<div id="field-help">Help text</div>
```

### Buttons

```svelte
<button aria-label="Close dialog">
  {@html navigationIcons.close}
</button>
```

### Status Updates

```svelte
<div role="status" aria-live="polite">Loading...</div>
<div role="alert" aria-live="assertive">Error occurred</div>
```

### Testing

```bash
npm run test:a11y              # Run accessibility tests
npm run test:a11y:watch       # Watch mode
```

## Settings Components

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

## Pre-Commit Workflow

### Automated (Husky)

- lint-staged auto-formats staged files
- svelte-check validates TypeScript

### Manual Checklist

1. Check IDE Problems panel for errors
2. Run `npm run check:all`
3. Test affected functionality
4. Review accessibility warnings

## Debug Tools

```bash
# Screenshots
cd tools/
node screenshot.js http://localhost:8080/ui/dashboard
node screenshot.js http://localhost:8080/ui/analytics -w 1920 -h 1080

# Legacy
node tools/test-all-pages.js
```

## Common Patterns

### Loading States

```svelte
{#if loading}
  <div class="loading loading-spinner" />
{:else if error}
  <div role="alert" class="alert alert-error">{error.message}</div>
{:else}
  <Content />
{/if}
```

### Dynamic Lists

```svelte
{#each items as item (item.id)}
  <Item {item} />
{/each}
```

## Resources

- Svelte 5 docs available via MCP tool
- WCAG: https://www.w3.org/WAI/WCAG21/quickref/
- axe DevTools browser extension for testing
