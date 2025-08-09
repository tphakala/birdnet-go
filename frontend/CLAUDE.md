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

// Array validation example:
const items = Array.isArray(data.items) ? data.items : [];
const config = isPlainObject(data.config) ? data.config : {};

// Guard against non-array but truthy values:
function safeArrayDefault(value: unknown, defaultValue: unknown[] = []): unknown[] {
  return Array.isArray(value) ? value : defaultValue;
}

// Usage in settings:
const settings = {
  include: safeArrayDefault(base.include),
  exclude: safeArrayDefault(base.exclude),
};
```

### Type Guards for Object Safety

Always validate object types before using them, especially in settings derivation:

```typescript
// ✅ Define reusable type guard
function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  // Check if the prototype is exactly Object.prototype or null
  const proto = Object.getPrototypeOf(value);
  return proto === null || proto === Object.prototype;
}

// ✅ Use type guard for safe object assignment
const settings = {
  include: base.include ?? [],
  exclude: base.exclude ?? [],
  config: isPlainObject(base.config) ? base.config : {}, // Safe object validation
};

// ❌ Unsafe direct assignment (base.config could be array, null, etc.)
const unsafe = {
  config: base.config ?? {}, // Could assign [] or other non-plain objects
};

// ✅ Complete pattern for settings derivation
let settings = $derived(
  (() => {
    const base = $speciesSettings ?? fallbackSettings; // Use ?? for root object

    return {
      include: Array.isArray(base.include) ? base.include : [],
      exclude: Array.isArray(base.exclude) ? base.exclude : [],
      config: isPlainObject(base.config) ? base.config : {}, // Type guard for objects
    } as SettingsType;
  })()
);
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

## Testing Best Practices

### TypeScript in Test Files

#### Handling `any` Types in Edge Case Testing

When testing edge cases with intentionally malformed data, you need to use `any` types. Follow these patterns:

1. **Use inline ESLint disable comments for intentional `any` usage**:

```typescript
// For single line
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const malformedData = 'string' as any;

// For blocks
/* eslint-disable @typescript-eslint/no-explicit-any */
settingsActions.updateSection('realtime', {
  species: undefined as any,
  config: 'not-an-object' as any,
});
/* eslint-enable @typescript-eslint/no-explicit-any */
```

2. **Create type helpers for test data**:

```typescript
// Define test-specific types
type MalformedSettings = Record<string, unknown>;
type TestData = Partial<SettingsFormData> & { [key: string]: unknown };

// Use unknown with type guards instead of any where possible
const testData: unknown = getData();
if (typeof testData === 'object' && testData !== null) {
  // Type guard ensures safe access
}
```

#### Avoiding Common ESLint Errors

1. **Unused Variables**:
   - Remove unused imports immediately
   - Use underscore prefix for intentionally unused variables: `_unusedVar`
   - For required but unused component references: `expect(component).toBeTruthy()`

2. **Nullish Coalescing vs Logical OR**:

```typescript
// ❌ Avoid - triggers ESLint warning
const count = value || 0; // Problem: treats 0 as falsy

// ✅ Correct - use nullish coalescing
const count = value ?? 0; // Only replaces null/undefined

// When you explicitly want logical OR behavior, add comment:
const display = value || 'default'; // eslint-disable-line @typescript-eslint/prefer-nullish-coalescing -- intentional falsy check
```

3. **Unnecessary Conditionals**:

```typescript
// ❌ Avoid - settings is always defined after get()
const settings = get(birdnetSettings);
if (settings) {
  // Unnecessary - get() always returns a value
  // ...
}

// ✅ Correct - check specific properties
const settings = get(birdnetSettings);
if (settings.sensitivity !== undefined) {
  // ...
}
```

4. **Browser APIs in Tests**:

```typescript
// Check for API availability
if (typeof performance !== 'undefined') {
  const startTime = performance.now();
  // ...
}

// Or use Node.js alternatives in test environment
import { performance } from 'perf_hooks'; // For Node.js
```

#### Type Assertions in Tests

```typescript
// For accessing nested properties in tests
const formData = get(settingsStore).formData;
const speciesConfig = (formData as TestFormData)?.realtime?.species?.config;

// Type guard approach (preferred)
function isSpeciesSettings(value: unknown): value is SpeciesSettings {
  return typeof value === 'object' && value !== null && 'include' in value && 'exclude' in value;
}
```

#### Test File Organization

1. **Group ESLint disable directives at the top for file-wide issues**:

```typescript
/* eslint-disable @typescript-eslint/no-explicit-any */
// Test file with many intentional any types
```

2. **Use describe blocks to scope disable directives**:

```typescript
describe('Edge Cases', () => {
  /* eslint-disable @typescript-eslint/no-explicit-any */
  // Tests with malformed data
  /* eslint-enable @typescript-eslint/no-explicit-any */
});
```

3. **Document why `any` is necessary**:

```typescript
// Testing malformed data structure - any is required
settingsActions.updateSection('config', {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: 'string-instead-of-object' as any,
});
```

### Performance Testing

```typescript
// Safe performance measurement in tests
function measurePerformance(fn: () => void): number {
  if (typeof performance !== 'undefined') {
    const start = performance.now();
    fn();
    return performance.now() - start;
  }
  // Fallback for environments without performance API
  const start = Date.now();
  fn();
  return Date.now() - start;
}
```

### Mock Data Types

Create dedicated types for test scenarios:

```typescript
// types/test-helpers.ts
export type DeepPartial<T> = T extends object ? { [P in keyof T]?: DeepPartial<T[P]> } : T;

export type MalformedData =
  | string
  | number
  | boolean
  | null
  | undefined
  | unknown[]
  | Record<string, unknown>;

export type TestSettings = DeepPartial<SettingsFormData> & {
  [key: string]: MalformedData;
};
```

### Running Linters Before Commit

**Always run these commands before committing test files**:

```bash
# Check formatting
npm run format:check

# Fix formatting
npx prettier --write src/**/*.test.ts

# Check linting
npm run lint

# Fix auto-fixable issues
npx eslint --fix src/**/*.test.ts

# Full check
npm run check:all
```

### Strict TypeScript Configuration

#### Dealing with Strict Null Checks

When TypeScript's strict mode is enabled, be careful with store values:

```typescript
// ❌ Problematic - assumes store always has value
const settings = get(birdnetSettings);
settings.threshold = 0.5; // Error if settings could be undefined

// ✅ Safe access patterns
const settings = get(birdnetSettings);
if (settings) {
  settings.threshold = 0.5;
}

// ✅ With default fallback
const settings = get(birdnetSettings) ?? createDefaultSettings();
settings.threshold = 0.5;

// ✅ Optional chaining for nested access
const threshold = get(birdnetSettings)?.dynamicThreshold?.min ?? 0;
```
