# Phase 3f: Pipeline Integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire multi-model inference end-to-end: Orchestrator dispatch with per-model locking, ProcessData with modelID routing, name resolution for Perch results, and ModelID in detection results.

**Architecture:** The Orchestrator gains a `sync.RWMutex` protecting the models map and a `PredictModel(ctx, modelID, samples)` method that fetches the model entry under RLock then acquires per-model lock for inference. ProcessData gets a modelID parameter and per-model overrun tracking. Results carry ModelID for downstream storage. Thread counts are divided among models.

**Tech Stack:** Go 1.26, testify, `internal/classifier`, `internal/analysis`

**Spec:** `docs/superpowers/specs/2026-03-29-phase-3ef-buffer-pipeline-design.md` — Phase 3f section

---

## File Structure

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/classifier/orchestrator.go` | Add RWMutex, PredictModel, ResolveName, update ReloadModel |
| Create | `internal/classifier/orchestrator_test.go` | Tests for PredictModel, locking, ResolveName |
| Create | `internal/classifier/threads.go` | divideThreads function |
| Create | `internal/classifier/threads_test.go` | Thread division tests |
| Modify | `internal/classifier/queue.go` | Add ModelID field to Results struct |
| Modify | `internal/analysis/process.go` | Add modelID param, per-model overrun tracking |
| Modify | `internal/analysis/process_test.go` | Tests for updated ProcessData |
| Modify | `internal/analysis/buffer_manager.go` | Pass modelID to ProcessData |

---

## Task 1: Orchestrator Two-Level Locking and PredictModel

**Files:**
- Modify: `internal/classifier/orchestrator.go:16-141`
- Create: `internal/classifier/orchestrator_test.go`

- [ ] **Step 1: Write failing tests for PredictModel**

```go
// internal/classifier/orchestrator_test.go

package classifier

import (
    "context"
    "sync"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/tphakala/birdnet-go/internal/datastore"
)

// mockModelInstance implements ModelInstance for testing.
type mockModelInstance struct {
    id      string
    spec    ModelSpec
    predict func(ctx context.Context, samples [][]float32) ([]datastore.Results, error)
}

func (m *mockModelInstance) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
    if m.predict != nil {
        return m.predict(ctx, samples)
    }
    return []datastore.Results{{Species: "TestSpecies", Confidence: 0.95}}, nil
}
func (m *mockModelInstance) Spec() ModelSpec          { return m.spec }
func (m *mockModelInstance) ModelID() string           { return m.id }
func (m *mockModelInstance) ModelName() string         { return "TestModel" }
func (m *mockModelInstance) ModelVersion() string      { return "1.0" }
func (m *mockModelInstance) NumSpecies() int           { return 1 }
func (m *mockModelInstance) Labels() []string          { return []string{"TestSpecies_Test Common"} }
func (m *mockModelInstance) Close() error              { return nil }

func TestOrchestrator_PredictModel_Success(t *testing.T) {
    t.Parallel()
    o := &Orchestrator{
        models: map[string]*modelEntry{
            "test-model": {instance: &mockModelInstance{id: "test-model"}},
        },
    }

    results, err := o.PredictModel(context.Background(), "test-model", [][]float32{{0.1, 0.2}})
    require.NoError(t, err)
    require.Len(t, results, 1)
    assert.Equal(t, "TestSpecies", results[0].Species)
}

func TestOrchestrator_PredictModel_UnknownModel(t *testing.T) {
    t.Parallel()
    o := &Orchestrator{
        models: map[string]*modelEntry{},
    }

    _, err := o.PredictModel(context.Background(), "nonexistent", nil)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "unknown model")
}

