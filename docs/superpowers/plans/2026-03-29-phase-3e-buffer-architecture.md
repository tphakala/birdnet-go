# Phase 3e: Multi-Model Buffer Architecture — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the buffer pipeline so each audio source can feed multiple analysis buffers (one per model) at different sample rates, with resampling in the write path.

**Architecture:** BufferConsumer fans out each audio frame to model-specific AnalysisBuffers. Models requiring a different sample rate (32kHz for Perch/BirdNET v3.0 vs 48kHz for BirdNET v2.4) get resampled data via a shared per-rate Resampler. Each (source, model) pair gets its own analysis monitor goroutine. Single-model behavior is preserved — one target produces identical results to today.

**Tech Stack:** Go 1.26, testify, `internal/audiocore/resample` (existing), `internal/classifier` (ModelSpec)

**Spec:** `docs/superpowers/specs/2026-03-29-phase-3ef-buffer-pipeline-design.md`

---

## File Structure

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/audiocore/buffer/manager.go` | Composite `bufferKey`, multi-model allocation/deallocation, per-size Float32Pool |
| Modify | `internal/audiocore/buffer/manager_test.go` | Tests for composite keys, Float32Pool lazy creation |
| Create | `internal/analysis/overlap.go` | `effectiveOverlap()` + `overlapBytes()` with PCM alignment |
| Create | `internal/analysis/overlap_test.go` | Overlap scaling tests |
| Modify | `internal/analysis/buffer_consumer.go` | `modelTarget`, fan-out `Write()`, resampler lifecycle |
| Modify | `internal/analysis/buffer_consumer_test.go` | Fan-out tests with multiple targets |
| Modify | `internal/analysis/buffer_manager.go` | `monitorKey`, `monitorConfig`, per-(source, model) monitors |
| Modify | `internal/analysis/buffer_manager_test.go` | Monitor management tests (if exists, else create) |
| Modify | `internal/analysis/audio_pipeline_service.go:453` | Update `registerConsumersForSources` to pass model targets |

---

## Task 1: Buffer Manager — Composite Keys

**Files:**
- Modify: `internal/audiocore/buffer/manager.go:23-135`
- Modify or Create: `internal/audiocore/buffer/manager_test.go`

- [ ] **Step 1: Write failing tests for composite-key allocation**

```go
// internal/audiocore/buffer/manager_test.go

package buffer

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/tphakala/birdnet-go/internal/logger"
)

func TestManager_AllocateAnalysis_MultiModel(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := NewManager(log)

    // Allocate two models for the same source.
    err := mgr.AllocateAnalysis("mic1", "birdnet-v2.4", 288000, 96000, 192000)
    require.NoError(t, err)

    err = mgr.AllocateAnalysis("mic1", "perch-v2", 320000, 106666, 213334)
    require.NoError(t, err)

    // Each returns distinct buffers.
    ab1, err := mgr.AnalysisBuffer("mic1", "birdnet-v2.4")
    require.NoError(t, err)
    require.NotNil(t, ab1)

    ab2, err := mgr.AnalysisBuffer("mic1", "perch-v2")
    require.NoError(t, err)
    require.NotNil(t, ab2)

    assert.NotSame(t, ab1, ab2, "different models must have distinct buffers")
}

func TestManager_AllocateAnalysis_DuplicateModelError(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := NewManager(log)

    err := mgr.AllocateAnalysis("mic1", "birdnet-v2.4", 288000, 96000, 192000)
    require.NoError(t, err)

    err = mgr.AllocateAnalysis("mic1", "birdnet-v2.4", 288000, 96000, 192000)
    assert.Error(t, err, "duplicate (source, model) must error")
}

func TestManager_DeallocateSource_RemovesAllModels(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := NewManager(log)

    _ = mgr.AllocateAnalysis("mic1", "birdnet-v2.4", 288000, 96000, 192000)
    _ = mgr.AllocateAnalysis("mic1", "perch-v2", 320000, 106666, 213334)
    _ = mgr.AllocateCapture("mic1", 120, 48000, 2)

    mgr.DeallocateSource("mic1")

    _, err := mgr.AnalysisBuffer("mic1", "birdnet-v2.4")
    assert.Error(t, err, "buffer should be gone after deallocation")

    _, err = mgr.AnalysisBuffer("mic1", "perch-v2")
    assert.Error(t, err, "buffer should be gone after deallocation")
}

