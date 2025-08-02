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

**CRITICAL: Always run static analysis before committing to prevent IDE warnings/errors**

```bash
npm run check:all     # Format + lint + CSS lint + typecheck
npm run lint:fix      # Auto-fix JavaScript/TypeScript linting
npm run lint:css      # CSS/Svelte style validation
npm run lint:css:fix  # Auto-fix CSS issues
npm run typecheck     # TypeScript/Svelte validation

# Unit tests:
npx vitest run --reporter=json

# Code Analysis:
npm run analyze:all       # Run circular dependency + duplication analysis
npm run analyze:circular  # Check for circular dependencies (✅ None found)
npm run analyze:deps      # Show dependency summary and counts
npm run analyze:orphans   # Find unused/orphaned files (111 orphans detected)
npm run analyze:duplicates # Find code duplication (4 clones, 0.63% duplication)

# Accessibility Testing:
npm run test:a11y         # Run accessibility tests using axe-core
npm run test:a11y:watch   # Watch accessibility tests during development

# Security Analysis (143 warnings found):
# - Object injection: 136 warnings (mostly false positives in frontend)
# - Unsafe regex: 5 warnings (potential ReDoS vulnerabilities)
# - Non-literal filesystem: 2 warnings (path traversal risks)
```

**Static Analysis Checklist:**

- ✅ **IDE Diagnostics**: Check VS Code Problems panel for TypeScript/ESLint errors
- ✅ **Type Safety**: Ensure no `any` types without proper eslint-disable comments
- ✅ **Security Issues**: Review `eslint-plugin-security` warnings (especially regex/filesystem)
- ✅ **Code Quality**: Run `npm run analyze:all` for dependency and duplication analysis
- ✅ **CSS/Style Validation**: Run `npm run lint:css` for Tailwind/CSS issues
- ✅ **Import Validation**: Verify all imports resolve correctly
- ✅ **Component Props**: Check component type compatibility
- ✅ **Test Syntax**: Validate test file syntax and imports
- ✅ **Accessibility**: Run `npm run test:a11y` for comprehensive axe-core validation

**⚠️ NEVER COMMIT CODE WITH:**

- TypeScript compilation errors
- ESLint errors (warnings are acceptable)
- Critical security issues (unsafe regex, path traversal)
- Major CSS/style violations (use `npm run lint:css:fix`)
- Missing imports or undefined variables
- Component type mismatches
- Accessibility violations

## TypeScript Best Practices

**CRITICAL: Never bypass TypeScript's type checking system**

- **❌ FORBIDDEN**: Using `any` type without proper justification and eslint-disable comment
- **❌ FORBIDDEN**: Using type assertions (`as Type`) to bypass proper type safety
- **❌ FORBIDDEN**: Using non-null assertions (`!`) without absolute certainty
- **✅ REQUIRED**: Use proper type guards, conditional checks, and iterator validation
- **✅ REQUIRED**: Handle undefined/null cases explicitly with proper checks
- **✅ REQUIRED**: Use TypeScript's strict mode and compiler checks as intended

**Examples:**

```typescript
// ❌ Bad: Type assertion bypasses safety
const value = map.get(key) as string;

// ❌ Bad: Non-null assertion without verification
const value = map.get(key)!;

// ✅ Good: Proper type checking and iterator validation
const result = iterator.next();
if (!result.done && result.value !== undefined) {
  // Safe to use result.value
}

// ✅ Good: Explicit null/undefined handling
const value = map.get(key);
if (value !== undefined) {
  // Safe to use value
}
```

TypeScript's type system exists to prevent runtime errors. Bypassing it defeats the purpose and introduces potential bugs.

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

## Logging

Use the centralized logger utility instead of console statements:

```typescript
import { getLogger, loggers } from '$lib/utils/logger';

// Option 1: Create a custom logger for your module
const customLogger = getLogger('myModule');

// Option 2: Use predefined category loggers
const apiLogger = loggers.api; // For API-related logging
const authLogger = loggers.auth; // For authentication
const sseLogger = loggers.sse; // For SSE connections
const audioLogger = loggers.audio; // For audio components
const uiLogger = loggers.ui; // For UI components
const settingsLogger = loggers.settings; // For settings

// Most common pattern - choose one logger per file:
const logger = loggers.ui; // For UI components
```

**Important**: Call `getLogger()` once when the module loads and reuse the returned logger instance throughout the module. This prevents creating multiple logger instances inside functions, reducing unnecessary object allocation and ensuring consistent category scoping.

