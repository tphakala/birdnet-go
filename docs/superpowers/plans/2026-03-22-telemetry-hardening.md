# Telemetry Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix five telemetry bugs: nil-panic in CaptureError, unbounded deferred message queue, FormData Blob validation, logger-to-Sentry wiring, and CI source map uploads.

**Architecture:** Three independent PRs — backend Go hardening, frontend TypeScript wiring, and CI pipeline changes. Each PR is self-contained and can be merged independently.

**Tech Stack:** Go 1.26 (testify), TypeScript/Svelte 5 (Vitest), GitHub Actions CI

**Spec:** `docs/superpowers/specs/2026-03-22-telemetry-hardening-design.md`

---

## File Map

### PR 1: Backend Telemetry Hardening
| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/telemetry/sentry.go` | Add nil guard to CaptureError, add cap + drain to deferred messages |
| Modify | `internal/telemetry/telemetry_test.go` | Tests for nil guard, cap enforcement, drain behavior |

### PR 2: Frontend Telemetry Wiring
| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `frontend/src/lib/utils/api.ts` | Fix Blob validation in validateAndSanitizeBody |
| Modify | `frontend/src/lib/utils/api.test.ts` | Test for Blob size counting |
| Modify | `frontend/src/lib/telemetry/sentry.ts` | Add captureError function |
| Modify | `frontend/src/lib/telemetry/sentry.test.ts` | Tests for captureError |
| Modify | `frontend/src/lib/utils/logger.ts` | Add setSentryCaptureError injection hook, wire error() |
| Create | `frontend/src/lib/utils/logger.test.ts` | Tests for logger Sentry integration |
| Modify | `frontend/src/lib/stores/appState.svelte.ts` | Wire setSentryCaptureError after Sentry init |

### PR 3: Source Map Upload in CI
| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `frontend/src/lib/telemetry/sentry.ts` | Fix release format to match backend |
| Modify | `frontend/src/lib/telemetry/sentry.test.ts` | Update release assertion |
| Modify | `frontend/vite.config.js` | Enable hidden source maps |
| Modify | `.github/workflows/nightly-build.yml` | Add source map upload step |
| Modify | `.github/workflows/release-build.yml` | Add Node.js setup + source map upload step |

---

## PR 1: Backend Telemetry Hardening (#41, #42)

### Task 1: CaptureError nil guard

**Files:**
- Modify: `internal/telemetry/sentry.go:667` (CaptureError function)
- Modify: `internal/telemetry/telemetry_test.go`

- [ ] **Step 1: Write failing test for nil error**

Add to `internal/telemetry/telemetry_test.go`:

```go
func TestCaptureError_NilError(t *testing.T) {
	t.Parallel()

	// Enable test mode so telemetry functions don't skip
	EnableTestMode()
	t.Cleanup(func() {
		DisableTestMode()
	})

	// CaptureError with nil should not panic
	assert.NotPanics(t, func() {
		CaptureError(nil, "test-component")
	}, "CaptureError should not panic on nil error")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race -run TestCaptureError_NilError ./internal/telemetry/`
Expected: FAIL — panics with nil pointer dereference at `err.Error()`

- [ ] **Step 3: Add nil guard to CaptureError**

In `internal/telemetry/sentry.go`, at the top of `CaptureError` (line 667), before the `shouldSkipTelemetry` check:

```go
func CaptureError(err error, component string) {
	if err == nil {
		GetLogger().Warn("CaptureError called with nil error",
			logger.String("component", component))
		return
	}

	if shouldSkipTelemetry() {
		return
	}
	// ... rest unchanged
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race -run TestCaptureError_NilError ./internal/telemetry/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/telemetry/sentry.go internal/telemetry/telemetry_test.go
git commit -m "fix: add nil guard to CaptureError to prevent panic"
```

---

### Task 2: Bounded deferred message queue — cap

**Files:**
- Modify: `internal/telemetry/sentry.go` (constants, CaptureMessageDeferred)
- Modify: `internal/telemetry/telemetry_test.go`

- [ ] **Step 1: Write failing test for cap enforcement**

Add to `internal/telemetry/telemetry_test.go`:

```go
func TestCaptureMessageDeferred_Cap(t *testing.T) {
	// Not parallel — mutates package-level state

	EnableTestMode()
	t.Cleanup(func() {
		DisableTestMode()
		// Reset deferred state
		deferredMutex.Lock()
		deferredMessages = nil
		sentryInitialized = false
		deferredOverflowLogged = false
		deferredMutex.Unlock()
	})

	// Reset state so messages are deferred (not sent immediately)
	deferredMutex.Lock()
	deferredMessages = nil
	sentryInitialized = false
	deferredOverflowLogged = false
	deferredMutex.Unlock()

	// Queue more than maxDeferredMessages
	for i := range maxDeferredMessages + 50 {
		CaptureMessageDeferred(
			fmt.Sprintf("msg-%d", i),
			sentry.LevelWarning,
			"test",
		)
	}

	deferredMutex.Lock()
	count := len(deferredMessages)
	deferredMutex.Unlock()

	assert.Equal(t, maxDeferredMessages, count,
		"deferred queue should be capped at maxDeferredMessages")
}
```

Note: The test references `maxDeferredMessages` and `deferredOverflowLogged` which don't exist yet. The test won't compile until Step 3 is applied. Write both the test and implementation, then run.

- [ ] **Step 2: Add cap constant, overflow flag, and cap logic**

In `internal/telemetry/sentry.go`, add constant and flag near the existing `deferredMessages` var block (around line 50):

```go
// maxDeferredMessages is the maximum number of messages that can be queued
// before Sentry initializes. Prevents unbounded memory growth.
const maxDeferredMessages = 100

var (
	sentryInitialized    bool
	deferredMessages     []DeferredMessage
	deferredMutex        sync.Mutex
	deferredOverflowLogged bool
	attachmentUploader   *AttachmentUploader
	testMode             int32
)
```

In `CaptureMessageDeferred`, after the `sentryInitialized` check and before the existing append (around line 811):

```go
	// Cap deferred messages to prevent unbounded growth
	if len(deferredMessages) >= maxDeferredMessages {
		if !deferredOverflowLogged {
			deferredOverflowLogged = true
			GetLogger().Warn("deferred message queue full, dropping new messages",
				logger.Int("max", maxDeferredMessages))
		}
		return
	}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test -race -run TestCaptureMessageDeferred_Cap ./internal/telemetry/`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/telemetry/sentry.go internal/telemetry/telemetry_test.go
git commit -m "fix: cap deferred message queue at 100 to prevent unbounded growth"
```

---

### Task 3: Drain deferred messages on opt-out

**Files:**
- Modify: `internal/telemetry/sentry.go` (InitSentry)
- Modify: `internal/telemetry/telemetry_test.go`

- [ ] **Step 1: Write failing test for drain on opt-out**

Add to `internal/telemetry/telemetry_test.go`:

```go
func TestInitSentry_DisabledDrainsQueue(t *testing.T) {
	// Not parallel — mutates package-level state

	EnableTestMode()
	t.Cleanup(func() {
		DisableTestMode()
		deferredMutex.Lock()
		deferredMessages = nil
		sentryInitialized = false
		deferredOverflowLogged = false
		deferredMutex.Unlock()
	})

	// Pre-populate deferred messages
	deferredMutex.Lock()
	deferredMessages = []DeferredMessage{
		{Message: "msg-1", Level: sentry.LevelWarning, Component: "test"},
		{Message: "msg-2", Level: sentry.LevelError, Component: "test"},
	}
	sentryInitialized = false
	deferredMutex.Unlock()

	// Call InitSentry with Sentry disabled
	settings := &conf.Settings{}
	settings.Sentry.Enabled = false

	err := InitSentry(settings)
	require.NoError(t, err)

	// Verify queue is drained and state is set
	deferredMutex.Lock()
	defer deferredMutex.Unlock()

	assert.Nil(t, deferredMessages, "deferred messages should be nil after opt-out drain")
	assert.True(t, sentryInitialized, "sentryInitialized should be true after opt-out")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race -run TestInitSentry_DisabledDrainsQueue ./internal/telemetry/`
Expected: FAIL — `sentryInitialized` remains false, deferredMessages not cleared

- [ ] **Step 3: Add drain logic to InitSentry opt-out path**

In `internal/telemetry/sentry.go`, modify the `InitSentry` function's disabled branch (around line 106):

```go
func InitSentry(settings *conf.Settings) error {
	// Check if Sentry is explicitly enabled (opt-in)
	if !settings.Sentry.Enabled {
		GetLogger().Info("Sentry telemetry is disabled (opt-in required)")
		// Drain deferred messages — Sentry will never initialize
		deferredMutex.Lock()
		deferredMessages = nil
		sentryInitialized = true
		deferredMutex.Unlock()
		return nil
	}
	// ... rest unchanged
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race -run TestInitSentry_DisabledDrainsQueue ./internal/telemetry/`
Expected: PASS

- [ ] **Step 5: Run all telemetry tests**

Run: `go test -race -v ./internal/telemetry/`
Expected: All tests pass

- [ ] **Step 6: Run linter**

Run: `golangci-lint run -v ./internal/telemetry/...`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add internal/telemetry/sentry.go internal/telemetry/telemetry_test.go
git commit -m "fix: drain deferred message queue when Sentry is disabled"
```

---

## PR 2: Frontend Telemetry Wiring (#43, #44)

### Task 4: FormData Blob validation fix

**Files:**
- Modify: `frontend/src/lib/utils/api.ts:225` (validateAndSanitizeBody)
- Modify: `frontend/src/lib/utils/api.test.ts`

- [ ] **Step 1: Apply the Blob fix**

In `frontend/src/lib/utils/api.ts`, in the `validateAndSanitizeBody` function (around line 225), change:

```typescript
// Before:
      } else if (value instanceof File) {
        totalSize += value.size;
      }

// After:
      } else if (value instanceof Blob) {
        // Blob covers both Blob and File (File extends Blob)
        totalSize += value.size;
      }
