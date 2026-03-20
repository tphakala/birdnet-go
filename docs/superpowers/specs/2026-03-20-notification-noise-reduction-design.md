# Notification Noise Reduction Design

Reduce notification noise from two sources: repeated disk space alerts that fire every 30 minutes while above threshold, and securefs file-not-found errors caused by a known race condition between detection DB commits and audio file encoding.

## Problem Statement

### Disk Alert Spam

The "Low disk space" alert rule has a 30-minute cooldown. Once disk usage exceeds 85%, a new notification fires every 30 minutes indefinitely as long as the condition holds. There is no concept of "I already told you about this" — only time-based cooldown.

Users want to be alerted when disk usage gets worse (crosses a new threshold), not reminded of the same condition repeatedly.

### securefs File-Not-Found Notifications

When BirdNET-Go detects a bird, the detection record is committed to the database before the audio clip finishes encoding (FFmpeg). The frontend receives the detection via SSE/polling and immediately requests the audio file. This request hits `securefs.ServeRelativeFile`, which fails because the file doesn't exist yet.

The error wrapping in `securefs.go:672` uses `.Build()`, which triggers the telemetry/error hook system, creating a bell notification for each occurrence. The API layer already handles this gracefully with `handleAudio404WithWait` — it retries and successfully serves the file ~400-500ms later.

Evidence from production logs (2026-03-20):
- `17:28:52.008` securefs ERROR: file not found
- `17:28:52.432` actions.log: same file saved successfully (424ms later)
- `17:28:52.536` access.log: "Successfully served audio clip after waiting for encoding"

The notification is pure noise from an expected, handled code path.

## Design

### 1. Stepped Metric Escalation

Add escalation steps to metric-based alert rules. When a rule fires at a threshold, record the "acknowledged step." Only re-fire when the metric crosses the next higher step. Reset when the metric drops below the base threshold.

#### Data Model

Add an `EscalationSteps` field to `AlertRule`:

```go
// entities.AlertRule
EscalationSteps []float64 `gorm:"serializer:json"` // e.g., [85, 90, 95, 99]
```

When empty/nil, the rule behaves as today (cooldown-only). When present, the engine uses stepped escalation logic.

#### Engine State

New in-memory state in `Engine` (alongside existing `cooldowns` map):

```go
escalations   map[string]float64   // escalation key -> last fired step value
escalationsMu sync.RWMutex
```

The escalation key incorporates both the rule ID and the metric instance identifier (e.g., disk mount point path) using the same logic as `metricBufferKey`. This ensures that multiple disk mount points are tracked independently — `/` hitting 95% does not suppress alerts for `/mnt/data` crossing 85%.

Key format: `fmt.Sprintf("%d|%s", ruleID, metricBufferKey(metricName, properties))` (e.g., `"7|system.disk_usage|/"`)

Resets on server restart, same as cooldowns.

#### Escalation Logic

The escalation check runs in two phases within `HandleEvent`:

**Phase 1 — State clearing (runs for ALL metric events, even when ruleMatches returns false):**

For each rule that has `EscalationSteps` and a `TriggerType` of `TriggerTypeMetric`:
1. If the rule's `ObjectType` and `MetricName` match the event, extract the current metric value.
2. If the current value is below `EscalationSteps[0]` (the base threshold), clear the escalation state for this rule+instance key. This means the next breach starts fresh.

This phase must run before or independently of `ruleMatches`, because when the metric drops below the base condition threshold, `ruleMatches` returns false and the engine would skip the rule entirely — leaving stale escalation state that suppresses future alerts.

**Phase 2 — Stepped fire decision (runs when ruleMatches returns true):**