### Logger Methods

```typescript
// Debug information (dev only)
logger.debug('Component initialized', props);

// Informational messages (dev only)
logger.info('Connection established');

// Warnings (always logged)
logger.warn('Deprecated method used');

// Errors with context (always logged)
logger.error('Failed to save', error, {
  component: 'SettingsPage',
  action: 'save',
  userId: user.id,
});

// Performance timing (development only - no-op in production)
logger.time('dataLoad');
// ... expensive operation
logger.timeEnd('dataLoad'); // Logs: [category] dataLoad: 123.45ms

// Grouping (development only - no-op in production)
logger.group('Processing items');
items.forEach(item => logger.debug('Item:', item));
logger.groupEnd();
```

### Key Features

- **Environment-aware**: Debug/info logs only in development
- **Zero configuration**: Works immediately
- **Category-based**: Helps identify log sources
- **Sentry-ready**: Structured for future integration
- **Type-safe**: Full TypeScript support
- **No console warnings**: Properly configured for ESLint
- **Security-aware**: Built-in PII protection guidelines

### Security and PII Protection

**CRITICAL**: Never log personally identifiable information (PII) or sensitive data:

```typescript
// ❌ Don't log PII or sensitive data
logger.error('Login failed', error, {
  username: 'john.doe@example.com', // PII
  password: 'secret123', // Sensitive
  sessionToken: 'abc123xyz', // Sensitive
});

// ✅ Log safe identifiers and context
logger.error('Login failed', error, {
  component: 'LoginForm',
  action: 'authenticate',
  userId: user.id, // Safe: non-PII identifier
  attemptCount: 3, // Safe: operational data
});

// ✅ Sanitize or redact sensitive fields
logger.debug('API request', {
  endpoint: '/api/user/profile',
  method: 'POST',
  headers: { 'content-type': 'application/json' }, // Safe headers only
  bodyFields: Object.keys(requestBody), // Field names, not values
});
```

**Safe to log**: Component names, action names, non-PII IDs, counts, timestamps, status codes, error types
**Never log**: Emails, usernames, passwords, tokens, API keys, personal data, request/response bodies with PII

### Log Levels by Build Target

| Log Level | Development | Production | Purpose                                     |
| --------- | ----------- | ---------- | ------------------------------------------- |
| `debug`   | ✅ Logged   | ❌ Silent  | Development details, state changes          |
| `info`    | ✅ Logged   | ❌ Silent  | Important flow information                  |
| `warn`    | ✅ Logged   | ✅ Logged  | Deprecations, fallbacks, recoverable issues |
| `error`   | ✅ Logged   | ✅ Logged  | Failures requiring attention                |
| `time`    | ✅ Logged   | ❌ No-op   | Performance timing measurements             |
| `group`   | ✅ Logged   | ❌ No-op   | Console grouping for organization           |

**Key Points:**

- Only `warn` and `error` logs appear in production builds
- Timing and grouping functions are no-op in production (zero overhead)
- Development logs help with debugging but are stripped from production bundles

### Best Practices

1. Use appropriate log levels:
   - `debug`: Development details, state changes
   - `info`: Important flow information
   - `warn`: Deprecations, fallbacks
   - `error`: Failures requiring attention

2. Always provide context for errors:

   ```typescript
   logger.error('API request failed', error, {
     component: 'DetectionsPage',
     action: 'loadDetections',
     endpoint: '/api/v2/detections',
   });
   ```

3. Use categories that match your module's purpose
4. Keep production logs minimal (warn/error only)

### Svelte-Specific Patterns

**Avoid redundant environment checks** - Logger handles dev/prod automatically:

```svelte
<!-- ✅ Do this - logger handles environment -->
<script>
  $effect(() => {
    logger.debug('Component state updated', { state });
  });
</script>

<!-- ❌ Don't do this - redundant check -->
{#if import.meta.env.DEV}
  {logger.debug('Component state:', state)}
{/if}
```

**Move logging out of templates** - Use functions or effects:

```svelte
<!-- ✅ Log in reactive statements -->
<script>
  $effect(() => {
    if (someCondition) {
      logger.warn('Unexpected condition', { component: 'MyComponent' });
    }
  });
</script>

<!-- ❌ Don't log in templates -->
{#if someCondition}
  {logger.warn('Unexpected condition')}
  <div>Content</div>
{/if}
```

