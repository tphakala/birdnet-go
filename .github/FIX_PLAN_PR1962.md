# Fix Plan for PR #1962 - Error Classification Issues

## Overview

This document outlines fixes for issues identified in PR #1962 code review. The PR successfully implements operational error classification in the generator layer, but several areas need adjustment to fully achieve the goal of reducing false alerts.

## Issue #1: API Layer Logging Inconsistency (HIGH PRIORITY - MUST FIX)

### Problem

The API layer in `handleAutoPreRenderMode` logs ALL errors at Error level, overriding the operational error classification done in the generator layer. This defeats the PR's purpose for this code path.

**Location**: `internal/api/v2/media.go:589-596`

### Current Code

```go
func (c *Controller) handleAutoPreRenderMode(ctx echo.Context, noteID, clipPath string, params spectrogramParameters) error {
    // Auto or prerender mode - generate on-demand if needed
    generationStart := time.Now()
    spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), clipPath, params.width, params.raw)
    generationDuration := time.Since(generationStart)

    if err != nil {
        c.logErrorIfEnabled("Spectrogram generation failed",  // ← Always logs as Error
            logger.String("note_id", noteID),
            logger.String("clip_path", clipPath),
            logger.Error(err),
            logger.Int64("duration_ms", generationDuration.Milliseconds()),
            logger.String("path", ctx.Request().URL.Path),
            logger.String("ip", ctx.RealIP()))
        return c.spectrogramHTTPError(ctx, err)
    }
    // ... success path
}
```

### Proposed Fix

```go
func (c *Controller) handleAutoPreRenderMode(ctx echo.Context, noteID, clipPath string, params spectrogramParameters) error {
    // Auto or prerender mode - generate on-demand if needed
    generationStart := time.Now()
    spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), clipPath, params.width, params.raw)
    generationDuration := time.Since(generationStart)

    if err != nil {
        // Check if this is an operational error (context canceled, timeout, etc.)
        if spectrogram.IsOperationalError(err) {
            // Log at Debug level for expected operational events
            c.logDebugIfEnabled("Spectrogram generation canceled or interrupted",
                logger.String("note_id", noteID),
                logger.String("clip_path", clipPath),
                logger.Error(err),
                logger.Int64("duration_ms", generationDuration.Milliseconds()),
                logger.String("path", ctx.Request().URL.Path),
                logger.String("ip", ctx.RealIP()))
        } else {
            // Log at Error level for unexpected failures
            c.logErrorIfEnabled("Spectrogram generation failed",
                logger.String("note_id", noteID),
                logger.String("clip_path", clipPath),
                logger.Error(err),
                logger.Int64("duration_ms", generationDuration.Milliseconds()),
                logger.String("path", ctx.Request().URL.Path),
                logger.String("ip", ctx.RealIP()))
        }
        return c.spectrogramHTTPError(ctx, err)
    }
    // ... success path
}
```

### Implementation Steps

1. Import `spectrogram` package in `internal/api/v2/media.go` (already imported)
2. Add `IsOperationalError()` check before logging
3. Use `logDebugIfEnabled` for operational errors
4. Use `logErrorIfEnabled` for genuine failures
5. Preserve all existing log fields in both paths

### Testing

- **Manual test**: Cancel a spectrogram generation request (Ctrl+C curl, close browser tab)
- **Verify**: Check logs show Debug level, not Error level
- **Manual test**: Trigger genuine error (invalid audio file, missing sox binary)
- **Verify**: Check logs show Error level
- **Unit test**: Add test case in `media_test.go` (if test file exists) to verify logging behavior

### Risk Assessment

- **Risk**: Low - Only changes log level, no functional behavior change
- **Rollback**: Simple - revert to unconditional `logErrorIfEnabled`

---

## Issue #2: Stats Pollution on Shutdown (MEDIUM PRIORITY - RECOMMENDED FIX)

### Problem

The pre-renderer increments `pr.stats.Failed` for ALL errors, including operational interruptions during shutdown. This pollutes metrics and makes it harder to identify genuine failures.

**Location**: `internal/spectrogram/prerenderer.go:468-470`

### Current Code