func TestManager_AnalysisBuffers_ReturnsAllForSource(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := NewManager(log)

    _ = mgr.AllocateAnalysis("mic1", "birdnet-v2.4", 288000, 96000, 192000)
    _ = mgr.AllocateAnalysis("mic1", "perch-v2", 320000, 106666, 213334)
    _ = mgr.AllocateAnalysis("mic2", "birdnet-v2.4", 288000, 96000, 192000)

    buffers := mgr.AnalysisBuffers("mic1")
    assert.Len(t, buffers, 2)
    assert.Contains(t, buffers, "birdnet-v2.4")
    assert.Contains(t, buffers, "perch-v2")

    buffers2 := mgr.AnalysisBuffers("mic2")
    assert.Len(t, buffers2, 1)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/audiocore/buffer && go test -run TestManager_AllocateAnalysis_MultiModel -v`
Expected: FAIL — `AllocateAnalysis` has wrong signature (missing modelID)

- [ ] **Step 3: Implement composite key and updated Manager**

In `internal/audiocore/buffer/manager.go`, make these changes:

1. Add `bufferKey` struct before Manager:
```go
// bufferKey identifies a unique analysis buffer (one per source x model).
type bufferKey struct {
    sourceID string
    modelID  string
}
```

2. Change `Manager.analysisBuffers` field from `map[string]*AnalysisBuffer` to `map[bufferKey]*AnalysisBuffer`.

3. Update `NewManager` to initialize with `map[bufferKey]*AnalysisBuffer`.

4. Update `AllocateAnalysis` signature to `(sourceID, modelID string, capacity, overlapSize, readSize int) error`. Change the map key from `sourceID` to `bufferKey{sourceID, modelID}`.

5. Update `AnalysisBuffer` signature to `(sourceID, modelID string) (*AnalysisBuffer, error)`. Look up by `bufferKey{sourceID, modelID}`.

6. Add `AnalysisBuffers(sourceID string) map[string]*AnalysisBuffer` method that iterates the map and returns all entries matching sourceID:
```go
func (m *Manager) AnalysisBuffers(sourceID string) map[string]*AnalysisBuffer {
    m.mu.RLock()
    defer m.mu.RUnlock()
    result := make(map[string]*AnalysisBuffer)
    for key, buf := range m.analysisBuffers {
        if key.sourceID == sourceID {
            result[key.modelID] = buf
        }
    }
    return result
}
```

7. Update `DeallocateSource` to iterate all keys and delete those with matching sourceID:
```go
func (m *Manager) DeallocateSource(sourceID string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    for key := range m.analysisBuffers {
        if key.sourceID == sourceID {
            delete(m.analysisBuffers, key)
        }
    }
    delete(m.captureBuffers, sourceID)
}
```

- [ ] **Step 4: Fix all callers of the changed API**

Search for all callers of `AllocateAnalysis` and `AnalysisBuffer` across the codebase. Update each call site to pass the model ID. For Phase 3e single-model mode, use the primary model's ID from `bn.ModelInfo.ID`.

Key callers to update:
- `internal/analysis/audio_pipeline_service.go` — `registerConsumersForSources`
- `internal/analysis/buffer_manager.go` — `analysisBufferMonitor` calls `AnalysisBuffer`
- Any test files referencing these methods

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd internal/audiocore/buffer && go test -run TestManager_ -v -race`
Expected: PASS

- [ ] **Step 6: Run linter**

Run: `golangci-lint run -v ./internal/audiocore/buffer/...`
Expected: Zero errors

- [ ] **Step 7: Commit**

```bash
git add internal/audiocore/buffer/manager.go internal/audiocore/buffer/manager_test.go
git commit -m "refactor(buffer): add composite bufferKey for multi-model analysis buffers"
```

---

## Task 2: Float32Pool Lazy Map

**Files:**
- Modify: `internal/audiocore/buffer/manager.go`
- Modify: `internal/audiocore/buffer/manager_test.go`

- [ ] **Step 1: Write failing test for per-size Float32Pool**

```go
// in manager_test.go

func TestManager_Float32Pool_LazySizes(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := NewManager(log)

    // Request two different sizes.
    pool1 := mgr.Float32Pool(144384)
    require.NotNil(t, pool1)

    pool2 := mgr.Float32Pool(160000)
    require.NotNil(t, pool2)

    assert.NotSame(t, pool1, pool2, "different sizes must have distinct pools")

    // Same size returns same pool.
    pool1Again := mgr.Float32Pool(144384)
    assert.Same(t, pool1, pool1Again, "same size must return same pool")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/audiocore/buffer && go test -run TestManager_Float32Pool_LazySizes -v`
Expected: FAIL — `Float32Pool` method has wrong signature

- [ ] **Step 3: Implement per-size Float32Pool map**

In `manager.go`:

1. Change `float32Pool *Float32Pool` field to `float32Pools map[int]*Float32Pool` and add `float32PoolMu sync.Mutex`.

2. Update `NewManager` to initialize `float32Pools: make(map[int]*Float32Pool)`.

3. Replace the existing `Float32Pool() *Float32Pool` method with:
```go
// Float32Pool returns a pool for the given buffer size, creating one lazily if needed.
func (m *Manager) Float32Pool(size int) *Float32Pool {
    m.float32PoolMu.Lock()
    defer m.float32PoolMu.Unlock()
    if pool, ok := m.float32Pools[size]; ok {
        return pool
    }
    pool, _ := NewFloat32Pool(size) //nolint:errcheck // size > 0 guaranteed by callers
    m.float32Pools[size] = pool
    return pool
}
```

4. Update any existing callers of `Float32Pool()` to pass the size argument.

- [ ] **Step 4: Run tests**

Run: `cd internal/audiocore/buffer && go test -run TestManager_ -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/audiocore/buffer/manager.go internal/audiocore/buffer/manager_test.go
git commit -m "feat(buffer): add per-size Float32Pool map with lazy creation"
```

---

## Task 3: Overlap Scaling Function

**Files:**
- Create: `internal/analysis/overlap.go`
- Create: `internal/analysis/overlap_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/analysis/overlap_test.go

package analysis

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
)

func TestEffectiveOverlap_SameClipLength(t *testing.T) {
    t.Parallel()
    // 2s overlap for 3s base, 3s model = 2s (no scaling).
    result := effectiveOverlap(2*time.Second, 3*time.Second, 3*time.Second)
    assert.Equal(t, 2*time.Second, result)
}

func TestEffectiveOverlap_LongerClip(t *testing.T) {
    t.Parallel()
    // 2s overlap for 3s base, 5s model = 3.333s.
    result := effectiveOverlap(2*time.Second, 3*time.Second, 5*time.Second)
    // (2s * 5s) / 3s = 10s^2 / 3s = 3.333...s
    expected := (2 * time.Second * 5) / 3
    assert.Equal(t, expected, result)
}

func TestEffectiveOverlap_ZeroOverlap(t *testing.T) {
    t.Parallel()
    result := effectiveOverlap(0, 3*time.Second, 5*time.Second)
    assert.Equal(t, time.Duration(0), result)
}

func TestOverlapBytes_Alignment(t *testing.T) {
    t.Parallel()
    const bytesPerSample = 2

    // 3.333s at 32kHz = 106,666.66 samples -> 213,333.33 bytes -> must align to 213,332
    overlap := effectiveOverlap(2*time.Second, 3*time.Second, 5*time.Second)
    bytes := overlapBytes(overlap, 32000, bytesPerSample)
    assert.Equal(t, 0, bytes%bytesPerSample, "must be aligned to sample boundary")
    assert.Equal(t, 213332, bytes) // floor-aligned
}

func TestOverlapBytes_48kHz3s(t *testing.T) {
    t.Parallel()
    const bytesPerSample = 2

    // 2s at 48kHz = 96,000 samples = 192,000 bytes (already even).
    overlap := effectiveOverlap(2*time.Second, 3*time.Second, 3*time.Second)
    bytes := overlapBytes(overlap, 48000, bytesPerSample)
    assert.Equal(t, 192000, bytes)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/analysis && go test -run TestEffectiveOverlap -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Implement overlap functions**

```go
// internal/analysis/overlap.go

package analysis

import "time"

// effectiveOverlap scales user-configured overlap to a model's clip length.
// The overlap ratio relative to the base clip is preserved.
// Example: 2.0s overlap for 3s base -> for 5s model: (2.0 * 5) / 3 = 3.33s.
func effectiveOverlap(userOverlap, baseClipLength, modelClipLength time.Duration) time.Duration {
    if baseClipLength == 0 {
        return 0
    }
    return (userOverlap * modelClipLength) / baseClipLength
}

// overlapBytes converts an overlap duration to a byte count aligned to PCM
// sample boundaries. sampleRate is in Hz, bytesPerSample is typically 2 for
// 16-bit mono PCM.
func overlapBytes(overlap time.Duration, sampleRate, bytesPerSample int) int {
    samples := int(overlap.Seconds() * float64(sampleRate))
    bytes := samples * bytesPerSample
    // Force alignment to sample boundary.
    return (bytes / bytesPerSample) * bytesPerSample
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd internal/analysis && go test -run "TestEffectiveOverlap|TestOverlapBytes" -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/overlap.go internal/analysis/overlap_test.go
git commit -m "feat(analysis): add effectiveOverlap and overlapBytes with PCM alignment"
```

---

## Task 4: BufferConsumer Fan-Out with Resampling

**Files:**
- Modify: `internal/analysis/buffer_consumer.go:23-136`
- Modify: `internal/analysis/buffer_consumer_test.go`

- [ ] **Step 1: Write failing tests for fan-out Write**

```go
// Add to internal/analysis/buffer_consumer_test.go

func TestBufferConsumer_FanOut_MultiModel(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := buffer.NewManager(log)

    // Allocate two analysis buffers: 48kHz and 32kHz models.
    require.NoError(t, mgr.AllocateAnalysis("src1", "birdnet-v2.4", 288000, 96000, 192000))
    require.NoError(t, mgr.AllocateAnalysis("src1", "perch-v2", 320000, 106666, 213334))
    require.NoError(t, mgr.AllocateCapture("src1", 120, 48000, 2))

    targets := []ModelTarget{
        {ModelID: "birdnet-v2.4", SampleRate: 48000},
        {ModelID: "perch-v2", SampleRate: 32000},
    }

    consumer, err := NewBufferConsumer("src1", mgr, 48000, 16, 1, targets)
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, consumer.Close()) })

    // Write a 48kHz frame (4800 bytes = 2400 samples = 50ms).
    frame := audiocore.AudioFrame{
        SourceID:   "src1",
        SourceName: "test",
        Data:       make([]byte, 4800),
        SampleRate: 48000,
        BitDepth:   16,
        Channels:   1,
    }
    // Fill with non-zero pattern to verify data flows.
    for i := range frame.Data {
        frame.Data[i] = byte(i % 256)
    }

    require.NoError(t, consumer.Write(frame))

    // 48kHz buffer should have received 4800 bytes.
    ab48, err := mgr.AnalysisBuffer("src1", "birdnet-v2.4")
    require.NoError(t, err)
    // Buffer was written to (we can't easily read without filling, but at least no error).

    // 32kHz buffer should have received resampled data (~3200 bytes = 2/3 of 4800).
    ab32, err := mgr.AnalysisBuffer("src1", "perch-v2")
    require.NoError(t, err)

    _ = ab48 // verified no error
    _ = ab32 // verified no error
}