**Provide component context** - Always include component name and action:

```svelte
<script>
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.ui;

  async function handleSubmit() {
    try {
      await submitData();
    } catch (error) {
      logger.error('Form submission failed', error, {
        component: 'MyForm',
        action: 'handleSubmit',
      });
    }
  }
</script>
```

## Server-Sent Events (SSE)

Use `reconnecting-eventsource` package for real-time updates with automatic reconnection handling.

```javascript
import ReconnectingEventSource from 'reconnecting-eventsource';

// Create connection with automatic reconnection
const eventSource = new ReconnectingEventSource('/api/endpoint', {
  max_retry_time: 30000, // Max 30 seconds between reconnection attempts
  withCredentials: false,
});

// Handle events
eventSource.onmessage = event => {
  const data = JSON.parse(event.data);
  // Process data
};

// Cleanup
eventSource.close();
```

- See `/frontend/doc/reconnecting-eventsource.md` for full implementation guide
- No manual reconnection logic needed
- Automatic exponential backoff

## Date and Time Handling

**IMPORTANT**: Always use local timezone functions to avoid timezone bugs.

```typescript
import {
  getLocalDateString,
  getLocalTimeString,
  isToday,
  isFutureDate,
  parseHour,
  parseTime,
} from '$lib/utils/date';

// ✅ Correct: Use local timezone helpers
const today = getLocalDateString(); // "2024-01-15"
const tomorrow = getLocalDateString(new Date(Date.now() + 86400000));
const isCurrentDay = isToday('2024-01-15'); // true/false
const isFuture = isFutureDate('2024-12-31'); // true/false

// Time handling
const currentTime = getLocalTimeString(); // "14:30:45"
const timeNoSeconds = getLocalTimeString(new Date(), false); // "14:30"
const hour = parseHour('14:30:00'); // 14 (with validation)
const time = parseTime('14:30:45'); // { hours: 14, minutes: 30, seconds: 45 }

// ❌ Wrong: Never use toISOString() for date display
const wrongDate = new Date().toISOString().split('T')[0]; // UTC conversion!
```

**Key Points:**

- `toISOString()` converts to UTC, causing wrong dates for users in timezones ahead/behind UTC
- Always use `getLocalDateString()` for YYYY-MM-DD formatting
- Use provided helpers for date comparisons and time parsing
- These utilities ensure consistent behavior across all timezones

## TypeScript Type Assertions in Svelte Bindings

**Problem**: Prettier conflicts with TypeScript type assertions in Svelte component bindings.

```svelte
<!-- ❌ This pattern breaks Prettier -->
<Checkbox bind:checked={(settings.mqtt as MQTTSettings).retain} />
```

**Root Cause**: Prettier reformats the type assertion syntax `(obj as Type).prop` which breaks Svelte's binding syntax parser.

**Solutions**:

1. **Use prettier-ignore comments** (recommended for consistency):

```svelte
<!-- prettier-ignore -->
<Checkbox
  bind:checked={(settings.mqtt as MQTTSettings).retain}
  onchange={() => updateRetain((settings.mqtt as MQTTSettings).retain)}
/>
```

2. **Use optional chaining with non-bind patterns**:

```svelte
<!-- Alternative: avoid bind:checked with type assertions -->
<Checkbox checked={settings.mqtt?.retain ?? false} onchange={checked => updateRetain(checked)} />
```

3. **Pre-cast to variables**:

```svelte
<script>
  let mqttSettings = $derived(settings.mqtt as MQTTSettings);
</script>

<Checkbox bind:checked={mqttSettings.retain} />
```

**Best Practice**: Use `<!-- prettier-ignore -->` to maintain consistency with existing codebase patterns while avoiding formatter conflicts.

## Accessibility Guidelines

### WCAG 2.1 Level AA Compliance

All components must follow WCAG 2.1 Level AA accessibility standards. The project includes comprehensive accessibility testing using axe-core.

#### Form Accessibility

**Label Association**: All form inputs must have proper labels

```svelte
<!-- ✅ Correct: Proper label association -->
<label for="username" id="username-label">Username</label>
<input type="text" id="username" aria-labelledby="username-label" required />

<!-- ✅ Correct: Help text association -->
<input type="email" id="email" aria-describedby="email-help" />
<div id="email-help">We'll never share your email</div>
```

**Field Validation**: Error states must be accessible