```

This is a one-line change (`File` → `Blob`). Since `validateAndSanitizeBody` is not exported and testing it indirectly via `api.post` requires full mock fetch setup, the fix is verified by code review and existing tests.

- [ ] **Step 2: Run frontend tests**

Run: `cd frontend && npm test -- --run`
Expected: All tests pass (no regressions)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/utils/api.ts
git commit -m "fix: count Blob size in FormData validation"
```

---

### Task 5: Add captureError to sentry.ts

**Files:**
- Modify: `frontend/src/lib/telemetry/sentry.ts`
- Modify: `frontend/src/lib/telemetry/sentry.test.ts`

- [ ] **Step 1: Write failing tests for captureError**

Add to `frontend/src/lib/telemetry/sentry.test.ts`, after the existing `captureApiError` describe block:

First, update the import at the top of the test file to include `captureError`:

```typescript
import { initSentry, captureApiError, captureError } from './sentry';
```

Then add the test describe block:

```typescript
describe('captureError', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    initSentry({ dsn: 'https://test@sentry.io/123', systemId: 'sys-1', version: '1.0.0' });
  });

  it('captures Error with logger tag', () => {
    const error = new Error('test error');
    captureError(error, { category: 'ui' });

    expect(Sentry.withScope).toHaveBeenCalledOnce();
    expect(Sentry.captureException).toHaveBeenCalledWith(error);
  });

  it('sets logger.category tag from context', () => {
    const error = new Error('test error');

    // Override the withScope mock to inspect scope calls
    const mockScope = {
      setLevel: vi.fn(),
      setTag: vi.fn(),
      setContext: vi.fn(),
    };
    vi.mocked(Sentry.withScope).mockImplementation((callback) => {
      callback(mockScope as unknown as Sentry.Scope);
    });

    captureError(error, { category: 'settings' });

    expect(mockScope.setTag).toHaveBeenCalledWith('error.type', 'logger');
    expect(mockScope.setTag).toHaveBeenCalledWith('logger.category', 'settings');
  });

  it('works without context', () => {
    const error = new Error('bare error');

    expect(() => captureError(error)).not.toThrow();
    expect(Sentry.captureException).toHaveBeenCalledWith(error);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd frontend && npx vitest run src/lib/telemetry/sentry.test.ts`