```go
func (pr *PreRenderer) processJob(job *Job, workerID int) {
    // ... generation logic ...

    if err := pr.generator.GenerateFromPCM(ctx, job.PCMData, spectrogramPath, width, pr.settings.Realtime.Dashboard.Spectrogram.Raw); err != nil {
        // Check if this is an expected operational error
        if IsOperationalError(err) {
            // Log at Debug level for expected operational events
            pr.logger.Debug("Spectrogram generation canceled or interrupted", ...)
        } else {
            // Log at Error level for unexpected failures
            pr.logger.Error("Failed to generate spectrogram", ...)
        }
        pr.mu.Lock()
        pr.stats.Failed++  // ← Increments for BOTH operational and genuine errors
        pr.mu.Unlock()
        return
    }
    // ... success path
}
```

### Proposed Fix (Option A - Recommended)

Don't count operational errors as failures:

```go
func (pr *PreRenderer) processJob(job *Job, workerID int) {
    // ... generation logic ...

    if err := pr.generator.GenerateFromPCM(ctx, job.PCMData, spectrogramPath, width, pr.settings.Realtime.Dashboard.Spectrogram.Raw); err != nil {
        // Check if this is an expected operational error
        if IsOperationalError(err) {
            // Log at Debug level for expected operational events
            pr.logger.Debug("Spectrogram generation canceled or interrupted", ...)
            // Don't increment Failed counter for operational events
        } else {
            // Log at Error level for unexpected failures
            pr.logger.Error("Failed to generate spectrogram", ...)
            pr.mu.Lock()
            pr.stats.Failed++
            pr.mu.Unlock()
        }
        return
    }
    // ... success path
}
```

### Proposed Fix (Option B - Alternative)

Track operational interruptions separately:

```go
// Add to Stats struct in prerenderer.go
type Stats struct {
    Queued      int64 // Number of jobs submitted
    Completed   int64 // Number of spectrograms successfully generated
    Failed      int64 // Number of failed generations (genuine errors)
    Skipped     int64 // Number skipped (already exist)
    Interrupted int64 // Number canceled/interrupted (operational events)  ← NEW
}

// In processJob
if IsOperationalError(err) {
    pr.logger.Debug("Spectrogram generation canceled or interrupted", ...)
    pr.mu.Lock()
    pr.stats.Interrupted++
    pr.mu.Unlock()
} else {
    pr.logger.Error("Failed to generate spectrogram", ...)
    pr.mu.Lock()
    pr.stats.Failed++
    pr.mu.Unlock()
}

// Update Stop() method to log new counter
pr.logger.Info("Spectrogram pre-renderer final stats",
    logger.Int64("queued", stats.Queued),
    logger.Int64("completed", stats.Completed),
    logger.Int64("failed", stats.Failed),
    logger.Int64("skipped", stats.Skipped),
    logger.Int64("interrupted", stats.Interrupted))  // ← NEW
```

### Recommendation

**Use Option A** for simplicity. Operational interruptions are expected events, not failures. Option B provides more granular metrics but adds complexity.

### Implementation Steps (Option A)

1. Move `pr.stats.Failed++` inside the `else` block (genuine errors only)
2. Remove stats increment from operational error path
3. No struct changes needed

### Testing

- **Test**: Start pre-renderer, queue jobs, stop service before completion
- **Verify**: `Failed` counter should NOT increase for interrupted jobs
- **Test**: Queue job with invalid audio file
- **Verify**: `Failed` counter SHOULD increase
- **Unit test**: Mock context cancellation in `processJob` test, verify stats

### Risk Assessment

- **Risk**: Low - Only affects metrics collection, no functional change
- **Rollback**: Simple - move stats increment outside if/else block

---

## Issue #3: Brittle Error String Matching (MEDIUM PRIORITY - RECOMMENDED FIX)

### Problem

`IsOperationalError` uses string matching on "signal: killed" which is fragile and OS/version-dependent.

**Location**: `internal/spectrogram/utils.go:112-119`

### Current Code

```go
func IsOperationalError(err error) bool {
    if err == nil {
        return false
    }
    return errors.Is(err, context.Canceled) ||
        errors.Is(err, context.DeadlineExceeded) ||
        strings.Contains(err.Error(), "signal: killed")  // ← Brittle
}
```

### Proposed Fix