```svelte
<input
  type="text"
  id="field"
  aria-invalid={hasError}
  aria-describedby={hasError ? 'field-error' : undefined}
/>
{#if hasError}
  <div id="field-error" role="alert">{errorMessage}</div>
{/if}
```

#### Interactive Elements

**Button Labels**: All buttons must have accessible names

```svelte
<!-- ✅ Correct: Text content provides label -->
<button>Save Changes</button>

<!-- ✅ Correct: aria-label for icon-only buttons -->
<button aria-label="Close dialog">
  {@html navigationIcons.close}
</button>

<!-- ✅ Correct: aria-labelledby referencing other text -->
<button aria-labelledby="section-title">Edit</button>
<h2 id="section-title">User Profile</h2>
```

**Link Context**: Links must have descriptive text

```svelte
<!-- ❌ Wrong: Vague link text -->
<a href="/detections/123">Click here</a>

<!-- ✅ Correct: Descriptive link text -->
<a href="/detections/123">View American Robin detection details</a>
```

#### Data Tables

**Header Association**: Use proper table structure

```svelte
<table role="table">
  <caption class="sr-only">Bird detections with species, confidence, and time</caption>
  <thead>
    <tr>
      <th scope="col" aria-sort="none">Species</th>
      <th scope="col" aria-sort="none">Confidence</th>
      <th scope="col">Actions</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td>American Robin</td>
      <td>95%</td>
      <td>
        <button aria-label="Play audio for American Robin detection">
          {@html mediaIcons.play}
        </button>
      </td>
    </tr>
  </tbody>
</table>
```

#### Modal Dialogs

**Focus Management**: Modals must trap focus and manage focus properly

```svelte
<!-- The Modal component handles this automatically -->
<Modal isOpen={showModal} title="Confirm Action" onClose={handleClose}>
  <!-- Focus is automatically trapped within modal -->
  <p>Are you sure you want to delete this item?</p>
</Modal>
```

**ARIA Attributes**: Proper modal semantics

```svelte
<div
  role="dialog"
  aria-modal="true"
  aria-labelledby="modal-title"
  aria-describedby="modal-description"
>
  <h2 id="modal-title">Delete Confirmation</h2>
  <p id="modal-description">This action cannot be undone.</p>
</div>
```

#### Navigation and Menus

**Dropdown Menus**: Use proper menu semantics

```svelte
<button aria-expanded={isOpen} aria-haspopup="menu" aria-controls="dropdown-menu"> Options </button>

{#if isOpen}
  <div role="menu" id="dropdown-menu">
    <button role="menuitem">Edit</button>
    <button role="menuitem">Delete</button>
  </div>
{/if}
```

**Pagination**: Descriptive button labels

```svelte
<div aria-label="Pagination">
  <button aria-label="Go to previous page" disabled={currentPage === 1}> « </button>
  <button aria-label="Current page">Page {currentPage} of {totalPages}</button>
  <button aria-label="Go to next page" disabled={currentPage === totalPages}> » </button>
</div>
```

#### Status and Live Regions

**Status Updates**: Use live regions for dynamic content

```svelte
<!-- For important status changes -->
<div role="status" aria-live="polite">Settings saved successfully</div>

<!-- For urgent alerts -->
<div role="alert" aria-live="assertive">Connection lost - attempting to reconnect</div>

<!-- For screen reader only announcements -->
<div class="sr-only" aria-live="polite">
  Current audio level: {level} percent
</div>
```

#### Icon Usage

**Decorative Icons**: All centralized icons include `aria-hidden="true"` automatically

```svelte
<!-- ✅ Correct: Icons are automatically decorative -->
<button aria-label="Save changes">
  {@html actionIcons.save}
</button>

<!-- ❌ Wrong: Never add custom SVG icons -->
<svg>...</svg>
<!-- Use centralized icons instead -->
```

#### Testing

**Automated Testing**: Run accessibility tests regularly

```bash
# Run accessibility test suite
npm run test:a11y

# Run specific accessibility tests
npm run test:a11y -- --reporter=verbose

# Run with watch mode during development
npm run test:a11y:watch
```

**Manual Testing Checklist**:

- [ ] All interactive elements are keyboard accessible
- [ ] Focus indicators are visible and clear
- [ ] Screen reader announces all important information
- [ ] Color is not the only way to convey information
- [ ] Form errors are announced to screen readers
- [ ] Modal focus is properly managed

#### Common Patterns

**Loading States**: Announce loading to screen readers

