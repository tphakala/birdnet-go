# Telemetry Hardening & Frontend Sentry Wiring

**Date:** 2026-03-22
**Issues:** #41, #42, #43, #44, #45 (Forgejo)
**Status:** Design approved

## Overview

Five telemetry issues spanning backend robustness, frontend validation, logger-to-Sentry wiring, and CI source map uploads. Grouped into three PRs by area.

## PR 1: Backend Telemetry Hardening (#41, #42)

### #41 — CaptureError nil guard

**Problem:** `CaptureError()` in `internal/telemetry/sentry.go` (function starts at line 667) calls `err.Error()` without a nil check. Passing a nil error panics the application.

**Fix:** Add nil guard at the top of `CaptureError()`. Log a warning so the caller bug is discoverable. `FastCaptureError()` does not need its own guard — it delegates to `CaptureError()` which handles it.

```go
func CaptureError(err error, component string) {
    if err == nil {
        GetLogger().Warn("CaptureError called with nil error",
            logger.String("component", component))
        return
    }
    // ... existing code unchanged
}
```

### #42 — Bounded deferredMessages with unconditional drain

**Problem:** `CaptureMessageDeferred()` appends to `deferredMessages` without limit. If Sentry never initializes (user opted out or init fails), messages accumulate forever.

**Fix:** Two changes:

1. **Cap at 100 messages** in `CaptureMessageDeferred()`. Drop newest (preserves earliest root-cause messages). Log warning only on the first drop to avoid log spam.

```go
const maxDeferredMessages = 100

// Package-level flag to log only the first drop
var deferredOverflowLogged bool

// In CaptureMessageDeferred, after the sentryInitialized check:
if len(deferredMessages) >= maxDeferredMessages {
    if !deferredOverflowLogged {
        deferredOverflowLogged = true
        GetLogger().Warn("deferred message queue full, dropping new messages",
            logger.Int("max", maxDeferredMessages))
    }
    return
}
```

2. **Drain on opt-out** in `InitSentry()`. When Sentry is explicitly disabled, clear the queue and mark as initialized so `CaptureMessageDeferred` routes future calls through `CaptureMessage` (which will no-op via `shouldSkipTelemetry`).

```go
func InitSentry(settings *conf.Settings) error {
    if !settings.Sentry.Enabled {
        GetLogger().Info("Sentry telemetry is disabled (opt-in required)")
        // Drain deferred messages — Sentry will never initialize
        deferredMutex.Lock()
        deferredMessages = nil
        sentryInitialized = true
        deferredMutex.Unlock()
        return nil
    }
    // ... rest unchanged (processDeferredMessages sets sentryInitialized=true on success)
}
```

**Note on error path:** If `initializeSentrySDK` fails, deferred messages are preserved (not drained). The cap prevents unbounded growth in this case. This avoids a bad state where `sentryInitialized = true` but Sentry isn't actually working.

**Why this approach:**
- Cap prevents unbounded growth during the startup window and during SDK init failure
- Explicit drain on opt-out handles the normal case where we know Sentry will never initialize
- Drop-newest preserves root-cause messages (earliest errors are typically the trigger)
- 100 is hardcoded, not configurable — this is an internal safety net, not user-facing

### Testing

- Test nil error handling: verify no panic, warning logged
- Test cap enforcement: queue 101+ messages, verify only 100 retained
- Test drain on opt-out: call InitSentry with Sentry disabled, verify deferredMessages is nil and sentryInitialized is true
- Test drain on success: existing processDeferredMessages tests cover this path
- Test SDK init failure: verify deferredMessages are preserved (not drained) when initializeSentrySDK returns error

---

## PR 2: Frontend Telemetry Wiring (#43, #44)

### #43 — FormData Blob validation

**Problem:** `validateAndSanitizeBody()` in `frontend/src/lib/utils/api.ts:219-228` checks `string` and `File` types when measuring FormData size, but misses `Blob` entries. A raw Blob attachment would be counted as 0 bytes, bypassing the 10MB client-side limit.

**Fix:** Replace `File` check with `Blob` check. Since `File extends Blob`, `instanceof Blob` catches both.

```typescript
// Before (api.ts:222-227):
if (typeof value === 'string') {
    totalSize += value.length;
} else if (value instanceof File) {
    totalSize += value.size;
}

// After:
if (typeof value === 'string') {
    totalSize += value.length;
} else if (value instanceof Blob) {
    // Blob covers both Blob and File (File extends Blob)
    totalSize += value.size;
}
```

**Risk context:** Low practical risk — backend enforces 1MB limit at the middleware level — but the frontend validation should be correct.

