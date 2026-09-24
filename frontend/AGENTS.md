# Frontend Development Guide (Svelte 5)

Applies to everything under `frontend/`. Run commands from `frontend/` unless
noted. See `TESTING.md` at the repository root for shared test patterns.

## Tech Stack

- **Svelte 5** with runes (`$state`, `$derived`, `$effect`, `$props`) and snippets
- **TypeScript** in strict mode
- **Tailwind CSS v4** (native utility classes, no component library)
- **Vite** build, **Vitest** + Testing Library tests
- **D3.js** for all charts and plots, unless a requirement genuinely needs
  something else
- **Icons**: `@lucide/svelte` (see `src/lib/utils/ICONS.md`)
- **i18n**: custom implementation in `$lib/i18n`, 16 locales

## Critical Rules

- **NEVER use `any`.** Type it, or use `unknown` plus a type guard. Tests are the
  only exception (see Testing below).
- **NEVER use type or non-null assertions to silence the compiler**
  (`value as string`, `value!`). Check for `undefined` instead.
- **NEVER write inline SVGs.** Use `@lucide/svelte` icons.
- **NEVER use `toISOString()` for dates.** It converts to UTC; use
  `getLocalDateString()` / `getLocalTimeString()` from `$lib/utils/date`.
- **NEVER rely on Secure Context APIs without a fallback.** BirdNET-Go usually
  runs over plain HTTP on home networks, where `crypto.randomUUID()` and
  `navigator.clipboard` are undefined. Use `copyToClipboard()` from
  `$lib/utils/clipboard`, and `generateId(prefix?)` from `$lib/utils/uuid` for
  element and list IDs. `generateSessionId()` (`$lib/utils/session`) is a
  concurrency token, not a secret; for security-sensitive randomness use
  `crypto.getRandomValues()`, which works over plain HTTP.
- **NEVER touch `localStorage` or `sessionStorage` directly.** Access can throw
  (private windows, blocked storage); use `getStoredValue()` / `setStoredValue()`
  from `$lib/utils/storage`, or wrap the call in `try`/`catch`.
- **NEVER ship ambiguous UI states.** Disabled controls, errors, and loading
  states must always tell the user _why_. See UX Design Principles.
- **NEVER log PII** (emails, passwords, tokens, personal data).
- **NEVER use `console.*` in app code.** Use the logger.
- **Run `npm run check:all` before every commit.**

## Structure

```text
frontend/
├── src/
│   ├── App.svelte, main.js      # entry points
│   ├── lib/
│   │   ├── desktop/              # tablet and desktop UI (see desktop/AGENTS.md)
│   │   │   ├── components/       # data, database, forms, media, modals, review, ui
│   │   │   ├── features/         # analytics, dashboard, detections, import-export,
│   │   │   │                     #   live-stream, settings, system, wizard
│   │   │   ├── layouts/          # RootLayout, sidebar, header
│   │   │   └── views/            # top-level pages
│   │   ├── components/           # shared primitives (icons, ui)
│   │   ├── api/, auth/, stores/, telemetry/, types/, utils/
│   │   └── i18n/                 # translation runtime and generated key types
│   └── test/setup.ts             # shared Vitest mocks
├── static/messages/              # translation files (see messages/AGENTS.md)
├── rules/                        # ast-grep rule sets
└── tools/                        # screenshot tooling (see tools/AGENTS.md)
```

## Commands

| npm (in `frontend/`) | Task (repo root)          | Purpose                                     |
| -------------------- | ------------------------- | ------------------------------------------- |
| `npm install`        | `task frontend-install`   | Install dependencies                        |
| `npm run dev`        | `task frontend-dev`       | Dev server                                  |
| `npm run check:all`  | `task frontend-lint`      | Format, lint, CSS lint, typecheck, ast-grep |
|                      | `task frontend-lint-fix`  | Auto-fix format, lint, ast-grep             |
| `npm run typecheck`  | `task frontend-typecheck` | TypeScript checks                           |
| `npm test`           | `task frontend-test`      | Unit and component tests                    |
| `npm run test:a11y`  |                           | Accessibility tests                         |
| `npm run build`      | `task frontend-build`     | Production build                            |
|                      | `task frontend-quality`   | Auto-fix, then checks, tests, and build     |

The Husky pre-commit hook runs lint-staged formatting, type checks, and the i18n
sync checks. Do not bypass it.