func TestOrchestrator_PredictModel_NoCrossModelBlocking(t *testing.T) {
    t.Parallel()

    // Model A blocks for 100ms, model B returns instantly.
    modelA := &mockModelInstance{
        id: "model-a",
        predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
            time.Sleep(100 * time.Millisecond)
            return []datastore.Results{{Species: "A"}}, nil
        },
    }
    modelB := &mockModelInstance{
        id: "model-b",
        predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
            return []datastore.Results{{Species: "B"}}, nil
        },
    }

    o := &Orchestrator{
        models: map[string]*modelEntry{
            "model-a": {instance: modelA},
            "model-b": {instance: modelB},
        },
    }

    var wg sync.WaitGroup
    var bDone time.Time

    wg.Add(2)
    go func() {
        defer wg.Done()
        _, _ = o.PredictModel(context.Background(), "model-a", nil)
    }()
    go func() {
        defer wg.Done()
        time.Sleep(10 * time.Millisecond) // Let A acquire its lock first
        _, _ = o.PredictModel(context.Background(), "model-b", nil)
        bDone = time.Now()
    }()

    start := time.Now()
    wg.Wait()

    // B should finish well before A's 100ms sleep
    assert.Less(t, bDone.Sub(start).Milliseconds(), int64(80),
        "model B should not be blocked by model A's inference")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/classifier && go test -run TestOrchestrator_PredictModel -v`
Expected: FAIL — `PredictModel` not defined

- [ ] **Step 3: Implement PredictModel with two-level locking**

In `internal/classifier/orchestrator.go`:

1. Add `mu sync.RWMutex` field to `Orchestrator` struct.

2. Add `PredictModel` method:
```go
// PredictModel runs inference on a specific model with per-model locking.
// Uses two-level locking: RLock on Orchestrator to fetch model entry,
// then per-model Lock for inference. Never holds both simultaneously.
func (o *Orchestrator) PredictModel(ctx context.Context, modelID string, sample [][]float32) ([]datastore.Results, error) {
    o.mu.RLock()
    entry, ok := o.models[modelID]
    o.mu.RUnlock()

    if !ok {
        return nil, errors.Newf("unknown model: %s", modelID).
            Component("classifier.orchestrator").
            Category(errors.CategoryValidation).
            Context("model_id", modelID).
            Build()
    }

    entry.mu.Lock()
    defer entry.mu.Unlock()
    return entry.instance.Predict(ctx, sample)
}
```

3. Update `ReloadModel` to use write lock:
```go
func (o *Orchestrator) ReloadModel() error {
    o.mu.Lock()
    defer o.mu.Unlock()
    // ... existing reload logic, now re-keys models map
}
```

4. Update `Delete` to use write lock.

- [ ] **Step 4: Run tests**

Run: `cd internal/classifier && go test -run TestOrchestrator_PredictModel -v -race`
Expected: PASS

- [ ] **Step 5: Run linter**

Run: `golangci-lint run -v ./internal/classifier/...`

- [ ] **Step 6: Commit**

```bash
git commit -m "feat(classifier): add PredictModel with two-level locking protocol"
```

---

## Task 2: Orchestrator Name Resolution

**Files:**
- Modify: `internal/classifier/orchestrator.go`
- Modify: `internal/classifier/orchestrator_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestOrchestrator_ResolveName_Found(t *testing.T) {
    t.Parallel()
    resolver := NewBirdNETLabelResolver([]string{"Turdus merula_Eurasian Blackbird"})
    o := &Orchestrator{
        nameResolvers: []NameResolver{resolver},
    }

    result := o.ResolveName("Turdus merula", "en")
    assert.Equal(t, "Eurasian Blackbird", result)
}

func TestOrchestrator_ResolveName_NotFound(t *testing.T) {
    t.Parallel()
    resolver := NewBirdNETLabelResolver([]string{"Turdus merula_Eurasian Blackbird"})
    o := &Orchestrator{
        nameResolvers: []NameResolver{resolver},
    }

    result := o.ResolveName("Unknown species", "en")
    assert.Equal(t, "", result)
}