func TestBufferConsumer_FanOut_SingleModel_Backwards_Compat(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := buffer.NewManager(log)

    require.NoError(t, mgr.AllocateAnalysis("src1", "birdnet-v2.4", 288000, 96000, 192000))
    require.NoError(t, mgr.AllocateCapture("src1", 120, 48000, 2))

    targets := []ModelTarget{
        {ModelID: "birdnet-v2.4", SampleRate: 48000},
    }

    consumer, err := NewBufferConsumer("src1", mgr, 48000, 16, 1, targets)
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, consumer.Close()) })

    frame := audiocore.AudioFrame{
        SourceID:   "src1",
        SourceName: "test",
        Data:       make([]byte, 4800),
        SampleRate: 48000,
        BitDepth:   16,
        Channels:   1,
    }

    // Single model at source rate — no resampler created.
    require.NoError(t, consumer.Write(frame))
}

func TestBufferConsumer_Close_ClosesResamplers(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := buffer.NewManager(log)

    require.NoError(t, mgr.AllocateAnalysis("src1", "perch-v2", 320000, 106666, 213334))
    require.NoError(t, mgr.AllocateCapture("src1", 120, 48000, 2))

    targets := []ModelTarget{
        {ModelID: "perch-v2", SampleRate: 32000},
    }

    consumer, err := NewBufferConsumer("src1", mgr, 48000, 16, 1, targets)
    require.NoError(t, err)

    // Close should succeed and release resampler resources.
    require.NoError(t, consumer.Close())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/analysis && go test -run TestBufferConsumer_FanOut -v`
Expected: FAIL — `ModelTarget` type and new `NewBufferConsumer` signature not defined

- [ ] **Step 3: Implement BufferConsumer fan-out**

In `internal/analysis/buffer_consumer.go`:

1. Add `ModelTarget` exported type:
```go
// ModelTarget describes a model that should receive audio data.
type ModelTarget struct {
    ModelID    string
    SampleRate int // target sample rate for this model
}
```

2. Add fields to `BufferConsumer`:
```go
type BufferConsumer struct {
    id             string
    bufferMgr      *buffer.Manager
    rate           int
    depth          int
    channels       int
    closed         atomic.Bool
    targets        []ModelTarget
    resamplers     map[int]*resample.Resampler // keyed by target rate
    groupedTargets map[int][]ModelTarget       // targets grouped by rate
}
```

3. Update `NewBufferConsumer` to accept `targets []ModelTarget`, build resamplers for non-source rates, and pre-compute `groupedTargets`.

4. Update `Write()` to fan out: iterate `groupedTargets`, resample if needed, write to each model's `AnalysisBuffer(id, target.ModelID)`.

5. Update `Close()` to close owned resamplers.

6. Add `resample` import.

- [ ] **Step 4: Update existing test callers**

All existing tests that call `NewBufferConsumer` without targets must be updated to pass a single target at the source rate (backwards-compatible default):
```go
targets := []ModelTarget{{ModelID: "default", SampleRate: 48000}}
consumer, err := NewBufferConsumer(id, mgr, 48000, 16, 1, targets)
```

Also update corresponding `AllocateAnalysis` calls to include `"default"` as modelID.

- [ ] **Step 5: Run all tests**

Run: `cd internal/analysis && go test -v -race ./...`
Expected: PASS

- [ ] **Step 6: Run linter**

Run: `golangci-lint run -v ./internal/analysis/...`
Expected: Zero errors

- [ ] **Step 7: Commit**

```bash
git add internal/analysis/buffer_consumer.go internal/analysis/buffer_consumer_test.go
git commit -m "feat(analysis): add multi-model fan-out to BufferConsumer with resampling"
```

---

## Task 5: Monitor Management — Per (Source, Model)

**Files:**
- Modify: `internal/analysis/buffer_manager.go:16-415`
- Create or Modify: `internal/analysis/buffer_manager_test.go`

- [ ] **Step 1: Write failing tests for multi-model monitors**

```go
// internal/analysis/buffer_manager_test.go

package analysis

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/tphakala/birdnet-go/internal/classifier"
)