```svelte
<div role="status" aria-live="polite">
  {#if loading}
    Loading data...
  {:else}
    Data loaded successfully
  {/if}
</div>
```

**Error Boundaries**: Accessible error messages

```svelte
{#if error}
  <div role="alert" class="alert alert-error">
    <span>Error: {error.message}</span>
  </div>
{/if}
```

**Dynamic Content**: Announce changes to screen readers

```svelte
<div aria-live="polite" class="sr-only">
  {filteredResults.length} results found
</div>
```

#### Resources

- **WCAG Guidelines**: https://www.w3.org/WAI/WCAG21/quickref/
- **ARIA Practices**: https://www.w3.org/WAI/ARIA/apg/
- **axe-core Rules**: https://github.com/dequelabs/axe-core/blob/develop/doc/rule-descriptions.md
- **Testing Tools**: Use axe DevTools browser extension for manual testing

## Icon Usage

**ALWAYS use centralized icons from `$lib/utils/icons.ts` - NEVER create inline SVGs**

```svelte
<script>
  import { navigationIcons, actionIcons, systemIcons } from '$lib/utils/icons';
</script>

<!-- ✅ Correct: Use centralized icons -->
{@html navigationIcons.close}
{@html actionIcons.search}
{@html systemIcons.clock}

<!-- ❌ Wrong: Never inline SVGs -->
<svg>...</svg>
```

**Available Icon Categories:**

- `navigationIcons`: close, arrows, chevrons, menu
- `actionIcons`: edit, delete, save, copy, add, search, filter
- `systemIcons`: clock, calendar, settings, user, loading, eye
- `alertIcons`: error, warning, info, success
- `mediaIcons`: play, pause, download, volume
- `dataIcons`: chart, document, folder, table

**Adding New Icons:**

1. Add to appropriate category in `icons.ts`
2. Use consistent sizing classes (`h-4 w-4`, `h-5 w-5`)
3. Include proper stroke/fill attributes
4. Test in multiple contexts

## Guidelines

- Follow Svelte 5 patterns (runes, snippets)
- Use TypeScript for all components
- Well defined reusable components
- Organize by functionality
- **MANDATORY: Run static analysis before EVERY commit** - Check IDE diagnostics panel
- Run `npm run check:all` before commits
- Address accessibility by ARIA roles, semantic markup, keyboard event handlers
- Write and run Vitest tests
- Document all components - Include comprehensive HTML comments at the top of each component describing purpose, usage, features, and props
- **Use centralized icons only** - see Icon Usage section above

## Accessibility Testing

### axe-core Integration

The project uses axe-core for automated accessibility testing that goes beyond basic ESLint rules.

**Test Files:**

```typescript
// Create accessibility tests with "Accessibility" in the describe block name
describe('Component Accessibility Tests', () => {
  it('should have no accessibility violations', async () => {
    document.body.innerHTML = '<button>Click Me</button>';
    const button = document.querySelector('button')!;

    await expectNoA11yViolations(button, A11Y_CONFIGS.strict);

    document.body.innerHTML = '';
  });
});
```

**Available Utilities:**

- `expectNoA11yViolations()` - Assert no accessibility violations
- `getA11yReport()` - Generate detailed accessibility report
- `runAxeTest()` - Run axe-core analysis with custom options

**Predefined Configurations:**

- `A11Y_CONFIGS.strict` - WCAG 2.0/2.1 AA compliance
- `A11Y_CONFIGS.lenient` - Basic accessibility (development)
- `A11Y_CONFIGS.forms` - Form-specific accessibility rules

**Testing Guidelines:**

- Test critical user flows for accessibility
- Focus on form elements, buttons, links, and navigation
- Include keyboard navigation and screen reader compatibility
- Test color contrast and focus management

## Pre-Commit Workflow

**AUTOMATED STEPS (via Husky pre-commit hook):**

- **lint-staged** - Auto-format and lint staged files
- **svelte-check** - TypeScript/Svelte type checking for staged files
- Bypass with `git commit --no-verify` only in emergencies

**MANUAL STEPS before every `git commit`:**

1. **Check IDE Problems Panel** - Resolve ALL TypeScript/ESLint errors
2. **Run `npm run check:all`** - Ensure all static analysis passes
3. **Test affected functionality** - Verify changes work as expected
4. **Review accessibility** - Check for a11y warnings and violations

**Note:** The pre-commit hook automatically prevents commits with TypeScript errors in staged files.