Add exit code checking and extract string constant:

**Note**: Requires adding `"os/exec"` to imports in `utils.go`

```go
import (
    "context"
    "fmt"
    "maps"
    "os/exec"  // ← ADD THIS
    "path/filepath"
    "slices"
    "strings"

    "github.com/tphakala/birdnet-go/internal/errors"
)

const (
    // Process termination signals
    signalKilledMessage = "signal: killed"
    exitCodeSIGKILL     = 137 // 128 + 9 (SIGKILL)
    exitCodeSIGTERM     = 143 // 128 + 15 (SIGTERM)
)

// IsOperationalError checks if an error is an expected operational event rather than
// a genuine failure. Operational errors include context cancellation, deadline exceeded,
// and process kills (e.g. context-triggered SIGKILL, OOM killer).
func IsOperationalError(err error) bool {
    if err == nil {
        return false
    }

    // Check for explicit context errors
    if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
        return true
    }

    // Check for process termination signals via exit code (more reliable)
    var exitErr *exec.ExitError
    if errors.As(err, &exitErr) {
        exitCode := exitErr.ExitCode()
        if exitCode == exitCodeSIGKILL || exitCode == exitCodeSIGTERM {
            return true
        }
    }

    // Fallback to string matching (for wrapped errors or non-exec errors)
    return strings.Contains(err.Error(), signalKilledMessage)
}
```

### Implementation Steps

1. Add constants at package level in `utils.go`
2. Add exit code checking using `errors.As` with `*exec.ExitError`
3. Keep string matching as fallback for wrapped errors
4. Update function comment to document behavior

### Testing

- **Unit test**: Add test case for exit code 137 (SIGKILL)
- **Unit test**: Add test case for exit code 143 (SIGTERM)
- **Unit test**: Add test case for wrapped exec.ExitError
- **Unit test**: Verify string matching fallback still works
- **Existing tests**: Verify existing tests in `utils_test.go:311-373` still pass

### Test Code Addition