Expected: FAIL — `captureError` is not exported from `./sentry`

- [ ] **Step 3: Add captureError function to sentry.ts**

Add to `frontend/src/lib/telemetry/sentry.ts`, after the `captureApiError` function:

```typescript
/**
 * Capture a non-API error from logger.error() calls.
 * Used via dependency injection from logger.ts to avoid circular imports.
 */
export function captureError(
  error: Error,
  context?: { category?: string; [key: string]: unknown },
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/lib/telemetry/sentry.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/telemetry/sentry.ts frontend/src/lib/telemetry/sentry.test.ts
git commit -m "feat: add captureError function to frontend Sentry module"
```

---

### Task 6: Wire logger.error() to Sentry via injection

**Files:**
- Modify: `frontend/src/lib/utils/logger.ts` (add injection hook + wire error method)
- Create: `frontend/src/lib/utils/logger.test.ts`
- Modify: `frontend/src/lib/stores/appState.svelte.ts` (wire setter after Sentry init)

- [ ] **Step 1: Write tests for logger Sentry integration**

Create `frontend/src/lib/utils/logger.test.ts`:

```typescript
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { getLogger, setSentryCaptureError } from './logger';

describe('logger Sentry integration', () => {
  const mockCapture = vi.fn();
  let logger: ReturnType<typeof getLogger>;

  beforeEach(() => {
    vi.clearAllMocks();
    logger = getLogger('test-category');
  });

  afterEach(() => {
    // Disconnect Sentry after each test
    setSentryCaptureError(null);
  });

  it('calls Sentry captureError when wired and error is Error instance', () => {
    setSentryCaptureError(mockCapture);
    const error = new Error('test error');

    logger.error('something failed', error);

    expect(mockCapture).toHaveBeenCalledOnce();
    expect(mockCapture).toHaveBeenCalledWith(error, { category: 'test-category' });
  });

  it('does not call Sentry when not wired', () => {
    const error = new Error('test error');
    logger.error('something failed', error);

    expect(mockCapture).not.toHaveBeenCalled();
  });

  it('does not call Sentry for non-Error second argument', () => {
    setSentryCaptureError(mockCapture);
    logger.error('something failed', { details: 'context object' });

    expect(mockCapture).not.toHaveBeenCalled();
  });

  it('does not call Sentry after disconnecting with null', () => {
    setSentryCaptureError(mockCapture);
    setSentryCaptureError(null);
    const error = new Error('test error');

    logger.error('something failed', error);

    expect(mockCapture).not.toHaveBeenCalled();
  });

  it('does not call Sentry for console-style multi-arg calls', () => {
    setSentryCaptureError(mockCapture);
    // Console-style: logger.error('msg', error, 'extra', 'data')
    // When args[2] is a string, it takes the console-style path
    logger.error('failed', new Error('err'), 'extra info');

    expect(mockCapture).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd frontend && npx vitest run src/lib/utils/logger.test.ts`