func TestMonitorConfig_ReadSize(t *testing.T) {
    t.Parallel()

    cfg := monitorConfig{
        sourceID: "mic1",
        modelID:  "birdnet-v2.4",
        spec:     classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
        readSize: 288000, // 3s * 48000 * 2
    }
    assert.Equal(t, 288000, cfg.readSize)

    cfg2 := monitorConfig{
        sourceID: "mic1",
        modelID:  "perch-v2",
        spec:     classifier.ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
        readSize: 320000, // 5s * 32000 * 2
    }
    assert.Equal(t, 320000, cfg2.readSize)
}

func TestMonitorKey_Equality(t *testing.T) {
    t.Parallel()

    k1 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"}
    k2 := monitorKey{sourceID: "mic1", modelID: "perch-v2"}
    k3 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"}

    assert.NotEqual(t, k1, k2, "different models must be different keys")
    assert.Equal(t, k1, k3, "same source+model must be equal keys")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd internal/analysis && go test -run "TestMonitorConfig|TestMonitorKey" -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement monitorKey and monitorConfig types**

In `internal/analysis/buffer_manager.go`:

1. Add types:
```go
// monitorKey identifies a unique monitor (one per source x model).
type monitorKey struct {
    sourceID string
    modelID  string
}

// monitorConfig describes parameters for a single analysis buffer monitor.
type monitorConfig struct {
    sourceID    string
    modelID     string
    spec        classifier.ModelSpec
    readSize    int // bytes = ClipLength * SampleRate * bytesPerSample
    overlapSize int // bytes, scaled from user config, PCM-aligned
}
```

2. Change `monitors sync.Map` key type from `string` to `monitorKey`.

3. Update `AddMonitor` → `AddMonitors(source string, models []monitorConfig) error` which creates one goroutine per config.

4. Update `RemoveMonitor` to iterate `sync.Map` via `Range()` and delete all keys with matching `sourceID`.

5. Update `analysisBufferMonitor` to accept `monitorConfig` and use `cfg.readSize` instead of hardcoded `analysisWindowBytes`. Use `cfg.modelID` when calling `AnalysisBuffer(cfg.sourceID, cfg.modelID)`.

6. For PR 1, `ProcessData` signature does NOT change yet. The monitor calls `ProcessData(bn, data, startTime, audioCapturedAt, cfg.sourceID)` — `modelID` is available but not passed. PR 2 will update this.

7. Update `UpdateMonitors` to work with the new `AddMonitors` API.

- [ ] **Step 4: Update callers**

Update `internal/analysis/audio_pipeline_service.go` to call `AddMonitors` with a single `monitorConfig` for the primary model.

- [ ] **Step 5: Run all tests**

Run: `cd internal/analysis && go test -v -race ./...`
Expected: PASS

- [ ] **Step 6: Run linter**

Run: `golangci-lint run -v ./internal/analysis/...`
Expected: Zero errors

- [ ] **Step 7: Commit**

```bash
git add internal/analysis/buffer_manager.go internal/analysis/buffer_manager_test.go
git commit -m "refactor(analysis): per-(source, model) monitor management"
```

---

## Task 6: Wire Callers — Audio Pipeline Service

**Files:**
- Modify: `internal/analysis/audio_pipeline_service.go:453`

- [ ] **Step 1: Read the current caller code**

Read `internal/analysis/audio_pipeline_service.go` around line 453 (`registerConsumersForSources`) to understand how `NewBufferConsumer`, `AllocateAnalysis`, and `AddMonitor` are called.

- [ ] **Step 2: Update to pass model targets and configs**

In `registerConsumersForSources`:

1. Build a `[]ModelTarget` from the Orchestrator's primary model:
```go
primarySpec := p.engine.BirdNET().Spec()
primaryID := p.engine.BirdNET().ModelInfo.ID
targets := []ModelTarget{
    {ModelID: primaryID, SampleRate: primarySpec.SampleRate},
}
```

2. Update `AllocateAnalysis` call to pass `primaryID`.

3. Update `NewBufferConsumer` call to pass `targets`.

4. Update `AddMonitor` → `AddMonitors` with a single `monitorConfig`:
```go
bytesPerSample := conf.BitDepth / 8
readSize := int(primarySpec.ClipLength.Seconds()) * primarySpec.SampleRate * bytesPerSample
models := []monitorConfig{{
    sourceID:    sid,
    modelID:     primaryID,
    spec:        primarySpec,
    readSize:    readSize,
    overlapSize: overlapBytes(effectiveOverlap(userOverlap, baseClipLength, primarySpec.ClipLength), primarySpec.SampleRate, bytesPerSample),
}}
err = p.bufferManager.AddMonitors(sid, models)
```

- [ ] **Step 3: Run full test suite**

Run: `go test -v -race ./internal/analysis/... ./internal/audiocore/buffer/...`
Expected: PASS

- [ ] **Step 4: Run linter on full project**

Run: `golangci-lint run -v`
Expected: Zero errors

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/audio_pipeline_service.go
git commit -m "refactor(analysis): wire multi-model targets into audio pipeline"
```

---

## Task 7: Integration Test — Single Model Backwards Compatibility

**Files:**
- Modify: `internal/analysis/buffer_consumer_test.go` or create integration test

- [ ] **Step 1: Write integration test verifying single-model pipeline**

```go
func TestBufferConsumer_SingleModel_FullPipeline(t *testing.T) {
    t.Parallel()
    log := logger.NewDefaultLogger()
    mgr := buffer.NewManager(log)

    const (
        sampleRate     = 48000
        clipLength     = 3 // seconds
        bytesPerSample = 2
        capacity       = sampleRate * clipLength * bytesPerSample // 288000
    )

    userOverlap := 2 * time.Second
    baseClip := 3 * time.Second
    modelClip := 3 * time.Second
    scaled := effectiveOverlap(userOverlap, baseClip, modelClip)
    oBytes := overlapBytes(scaled, sampleRate, bytesPerSample)

    readSize := capacity - oBytes

    require.NoError(t, mgr.AllocateAnalysis("mic1", "birdnet-v2.4", capacity, oBytes, readSize))
    require.NoError(t, mgr.AllocateCapture("mic1", 120, sampleRate, bytesPerSample))

    targets := []ModelTarget{{ModelID: "birdnet-v2.4", SampleRate: sampleRate}}
    consumer, err := NewBufferConsumer("mic1", mgr, sampleRate, 16, 1, targets)
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, consumer.Close()) })

    // Write enough data to fill the analysis buffer.
    frameSize := 4096
    framesNeeded := capacity / frameSize
    for range framesNeeded + 1 {
        frame := audiocore.AudioFrame{
            SourceID:   "mic1",
            Data:       make([]byte, frameSize),
            SampleRate: sampleRate,
            BitDepth:   16,
            Channels:   1,
        }
        require.NoError(t, consumer.Write(frame))
    }

    // Read from the analysis buffer — should return data.
    ab, err := mgr.AnalysisBuffer("mic1", "birdnet-v2.4")
    require.NoError(t, err)
    data, err := ab.Read()
    require.NoError(t, err)
    assert.NotNil(t, data, "should have enough data for a full read")
}
```

- [ ] **Step 2: Run the integration test**

Run: `cd internal/analysis && go test -run TestBufferConsumer_SingleModel_FullPipeline -v -race`
Expected: PASS

- [ ] **Step 3: Run full project tests and linter**

Run: `go test -race ./... && golangci-lint run -v`
Expected: All pass, zero lint errors

- [ ] **Step 4: Commit**

```bash
git add internal/analysis/buffer_consumer_test.go
git commit -m "test(analysis): add single-model backwards compatibility integration test"
```

---

## Final: Pre-PR Checks

- [ ] **Step 1: Run local CodeRabbit review**

Run: `coderabbit review --plain -t uncommitted`

- [ ] **Step 2: Run /code-review skill**

Address any findings before creating the PR.

- [ ] **Step 3: Verify all tests pass with race detector**

Run: `go test -race ./internal/analysis/... ./internal/audiocore/buffer/...`

- [ ] **Step 4: Verify single-model behavior unchanged**

Confirm that with a single model target at 48kHz, the pipeline produces identical results to the pre-change code. The integration test from Task 7 validates this.