### #44 — Wire logger.error() to Sentry

**Problem:** `logger.error()` in `frontend/src/lib/utils/logger.ts` has a commented-out Sentry placeholder (lines 181-187) but doesn't actually send errors to Sentry. Only API errors are captured (via `captureApiError` in `api.ts`).

**Design:** Use dependency injection to avoid circular dependency between `logger.ts` and `appState.svelte.ts`.

#### 1. New `captureError` function in `sentry.ts`

```typescript
export function captureError(
    error: Error,
    context?: { category?: string; [key: string]: unknown }
): void {
    Sentry.withScope(scope => {
        scope.setLevel('error');
        scope.setTag('error.type', 'logger');
        if (context?.category) {
            scope.setTag('logger.category', context.category);
        }
        if (context) {
            const { category, ...rest } = context;
            if (Object.keys(rest).length > 0) {
                scope.setContext('logger', rest);
            }
        }
        Sentry.captureException(error);
    });
}
```

#### 2. Injection hook in `logger.ts` (no appState import)

```typescript
type CaptureErrorFn = ((error: Error, context?: { category?: string }) => void) | null;
let _sentryCaptureError: CaptureErrorFn = null;

/** Called by appState after Sentry initializes. Pass null to disconnect. */
export function setSentryCaptureError(fn: CaptureErrorFn): void {
    _sentryCaptureError = fn;
}
```

In the `error()` method, after the console.error call:
```typescript
if (error instanceof Error && _sentryCaptureError) {
    _sentryCaptureError(error, { category });
}
```

#### 3. Wiring in `appState.svelte.ts`

```typescript
import { setSentryCaptureError } from '$lib/utils/logger';

// In the Sentry init .then() block:
import('$lib/telemetry/sentry')
    .then(({ initSentry, captureError }) => {
        initSentry({ dsn, systemId, version });
        setSentryCaptureError(captureError);
        logger.info('Frontend Sentry initialized');
    })
    .catch(err => {
        logger.warn('Failed to initialize Sentry', err);
    });
```

#### Key decisions

- **Only `logger.error()` wired**, not `logger.warn()` — warnings are too noisy for Sentry
- **Only `Error` instances captured**, not string messages — avoids noise from `logger.error('message', contextObj)` calls
- **Synchronous call**, not fire-and-forget promise — Sentry is already initialized when the setter runs
- **Nullable setter** — allows disconnecting when telemetry is disabled at runtime (`setSentryCaptureError(null)`)
- **No circular dependency** — `logger.ts` has zero imports from `appState`; `appState` imports from `logger` (existing, safe)
- **Errors during initial page load** (before Sentry init) go to console only — acceptable tradeoff for lazy-loaded telemetry
- **Throttled errors are not captured** — if the logger's throttle mechanism suppresses an error (via `throttleKey`), it won't reach Sentry either. This is intentional: prevents quota exhaustion for repetitive errors.

#### Coverage note

Many existing `logger.error()` call sites pass non-Error second arguments (e.g., context objects). These calls will NOT trigger Sentry capture — only calls with `logger.error('message', errorInstance)` will. This is by design to avoid noise, but callers wanting Sentry visibility should pass Error instances.

#### Cleanup: Remove commented-out Sentry placeholder

Delete the `// Future: This is where Sentry integration would go` block in logger.ts `error()` method.

### Testing

- #43: Test FormData with Blob entry, verify size counted correctly
- #44: Test setSentryCaptureError wiring — mock captureError, trigger logger.error with Error instance, verify mock called with correct category
- #44: Test null setter — set to null, verify logger.error does not call Sentry
- #44: Test non-Error argument — verify Sentry not called for string messages

---

## PR 3: Source Map Upload in CI (#45)

### Problem

Frontend Sentry errors show minified stack traces with no file/line mapping. Source maps are not generated during build and not uploaded to Sentry.

### Fix

Three changes:

#### 1. Fix release format mismatch between frontend and backend

The backend sets `release: "birdnet-go@0.9.0"` (sentry.go:161), but the frontend sets `release: config.version` (sentry.ts:32) which is just `"0.9.0"`. Source maps must match the frontend's release identifier.

Fix in `frontend/src/lib/telemetry/sentry.ts`:

```typescript
// Before:
release: config.version,

// After:
release: `birdnet-go@${config.version}`,
```

This aligns the frontend release format with the backend (`birdnet-go@VERSION`), ensuring source maps uploaded with `--release "birdnet-go@VERSION"` are correctly associated with frontend events.

#### 2. Enable hidden source maps in Vite