## Svelte 5 Patterns

```svelte
<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    count?: number;
    header?: Snippet;
    children?: Snippet;
  }

  let { title, count = 0, header, children }: Props = $props();

  let clicks = $state(0);
  let total = $derived(count + clicks); // pure, no side effects

  $effect(() => {
    // side effects only; return a cleanup function when needed
  });
</script>

{#if header}{@render header()}{/if}
{@render children?.()}

{#each items as item (item.id)}
  <Item {item} />
{/each}
```

- Use snippets, not slots; callback props, not `createEventDispatcher`; `onclick`,
  not `on:click`
- Always key `{#each}` blocks with a unique, stable ID (never a display name)
- Do not destructure `$state` objects (it breaks reactivity)
- If a Svelte MCP server is available in your tool, use its documentation
  lookup when unsure about Svelte 5 syntax and run its autofixer on components
  you write or change

## TypeScript Safety

```typescript
// Check before use instead of asserting
const value = map.get(key);
if (value !== undefined) {
  use(value);
}

// ?? for defaults: only null/undefined fall through
const items = data.items ?? [];

// || only when every falsy value ("", 0, false) should fall through
const displayName = user.name || 'Anonymous';

// Validate shape, not just presence, for data from the store or API
// (isPlainObject comes from $lib/utils/security)
const include = Array.isArray(base.include) ? base.include : [];
const config = isPlainObject(base.config) ? base.config : {};
```

`base.include || []` is a bug magnet (it turns `0`, `""`, and `false` into `[]`),
and `base.config ?? {}` still accepts an array. Validate with `Array.isArray`
or a type guard like `isPlainObject`.

## Internationalization

All user-visible text goes through `t()`:

```svelte
<script lang="ts">
  import { t } from '$lib/i18n';
</script>

<p>{t('about.avicommonsTitle')}</p>
```

Adding or changing keys (details in `static/messages/AGENTS.md`):

1. Add the key to `static/messages/en.json` first (English is the source of truth)
2. `npm run i18n:sync` to propagate the key to every locale (English fallback)
3. Translate the fallbacks in each locale file
4. `npm run generate:i18n-types` and commit the regenerated
   `src/lib/i18n/types.generated.ts`

The pre-commit hook rejects out-of-sync locales and stale generated types; CI
checks the generated types and key completeness. Run
`npm run i18n:validate:full` locally for the complete set, including orphaned
keys, which CI does not fail on.

Keys use dot notation with camelCase segments, grouped by feature
(`settings.audio.soundCards.gainLabel`). A segment that mirrors a backend
identifier (an event type, an operator, a config key) keeps that identifier's
spelling, even if it is snake_case; do not "fix" those keys.

## API, CSRF, and Live Data

- Use `api` / `fetchWithCSRF` from `$lib/utils/api`. They attach the
  `X-CSRF-Token` header from app state (fetched from `/api/v2/app/config` at
  startup); do not read CSRF tokens from the DOM or cookies yourself.
- Use `ReconnectingEventSource` from `$lib/utils/ReconnectingEventSource` for SSE
  streams, and close it on component teardown.

## Logging

```typescript
import { loggers } from '$lib/utils/logger';
const logger = loggers.ui; // once per file, pick the matching category

logger.debug('State changed', { component: 'MyComponent' });
logger.error('Save failed', error, { action: 'save' });
```

## UX Design Principles

Every interactive element needs a deliberate UX pass before it ships. Each
confused user becomes an issue that costs far more to triage than the design
review would have.

### No Ambiguous Disabled States

When a control is disabled, the user MUST be able to tell _why_ without
guessing:

- A **tooltip** (`title`) explaining the blocked condition. Tooltips are
  invisible on touch devices, so also show **inline helper text** or a **status
  badge** whenever the reason persists, and always for important controls
  (Save, Submit, Delete).
- **`aria-describedby`** pointing at the explanation.
- A **specific** reason: "Threshold must be between 0 and 1 before saving", not
  "Cannot save".
- Prefer `aria-disabled="true"` plus a suppressed click handler over native
  `disabled`: a natively disabled button leaves the tab order, so keyboard and
  screen reader users never reach its explanation. This is the pattern the
  shipped components use (for example
  `src/lib/desktop/features/settings/components/OptimizeReviewDialog.svelte`):