func TestOrchestrator_ResolveName_EmptyChain(t *testing.T) {
    t.Parallel()
    o := &Orchestrator{}

    result := o.ResolveName("Turdus merula", "en")
    assert.Equal(t, "", result)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/classifier && go test -run TestOrchestrator_ResolveName -v`
Expected: FAIL — `ResolveName` not defined, `nameResolvers` field missing

- [ ] **Step 3: Implement ResolveName**

In `internal/classifier/orchestrator.go`:

1. Add `nameResolvers []NameResolver` field to Orchestrator struct.

2. Add `ResolveName` method:
```go
// ResolveName walks the resolver chain and returns the first non-empty
// common name for the given scientific name and locale.
func (o *Orchestrator) ResolveName(scientificName, locale string) string {
    for _, r := range o.nameResolvers {
        if name := r.Resolve(scientificName, locale); name != "" {
            return name
        }
    }
    return ""
}
```

3. In `NewOrchestrator`, build the resolver chain from the primary model's labels:
```go
resolver := NewBirdNETLabelResolver(bn.Labels())
o.nameResolvers = []NameResolver{resolver}
```

- [ ] **Step 4: Run tests**

Run: `cd internal/classifier && go test -run TestOrchestrator_ResolveName -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(classifier): add ResolveName with resolver chain"
```

---

## Task 3: Thread Count Division

**Files:**
- Create: `internal/classifier/threads.go`
- Create: `internal/classifier/threads_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/classifier/threads_test.go

package classifier

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestDivideThreads_EqualSplit(t *testing.T) {
    t.Parallel()
    result := divideThreads(8, []string{"model-a", "model-b"}, "model-a")
    assert.Equal(t, 4, result["model-a"])
    assert.Equal(t, 4, result["model-b"])
}

func TestDivideThreads_RemainderToPrimary(t *testing.T) {
    t.Parallel()
    result := divideThreads(7, []string{"model-a", "model-b"}, "model-a")
    assert.Equal(t, 4, result["model-a"]) // 3 + 1 remainder
    assert.Equal(t, 3, result["model-b"])
}

func TestDivideThreads_MinimumOnePerModel(t *testing.T) {
    t.Parallel()
    result := divideThreads(2, []string{"a", "b", "c"}, "a")
    // 3 models but only 2 threads: each gets min 1, primary gets remainder
    assert.Equal(t, 1, result["a"])
    assert.Equal(t, 1, result["b"])
    assert.Equal(t, 1, result["c"])
}

func TestDivideThreads_SingleModel(t *testing.T) {
    t.Parallel()
    result := divideThreads(4, []string{"only"}, "only")
    assert.Equal(t, 4, result["only"])
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/classifier && go test -run TestDivideThreads -v`
Expected: FAIL — function not defined

- [ ] **Step 3: Implement divideThreads**

```go
// internal/classifier/threads.go

package classifier

// divideThreads distributes a total thread count among models.
// Each model gets at least 1 thread. The remainder goes to the primary model.
func divideThreads(total int, modelIDs []string, primaryID string) map[string]int {
    n := len(modelIDs)
    if n == 0 {
        return nil
    }

    // Ensure minimum 1 per model, cap at total.
    if total < n {
        total = n
    }

    perModel := total / n
    remainder := total % n

    result := make(map[string]int, n)
    for _, id := range modelIDs {
        result[id] = perModel
    }
    result[primaryID] += remainder

    return result
}
```

- [ ] **Step 4: Run tests**

Run: `cd internal/classifier && go test -run TestDivideThreads -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(classifier): add divideThreads for multi-model thread allocation"
```

---

## Task 4: ModelID in Results

**Files:**
- Modify: `internal/classifier/queue.go:10-18`

- [ ] **Step 1: Add ModelID field to Results struct**

In `internal/classifier/queue.go`, add `ModelID string` to the `Results` struct:

```go
type Results struct {
    StartTime       time.Time
    AudioCapturedAt time.Time
    PCMdata         []byte
    Results         []datastore.Results
    ElapsedTime     time.Duration
    ClipName        string
    Source          datastore.AudioSource
    ModelID         string // identifies which model produced these results
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/classifier/...`
Expected: SUCCESS — new field has zero value, no callers break

- [ ] **Step 3: Search for all ResultsQueue producers to verify none break**

Run: `grep -rn "classifier.Results{" --include="*.go" | grep -v _test.go`

Check each site. The new field is optional (zero value = empty string), so existing code compiles without changes. ProcessData will set it in Task 5.

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(classifier): add ModelID field to Results for multi-model attribution"
```

---

## Task 5: ProcessData with ModelID and Per-Model Overrun Tracking

**Files:**
- Modify: `internal/analysis/process.go:28-337`
- Modify: `internal/analysis/buffer_manager.go` (caller update)

- [ ] **Step 1: Update ProcessData signature**

Change signature from:
```go
func ProcessData(bn *classifier.Orchestrator, data []byte, startTime, audioCapturedAt time.Time, source string) error
```
to:
```go
func ProcessData(bn *classifier.Orchestrator, data []byte, startTime, audioCapturedAt time.Time, source, modelID string) error
```

- [ ] **Step 2: Update the caller in buffer_manager.go**

In `internal/analysis/buffer_manager.go`, the `analysisBufferMonitor` calls ProcessData. Update the call to pass `cfg.modelID`:

```go
// Was: ProcessData(m.bn, data, startTime, audioCapturedAt, cfg.sourceID)
// Now:
if processErr := ProcessData(m.bn, data, startTime, audioCapturedAt, cfg.sourceID, cfg.modelID); processErr != nil {
```

- [ ] **Step 3: Refactor overrun tracker to per-model**

In `process.go`, replace the global `overrunTracker` with a map:

```go
var (
    overrunTrackers   map[string]*bufferOverrunTracker
    overrunTrackersMu sync.Mutex
)

func init() {
    overrunTrackers = make(map[string]*bufferOverrunTracker)
}

// getOverrunTracker returns the tracker for a source:model key, creating one if needed.
func getOverrunTracker(source, modelID string) *bufferOverrunTracker {
    key := source + ":" + modelID
    overrunTrackersMu.Lock()
    defer overrunTrackersMu.Unlock()
    if t, ok := overrunTrackers[key]; ok {
        return t
    }
    t := &bufferOverrunTracker{}
    overrunTrackers[key] = t
    return t
}
```

Update `ProcessData` to use `getOverrunTracker(source, modelID)` instead of the global tracker.

- [ ] **Step 4: Set ModelID in results enqueue**

In ProcessData, when building the `classifier.Results` to enqueue, set ModelID:
```go
result := classifier.Results{
    // ... existing fields ...
    ModelID: modelID,
}
```

- [ ] **Step 5: Fix all compilation errors**

Search for all callers of `ProcessData` and update. The main caller is `buffer_manager.go` (updated in Step 2). Check for any test files calling ProcessData.

- [ ] **Step 6: Run tests**

Run: `go test -race -v ./internal/analysis/... ./internal/classifier/...`
Expected: PASS

- [ ] **Step 7: Run linter**

Run: `golangci-lint run -v ./internal/analysis/... ./internal/classifier/...`

- [ ] **Step 8: Commit**

```bash
git commit -m "feat(analysis): add modelID to ProcessData with per-model overrun tracking"
```

---

## Task 6: Integration Verification

**Files:**
- No new files — verification only

- [ ] **Step 1: Run full test suite**

Run: `go test -race ./internal/analysis/... ./internal/classifier/... ./internal/audiocore/...`
Expected: All PASS

- [ ] **Step 2: Run full project linter**

Run: `golangci-lint run -v`
Expected: Zero errors

- [ ] **Step 3: Verify single-model pipeline unchanged**

The existing integration test from Phase 3e (`TestBufferConsumer_SingleModel_FullPipeline`) should still pass, confirming backwards compatibility.

- [ ] **Step 4: Commit any remaining fixes**

---

## Final: Pre-PR Checks

- [ ] **Step 1: Run local CodeRabbit review**

Run: `coderabbit review --plain -t committed --base main`

- [ ] **Step 2: Run /code-review skill**

Address any findings before creating the PR.

- [ ] **Step 3: Create PR with /watch-pr skill**