In `frontend/vite.config.js`, add `sourcemap: 'hidden'` to the build config:

```javascript
build: {
    sourcemap: 'hidden', // Generate .map files but don't reference in bundles
    outDir: 'dist',
    // ... rest unchanged
}
```

`'hidden'` generates `.map` files without adding `//# sourceMappingURL` comments to the output bundles. This prevents exposing source maps to end users while making them available for Sentry upload.

#### 3. Add source map upload step to CI workflows

**CI structure context:** The Taskfile builds frontend as a dependency of the platform target (`task linux_amd64` depends on `frontend-build`). There is no separate frontend build step in CI — it's all one `Build BirdNET-Go` step.

**Approach:** Add source map upload and deletion as **post-build steps** after `Build BirdNET-Go`. The `.map` files will be embedded in the Go binary for the uploading matrix entry only (linux/amd64). This is acceptable because:
- Source maps are `hidden` (no `sourceMappingURL` in JS output)
- They're embedded in the Go binary's `embed.FS` but not served to users
- The extra binary size (~1-3MB) is negligible
- Only the linux/amd64 build is affected; other matrix entries don't generate .map files

For `nightly-build.yml` (already has Node.js setup):

```yaml
- name: Upload source maps to Sentry
  if: matrix.goos == 'linux' && matrix.goarch == 'amd64'
  env:
    SENTRY_AUTH_TOKEN: ${{ secrets.SENTRY_AUTH_TOKEN }}
    SENTRY_ORG: <org-slug>
    SENTRY_PROJECT: <project-slug>
  run: |
    npx @sentry/cli sourcemaps upload \
      --release "birdnet-go@${{ env.BUILD_VERSION }}" \
      --url-prefix '~/ui/assets/' \
      frontend/dist/
```

For `release-build.yml` (does NOT have Node.js setup — needs it added):

```yaml
# Add Node.js setup step (release-build.yml currently lacks this)
- name: Setup Node.js
  if: matrix.goos == 'linux' && matrix.goarch == 'amd64'
  uses: actions/setup-node@v6
  with:
    node-version: '24'

- name: Upload source maps to Sentry
  if: matrix.goos == 'linux' && matrix.goarch == 'amd64'
  env:
    SENTRY_AUTH_TOKEN: ${{ secrets.SENTRY_AUTH_TOKEN }}
    SENTRY_ORG: <org-slug>
    SENTRY_PROJECT: <project-slug>
  run: |
    npx @sentry/cli sourcemaps upload \
      --release "birdnet-go@${{ github.ref_name }}" \
      --url-prefix '~/ui/assets/' \
      frontend/dist/
```

Note: `release-build.yml` uses `${{ github.ref_name }}` directly (the release tag), not `${{ env.BUILD_VERSION }}` which is only set as a step-level env var inside `Build BirdNET-Go`. `nightly-build.yml` sets `BUILD_VERSION` as a workflow-level env var via `$GITHUB_ENV`, so `${{ env.BUILD_VERSION }}` works there.

**Key decisions:**
- **`sentry-cli` over `@sentry/vite-plugin`** — keeps `npm run build` credential-free, community forks build without Sentry env vars
- **Upload only once** (`linux/amd64` matrix entry) — same frontend bundle across all platforms
- **Post-build upload** — avoids Taskfile restructuring; .map files embedded in one binary variant is acceptable
- **No .map deletion step** — since upload happens post-build, the binary is already built. Deletion would only save disk in the CI runner (not worth the complexity)
- **`--url-prefix '~/ui/assets/'`** — matches Vite's `base: '/ui/assets/'` setting
- **Release format `birdnet-go@VERSION`** — now consistent between backend, frontend, and sentry-cli

### Prerequisites

- Create `SENTRY_AUTH_TOKEN` repository secret in GitHub
- Note the Sentry org slug and project slug for the workflow

### Testing

- Build locally with `sourcemap: 'hidden'`, verify `.map` files generated, no `sourceMappingURL` in `.js` files
- Verify `sentry-cli sourcemaps upload` command works with test credentials
- Trigger a frontend error in staging, confirm readable stack trace in Sentry
- Verify release identifier matches between frontend events and uploaded source maps

---

## PR Ordering

PRs are independent and can be developed/merged in parallel:

| PR | Scope | Risk | Size |
|----|-------|------|------|
| PR 1 | Backend Go | Low — defensive guards | Small |
| PR 2 | Frontend TS | Low — additive wiring | Medium |
| PR 3 | CI + Vite config | Low — build pipeline only | Small |

No ordering dependencies between PRs. PR 3 requires a Sentry auth token secret to be configured before the CI step works.
