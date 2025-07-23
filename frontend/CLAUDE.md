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
<Checkbox
  checked={settings.mqtt?.retain ?? false}
  onchange={(checked) => updateRetain(checked)}
/>
```

3. **Pre-cast to variables**:
```svelte
<script>
  let mqttSettings = $derived(settings.mqtt as MQTTSettings);
</script>

<Checkbox bind:checked={mqttSettings.retain} />
```

**Best Practice**: Use `<!-- prettier-ignore -->` to maintain consistency with existing codebase patterns while avoiding formatter conflicts.

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

**REQUIRED STEPS before every `git commit`:**

1. **Check IDE Problems Panel** - Resolve ALL TypeScript/ESLint errors
2. **Run `npm run check:all`** - Ensure all static analysis passes
3. **Test affected functionality** - Verify changes work as expected
4. **Review accessibility** - Check for a11y warnings and violations