```go
// Add to utils_test.go
func TestIsOperationalError_ExitCodes(t *testing.T) {
    tests := []struct {
        name     string
        exitCode int
        want     bool
    }{
        {
            name:     "exit code 137 (SIGKILL)",
            exitCode: 137,
            want:     true,
        },
        {
            name:     "exit code 143 (SIGTERM)",
            exitCode: 143,
            want:     true,
        },
        {
            name:     "exit code 1 (generic error)",
            exitCode: 1,
            want:     false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Create exec.ExitError with specific exit code
            cmd := exec.Command("false")
            cmd.Process = &os.Process{}
            err := &exec.ExitError{
                ProcessState: &os.ProcessState{},
            }
            // Set exit code (requires reflection or process state manipulation)
            // ... implementation details ...

            got := IsOperationalError(err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### Risk Assessment

- **Risk**: Low - Adds additional checks, preserves existing string matching
- **Rollback**: Simple - revert to string matching only
- **Compatibility**: Should work across Go versions and OS platforms

---

## Issue #4: Queue Position Race Condition (LOW PRIORITY - OPTIONAL)

### Problem

Queue position calculation in `initializeQueueStatus` happens outside the lock. Two concurrent requests might see the same queue position (cosmetic only).

**Location**: `internal/api/v2/media.go:1561-1596`

### Current Code

```go
func (c *Controller) initializeQueueStatus(spectrogramKey string) {
    // Step 1: Calculate queue position OUTSIDE the lock to minimize contention
    currentSlotsInUse := len(spectrogramSemaphore)

    var queuePosition int
    if currentSlotsInUse >= maxConcurrentSpectrograms {
        waitingCount := 0
        spectrogramQueue.Range(func(key, value any) bool {
            if status, ok := value.(*SpectrogramQueueStatus); ok {
                if status.GetStatus() == spectrogramStatusQueued {
                    waitingCount++
                }
            }
            return true
        })
        queuePosition = waitingCount + 1
    } else {
        queuePosition = 0
    }

    // Step 2: Create and store status in sync.Map (lock-free operation)
    status := &SpectrogramQueueStatus{}
    status.Update(spectrogramStatusQueued, queuePosition, "Waiting for generation slot")
    spectrogramQueue.Store(spectrogramKey, status)
}
```

### Assessment

**No fix recommended.** The code comment explains the optimization ("minimize contention"). The race is intentional and benign:

- Does not affect actual queue behavior (managed by `spectrogramSemaphore`)
- Does not affect generation correctness
- Only affects displayed queue position number (UI cosmetic)
- Trade-off: lock-free performance vs. strict position consistency

If strict consistency is required, position calculation could be moved inside status creation, but this adds lock contention for minimal benefit.

---

## Implementation Order

### Phase 1: Critical Fix (Merge Blocker)

1. **Issue #1**: API layer logging inconsistency
   - Implementation time: 10 minutes
   - Testing time: 15 minutes
   - **Must be fixed before merge**

### Phase 2: Recommended Fixes (Same PR or Follow-up)

2. **Issue #2**: Stats pollution on shutdown (Option A)
   - Implementation time: 5 minutes
   - Testing time: 10 minutes
   - Can be in same PR or follow-up

3. **Issue #3**: Brittle error string matching
   - Implementation time: 15 minutes
   - Testing time: 20 minutes (need exit code tests)
   - Can be in same PR or follow-up

### Phase 3: Optional

4. **Issue #4**: Queue position race
   - No fix planned - accepted trade-off

---

## Testing Strategy

### Automated Tests

1. **Add test**: `TestHandleAutoPreRenderMode_OperationalError` (Issue #1)
2. **Add test**: `TestPreRenderer_Stats_OperationalVsGenuine` (Issue #2)
3. **Add test**: `TestIsOperationalError_ExitCodes` (Issue #3)
4. **Run existing tests**: Ensure no regressions in `generator_test.go`, `utils_test.go`, `prerenderer_test.go`

### Manual Tests

1. **Cancel spectrogram generation** (browser/curl)
   - Verify Debug log level (not Error)
   - Verify no Failed stat increment (if Issue #2 fixed)
2. **Trigger genuine error** (invalid file, missing binary)
   - Verify Error log level
   - Verify Failed stat increment
3. **Service shutdown during generation**
   - Verify Debug log for interrupted jobs
   - Verify no Failed stat increment (if Issue #2 fixed)

### Regression Tests

- Run full test suite: `go test -race -v ./internal/...`
- Check for data races: `go test -race ./internal/spectrogram/...`
- Check for memory leaks: Monitor stats during extended operation

---

## Success Criteria

### Issue #1 (Critical)

- [ ] Operational errors (context.Canceled, context.DeadlineExceeded) logged at Debug level in API layer
- [ ] Genuine errors still logged at Error level in API layer
- [ ] Manual test: Canceled request shows Debug log
- [ ] Manual test: Invalid file shows Error log

### Issue #2 (Recommended)

- [ ] Operational errors do not increment `stats.Failed`
- [ ] Genuine errors still increment `stats.Failed`
- [ ] Service shutdown does not cause failure spike
- [ ] Manual test: Interrupt pre-render job, verify stats

### Issue #3 (Recommended)

- [ ] Exit code 137 (SIGKILL) detected as operational
- [ ] Exit code 143 (SIGTERM) detected as operational
- [ ] String matching fallback still works
- [ ] All existing tests pass

---

## Rollback Plan

If issues arise after deployment:

### Issue #1 Rollback

```go
// Revert to unconditional error logging
if err != nil {
    c.logErrorIfEnabled("Spectrogram generation failed", ...)
    return c.spectrogramHTTPError(ctx, err)
}
```

### Issue #2 Rollback

```go
// Move stats increment outside if/else
if err != nil {
    if IsOperationalError(err) { ... } else { ... }
    pr.mu.Lock()
    pr.stats.Failed++  // ← Back to unconditional increment
    pr.mu.Unlock()
    return
}
```

### Issue #3 Rollback

```go
// Revert to simple string matching
func IsOperationalError(err error) bool {
    if err == nil {
        return false
    }
    return errors.Is(err, context.Canceled) ||
        errors.Is(err, context.DeadlineExceeded) ||
        strings.Contains(err.Error(), "signal: killed")
}
```

---

## Notes

- All fixes maintain backward compatibility
- No API contract changes
- No database schema changes
- No configuration changes required
- Fixes are additive (don't remove functionality)