Expected: FAIL — `setSentryCaptureError` is not exported from `./logger`

- [ ] **Step 3: Add injection hook to logger.ts**

In `frontend/src/lib/utils/logger.ts`, add after the throttle logic (around line 75, before `getLogger`):

```typescript
// Sentry integration hook — set by appState after Sentry initializes.
// Uses dependency injection to avoid circular imports with appState.
type CaptureErrorFn = ((error: Error, context?: { category?: string }) => void) | null;
let _sentryCaptureError: CaptureErrorFn = null;

/** Called by appState after Sentry initializes. Pass null to disconnect. */
export function setSentryCaptureError(fn: CaptureErrorFn): void {
  _sentryCaptureError = fn;
}
```

- [ ] **Step 4: Wire Sentry capture in the error() method**

In `frontend/src/lib/utils/logger.ts`, in the `error()` method of `getLogger`:

First, delete the commented-out Sentry placeholder (the `// Future: This is where Sentry integration would go` block, approximately lines 181-187).

Then, add Sentry capture in the structured-style code path. The structure of `error()` is:

```
error(...args) {
  if (args.length === 0) return;
  // Lines 147-157: console-style early return (when args[2] is string/number/null)
  //   → console.error(...) and return — Sentry NOT called here

  // Lines 159-163: extract message, error, context, throttleKey
  // Line 166: throttle check (early return if throttled)
  // Lines 171-176: build errorData object
  // Lines 178-192: three branches for console.error output:
  //   if (error instanceof Error) → console.error(...)
  //   else if (error) → console.error(...)
  //   else → console.error(...)

  // ← INSERT SENTRY CAPTURE HERE (after all three console.error branches)
}
```