```svelte
<script lang="ts">
  const SAVE_HELP_ID = 'mysettings-save-help'; // unique per component
</script>

<button
  type="button"
  onclick={e => {
    if (!canSave) {
      e.preventDefault();
      return;
    }
    save();
  }}
  aria-disabled={!canSave ? 'true' : undefined}
  aria-describedby={!canSave ? SAVE_HELP_ID : undefined}
  title={!canSave ? saveBlockedReason : undefined}
  class="... aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
>
  {t('common.save')}
</button>
{#if !canSave}
  <p id={SAVE_HELP_ID} class="text-sm text-[var(--color-base-content)]/70">
    {saveBlockedReason}
  </p>
{/if}
```

Theme colours are written as CSS variables in arbitrary values
(`text-[var(--color-base-content)]`), not bare theme class names.

### General Rules

- Every state (loading, saving, validating, error, success) has a visible,
  labelled indicator; a bare spinner is not enough. Fetching, saving and
  processing are distinct states with distinct labels.
- Validation errors appear next to the offending field with the specific reason.
- Destructive actions confirm with context: what will be deleted and what else
  is affected.
- Empty states explain how to populate them.
- Confirm success explicitly (toast, inline check, or badge).
- Cold-read test: would a first-time user on this screen know what to do next?

## Accessibility

- Every input has a `<label for>`; helper text is linked with `aria-describedby`
- Icon-only buttons have an `aria-label`; when a control also has visible text,
  its accessible name must contain that text (WCAG 2.5.3)
- Live regions: `role="status"` for progress (it implies `aria-live="polite"`),
  `role="alert"` for errors
- Run `npm run test:a11y` for changes to interactive components

## Static Analysis (ast-grep)

`npm run check:all` includes `npm run ast:all`. The rule sets in `rules/` are
meant to catch Svelte 4 patterns (`export let`, `$:`, slots, `on:`), XSS via
`{@html}`, unsafe `localStorage` use, rune misuse, and convention breaks (console
over logger, date formatting). Individual sets: `ast:migration`,
`ast:best-practices`, `ast:svelte5`, `ast:security`. Guide:
`doc/AST-GREP-SETUP.md`.

A rule file that ast-grep cannot load as a rule set runs zero rules and still
exits cleanly, so a green `ast:all` is not proof a rule works. When you add or
change a rule, confirm it fires on a file that contains a guaranteed match.

## Testing

- `src/test/setup.ts` is loaded for every test by the Vitest config and mocks,
  among others: `$lib/utils/logger`, `$lib/utils/transitions`,
  `$lib/stores/toast`, `$lib/i18n`, `$lib/utils/settingsApi.js`,
  `$lib/utils/security`, `$app/navigation`, `$app/stores`, MapLibre,
  `window.location`, and global `fetch`. Read it before adding a `vi.mock()`;
  do not duplicate those mocks per file.
- The global `fetch` mock serves the translation files, answers every `/api/`
  URL with `200 {data: []}`, and rejects anything else. A test of an error path must override it, or it will
  silently exercise the success path.
- To test a module that `setup.ts` mocks, call `vi.unmock('<module id>')` at the
  top of the test file (see `src/lib/utils/logger.test.ts`).
- Override a shared mock for one test with a one-shot value
  (`vi.mocked(fn).mockReturnValueOnce(...)`, `mockResolvedValueOnce(...)`). A
  persistent `mockReturnValue` survives `vi.clearAllMocks()`, which only clears
  call history, and leaks into later tests; use `vi.resetAllMocks()` in
  `beforeEach` if you need persistent overrides. Reset any store you mutate.
- Render components with `renderTyped()` from `src/test/render-helpers.ts`
  instead of casting to `any`. Name tests with `it` (ESLint enforces it).
- Files named `*.integration.test.ts`, `*.browser.test.ts` or
  `*.reverse-proxy.test.ts` are excluded from `npm test`; do not use those
  suffixes for unit tests.
- Tests that deliberately pass malformed data may use `any`, but only with a
  scoped `// eslint-disable-next-line @typescript-eslint/no-explicit-any` and a
  comment saying why. Prefer `unknown`, or define a local
  `type DeepPartial<T> = { [K in keyof T]?: DeepPartial<T[K]> }` for partial
  fixtures.
- Put component tests next to the component (`Component.test.ts`).