1. If the rule has no `EscalationSteps`, proceed as today (cooldown-only).
2. Get the current metric value from the event properties.
3. Find the highest escalation step that the current value exceeds.
4. If `escalations[key]` already equals or exceeds this step, suppress the rule (don't fire).
5. Otherwise, fire the rule and record the new step in `escalations[key]`.

The existing cooldown check still applies — it prevents rapid-fire within the same step if the metric fluctuates around a boundary.

#### Default Rule Update

```go
// Low disk space rule in defaults.go
EscalationSteps: []float64{85, 90, 95, 99},
```

CPU and memory rules could also get escalation steps, but that's a separate decision. This spec only changes the disk rule.

#### Notification Message

The metric message in `dispatcher.go:metricMessage()` currently shows "threshold: 85%". With escalation, the threshold parameter should reflect the step that was crossed, so the user sees why they're getting a new alert (e.g., "Current value: 91% (threshold: 90%)").

This requires passing the fired step value to the dispatcher. The event properties already contain `PropertyValue`; we add a new property `PropertyThresholdStep` set by the engine when it fires a stepped rule.

**Important:** The engine must not mutate the shared `event.Properties` map, since multiple rules may match the same event concurrently. When firing a stepped rule, create a shallow copy of the properties map and add `PropertyThresholdStep` to the copy before passing it to the action function.

#### Migration

Existing alert rules in the database don't have `EscalationSteps`. GORM's `serializer:json` handles nil/empty gracefully — nil means no escalation (legacy behavior).

A lightweight data migration targets existing built-in rules: for any `AlertRule` row where `BuiltIn == true` and `NameKey == RuleKeyLowDiskName` and `EscalationSteps` is nil/empty, populate it with `[85, 90, 95, 99]`. This ensures existing installations get the noise reduction benefit without requiring users to manually reset their alert defaults.

The migration runs during the normal startup rule-seeding path (where defaults are applied), keeping the migration logic co-located with the defaults rather than requiring a separate database migration file.

### 2. Suppress securefs EnhancedError for Expected File-Not-Found

#### Change Locations

Both `ServeFile` (line 647-649) and `ServeRelativeFile` (line 670-672) in `internal/securefs/securefs.go`. Both methods use identical `.Build()` wrapping for file-open errors, so the same fix applies to both for consistency.

#### Current Code

```go
file, err := sfs.root.Open(validatedRelPath)
if err != nil {
    return nil, validatedRelPath, errors.New(err).
        Component(componentSecurefs).
        Category(errors.CategoryFileIO).
        Context("operation", "serve_relative_file_open").
        Build()
}
```

#### New Code

```go
file, err := sfs.root.Open(validatedRelPath)
if err != nil {
    // File-not-found is expected during the race window between detection
    // DB commit and audio export completion. The API layer handles this
    // gracefully (handleAudio404WithWait). Use plain error wrapping to
    // avoid triggering telemetry hooks and creating noise notifications.
    if stdErrors.Is(err, fs.ErrNotExist) {
        return nil, validatedRelPath, fmt.Errorf("openat %s: %w", validatedRelPath, err)
    }
    return nil, validatedRelPath, errors.New(err).
        Component(componentSecurefs).
        Category(errors.CategoryFileIO).
        Context("operation", "serve_relative_file_open").
        Build()
}
```

Note: `stdErrors` refers to the standard library `errors` package. Since the `internal/errors` package shadows the standard one, use an alias import (the securefs package may already have one, or add `stdErrors "errors"`).

#### Impact

- `mapOpenErrorToHTTP` still sees `fs.ErrNotExist` via `errors.Is()` and returns HTTP 404.
- `handleAudio404WithWait` still triggers and serves the file after encoding completes.
- No EnhancedError is created, so no telemetry hook fires, no notification created.
- Other securefs errors (permission denied, path traversal, I/O errors) still go through `.Build()` and create notifications.
- The `serveInternal` method's `GetLogger().Error()` call at line 600 still logs the error for debugging.

### 3. Error Burst Grouping in the Notification Error Hook

Add a sliding-window deduplication mechanism that collapses repeated errors from the same component+category into a single summary notification.

#### ErrorBurstTracker

New struct in `internal/notification/`:

```go
type ErrorBurstTracker struct {
    mu      sync.Mutex
    buckets map[string]*burstBucket
}

type burstBucket struct {
    count     int
    firstSeen time.Time
    lastSeen  time.Time
    sample    string   // first error message, for display in summary
    notified  bool     // whether summary notification was already sent this window
}
```

Key format: `"component:category"` (e.g., `"securefs:file-io"`).

#### Configuration

Hardcoded defaults:
- **Burst threshold:** 3 errors within the window before grouping kicks in
- **Window duration:** 5 minutes

These can be promoted to `ServiceConfig` fields later if needed.

#### Flow in errorNotificationHook

1. Compute key from `enhancedErr.GetComponent() + ":" + enhancedErr.GetCategory()`.
2. Record the error in the burst tracker (increment count, update timestamps).
3. If `count == 1` (first in window): let the notification through normally. This ensures users always see the first occurrence.
4. If `count <= threshold` (e.g., 2-3): let individual notifications through. Low-frequency errors aren't grouped.
5. If `count == threshold + 1`: create a single summary notification with i18n keys. Set `notified = true`.
6. If `count > threshold + 1` and `notified == true`: suppress (don't create notification). The summary already told the user about the burst.

#### Summary Notification

- Type: `TypeError` (same as individual error notifications)
- Priority: same as the individual error's priority
- Title key: `"notifications.errorBurst.title"`
- Title params: `{component: "securefs", count: 7}`
- Message key: `"notifications.errorBurst.message"`
- Message params: `{component: "securefs", category: "file-io", count: 7, window_minutes: 5, sample_error: "openat 2026/03/...flac: no such file or directory"}`
- English fallback: "Multiple securefs errors (7 in the last 5 minutes): openat ...flac: no such file or directory"

#### Cleanup

The burst tracker needs periodic cleanup of expired buckets. Two options:

**Lazy cleanup (preferred):** When recording a new error, check if the bucket's `firstSeen` is older than the window duration. If so, reset the bucket (clear count, update firstSeen to now, clear notified flag). This implements a true tumbling window — errors are grouped within a fixed window from the first occurrence, not from the most recent one.

Using `firstSeen` (not `lastSeen`) prevents a trickle of errors from indefinitely extending the window and eventually producing an inaccurate summary like "7 errors in the last 5 minutes" when the errors actually spanned 20 minutes.

The number of distinct component:category pairs is small (bounded by the number of components), so stale buckets aren't a memory concern.

#### Interaction with Section 2

With section 2 in place, securefs file-not-found errors won't reach the error hook at all. The burst tracker is a general-purpose safety net for any component that might produce bursts of errors. It protects against scenarios we haven't anticipated yet.

## Files Changed

| File | Change |
|------|--------|
| `internal/datastore/v2/entities/alert_rule.go` | Add `EscalationSteps` field |
| `internal/alerting/engine.go` | Add escalation state, stepped evaluation logic |
| `internal/alerting/defaults.go` | Add `EscalationSteps` to "Low disk space" rule |
| `internal/alerting/dispatcher.go` | Use step threshold in metric message params |
| `internal/alerting/constants.go` | Add `PropertyThresholdStep` constant |
| `internal/securefs/securefs.go` | Skip `.Build()` for `fs.ErrNotExist` in `ServeFile` and `ServeRelativeFile` |
| `internal/notification/error_integration.go` | Integrate `ErrorBurstTracker` |
| `internal/notification/burst_tracker.go` | New file: `ErrorBurstTracker` implementation |
| `internal/notification/i18n_keys.go` (or equivalent) | Add burst notification i18n keys |
| Frontend i18n files (10 locales) | Add translation strings for burst notifications |

## Testing

- **Stepped escalation:** Unit test that fires metric events at increasing values and verifies fire/suppress behavior at each step. Test reset when value drops below base threshold.
- **securefs 404 suppression:** Verify that `ServeRelativeFile` returns a plain error (not EnhancedError) for missing files, and an EnhancedError for other failures.
- **Burst tracker:** Unit test with rapid error injection, verify first N pass through, summary fires at threshold+1, subsequent errors suppressed, window expiry resets state.

## Out of Scope

- UI for configuring escalation steps (use defaults or API for now)
- Configurable burst tracker thresholds (hardcoded is fine initially)
- Applying escalation steps to CPU/memory rules (separate decision)
- Fixing the underlying race condition between detection commit and audio export (existing wait mechanism works well)