Add after the last `console.error` branch (after the closing `}` of the `else` block, before the method's closing `}`):

```typescript
      // Sentry capture for Error instances (non-API errors)
      if (error instanceof Error && _sentryCaptureError) {
        _sentryCaptureError(error, { category });
      }
```

This ensures: (1) console-style calls take the early return and skip Sentry, (2) throttled errors skip Sentry, (3) only structured-style calls with Error instances reach Sentry.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/lib/utils/logger.test.ts`
Expected: PASS

- [ ] **Step 6: Wire setSentryCaptureError in appState.svelte.ts**

In `frontend/src/lib/stores/appState.svelte.ts`:

Add import at the top (alongside existing logger import):
```typescript
import { setSentryCaptureError } from '../utils/logger';
```

Modify the Sentry init block (around line 281) to call setSentryCaptureError:

```typescript
      if (sentryConfig?.enabled && sentryConfig.dsn) {
        sentryEnabled = true;
        import('$lib/telemetry/sentry')
          .then(({ initSentry, captureError }) => {
            initSentry({
              dsn: sentryConfig.dsn,
              systemId: sentryConfig.systemId,
              version: config.version,
            });
            setSentryCaptureError(captureError);
            logger.info('Frontend Sentry initialized');
          })
          .catch(err => {
            logger.warn('Failed to initialize Sentry', err);
          });
      }
```

- [ ] **Step 7: Run all frontend tests**

Run: `cd frontend && npm test -- --run`
Expected: All tests pass

- [ ] **Step 8: Run frontend linting**

Run: `cd frontend && npm run check:all`
Expected: No errors

- [ ] **Step 9: Commit**

```bash
git add frontend/src/lib/utils/logger.ts frontend/src/lib/utils/logger.test.ts frontend/src/lib/stores/appState.svelte.ts
git commit -m "feat: wire logger.error() to Sentry via dependency injection"
```

---

## PR 3: Source Map Upload in CI (#45)

### Task 7: Fix frontend Sentry release format

**Files:**
- Modify: `frontend/src/lib/telemetry/sentry.ts:32`
- Modify: `frontend/src/lib/telemetry/sentry.test.ts:35`

- [ ] **Step 1: Update test to expect new release format**

In `frontend/src/lib/telemetry/sentry.test.ts`, update the `initSentry` test (around line 35):

```typescript
// Before:
      release: '1.0.0',

// After:
      release: 'birdnet-go@1.0.0',
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/lib/telemetry/sentry.test.ts`
Expected: FAIL — release is `'1.0.0'` not `'birdnet-go@1.0.0'`

- [ ] **Step 3: Fix release format in sentry.ts**

In `frontend/src/lib/telemetry/sentry.ts`, in `initSentry` (line 32):

```typescript
// Before:
    release: config.version,

// After:
    release: `birdnet-go@${config.version}`,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/lib/telemetry/sentry.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/telemetry/sentry.ts frontend/src/lib/telemetry/sentry.test.ts
git commit -m "fix: align frontend Sentry release format with backend"
```

---

### Task 8: Enable hidden source maps in Vite

**Files:**
- Modify: `frontend/vite.config.js`

- [ ] **Step 1: Add sourcemap setting to Vite build config**

In `frontend/vite.config.js`, in the `build` object (around line 77):

```javascript
  build: {
    sourcemap: 'hidden', // Generate .map files for Sentry, no sourceMappingURL in bundles
    outDir: 'dist',
    // ... rest unchanged
```

- [ ] **Step 2: Verify source maps are generated**

Run: `cd frontend && npm run build && ls -la dist/*.map 2>/dev/null && echo "Source maps found" || echo "No source maps"`
Expected: `.map` files present in `dist/`

- [ ] **Step 3: Verify no sourceMappingURL in JS bundles**

Run: `cd frontend && grep -r 'sourceMappingURL' dist/*.js && echo "FAIL: sourceMappingURL found" || echo "PASS: no sourceMappingURL"`
Expected: PASS — no sourceMappingURL references in JS files

- [ ] **Step 4: Commit**

```bash
git add frontend/vite.config.js
git commit -m "feat: enable hidden source map generation for Sentry"
```

---

### Task 9: Add source map upload to CI workflows

**Files:**
- Modify: `.github/workflows/nightly-build.yml`
- Modify: `.github/workflows/release-build.yml`

- [ ] **Step 1: Add upload step to nightly-build.yml**

In `.github/workflows/nightly-build.yml`, after the `Build BirdNET-Go` step, add:

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

Replace `<org-slug>` and `<project-slug>` with actual Sentry org and project slugs.

- [ ] **Step 2: Add Node.js setup and upload step to release-build.yml**

In `.github/workflows/release-build.yml`, after the `Build BirdNET-Go` step, add:

```yaml
      - name: Setup Node.js for Sentry CLI
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

Replace `<org-slug>` and `<project-slug>` with actual Sentry org and project slugs.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/nightly-build.yml .github/workflows/release-build.yml
git commit -m "ci: add source map upload to Sentry in release and nightly builds"
```

---

## Final Validation

After all tasks are complete for each PR:

### PR 1 (Backend)
- [ ] `go test -race -v ./internal/telemetry/`
- [ ] `golangci-lint run -v ./internal/telemetry/...`

### PR 2 (Frontend)
- [ ] `cd frontend && npm test -- --run`
- [ ] `cd frontend && npm run check:all`

### PR 3 (CI + config)
- [ ] `cd frontend && npm test -- --run` (for release format test)
- [ ] `cd frontend && npm run build` (verify source maps generated)
- [ ] Manual: verify `SENTRY_AUTH_TOKEN` secret exists in GitHub repo settings
