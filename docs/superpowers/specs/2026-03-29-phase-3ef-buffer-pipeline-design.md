# Phase 3e+3f: Buffer Architecture and Pipeline Integration

Detailed design for multi-model buffer fan-out, per-model monitors, and pipeline integration. Builds on the approved multi-model classifier design spec. Implemented as two sequential PRs.

## Status of Prior Phases

| Phase | Status | PR |
|-------|--------|-----|
| 3a — Structural refactoring | Merged | #2622 |
| 3b — ModelInstance interface + Orchestrator | Merged | #2623 |
| 3c — Perch v2 model + name resolution | Merged | #2624 |
| 3d — Audio resampling | Complete on main | `internal/audiocore/resample/` |

## Approach

**Approach A: BufferConsumer-centric resampling** was selected. Resampling happens once per frame in `BufferConsumer.Write()`, before writing to analysis buffers. Each AnalysisBuffer stores audio at its model's native sample rate.

Rejected alternatives:
- **Monitor-side resampling**: Re-resamples overlap bytes on every read (wastes ~67% of resampling CPU for high-overlap configs).
- **Multi-consumer routing**: Requires audio router changes, splits buffer management across consumers, larger blast radius.

## Phase 3e: Buffer Architecture

### Buffer Manager Keying

Current `Manager` uses `map[string]*AnalysisBuffer` keyed by `sourceID` (one buffer per source). Multi-model requires one buffer per (source, model).

```go
// internal/audiocore/buffer/manager.go

type bufferKey struct {
    sourceID string
    modelID  string
}

type Manager struct {
    analysisBuffers map[bufferKey]*AnalysisBuffer  // source x model -> buffer
    captureBuffers  map[string]*CaptureBuffer      // source -> buffer (unchanged)
    bytePool        *BytePool
    float32Pools    map[int]*Float32Pool            // keyed by pool size, lazily created
    mu              sync.RWMutex
    logger          logger.Logger
}
```

API changes:
- `AllocateAnalysis(sourceID, modelID string, capacity, overlapSize, readSize int) error` -- adds modelID
- `AnalysisBuffer(sourceID, modelID string) (*AnalysisBuffer, error)` -- adds modelID
- `AnalysisBuffers(sourceID string) map[string]*AnalysisBuffer` -- returns all model buffers for a source
- `DeallocateSource(sourceID string)` -- iterates all keys, removes those matching sourceID
- `Float32Pool(size int) *Float32Pool` -- returns pool for given size, creates lazily if needed

The Float32Pool changes from a single fixed-size pool (2048) to a map keyed by pool size. Different models need different sizes: 144,384 samples for 48kHz/3s vs 160,000 for 32kHz/5s. Pools are created on first request at a given size.

### BufferConsumer Fan-Out

BufferConsumer gains model awareness and per-rate resampler caching.

```go
// internal/analysis/buffer_consumer.go

type modelTarget struct {
    modelID    string
    sampleRate int    // target rate for this model's buffer
}

type BufferConsumer struct {
    id         string
    bufferMgr  *buffer.Manager
    rate       int                              // source sample rate (e.g., 48000)
    depth      int
    channels   int
    closed     atomic.Bool
    targets    []modelTarget                    // models to fan out to
    resamplers map[int]*resample.Resampler      // keyed by target rate
}
```

Construction: `NewBufferConsumer(id, bufferMgr, sampleRate, bitDepth, channels, targets []modelTarget) (*BufferConsumer, error)`

The constructor creates one `Resampler` per unique target rate that differs from the source rate. Models sharing a target rate (e.g., BirdNET v3.0 and Perch at 32kHz) share the same resampler output.

Write flow:

```go
func (c *BufferConsumer) Write(frame audiocore.AudioFrame) error {
    // 1. Capture buffer (unchanged, always at source rate)
    if cb, _ := c.bufferMgr.CaptureBuffer(c.id); cb != nil {
        _ = cb.Write(frame.Data)
    }

    // 2. Fan out to model analysis buffers, grouped by target rate
    for rate, targets := range c.groupedTargets {
        var data []byte
        if rate == c.rate {
            data = frame.Data  // native rate, no resampling
        } else {
            resampled, err := c.resamplers[rate].ResampleInto(frame.Data)
            if err != nil { /* log, continue */ }
            data = resampled
        }
        for _, t := range targets {
            if ab, _ := c.bufferMgr.AnalysisBuffer(c.id, t.modelID); ab != nil {
                _ = ab.Write(data)
            }
        }
    }
    return nil
}
```

Safety property: `ResampleInto` returns a slice valid until the next call. All same-rate buffers are written before resampling the next rate. `AnalysisBuffer.Write()` copies data into the ring buffer, so the resampler's internal buffer can be safely reused.

Close also closes owned resamplers.

### Concrete Example: BirdNET v2.4 + Perch v2

Audio source captures at 48kHz. Two models enabled:

| | BirdNET v2.4 | Perch v2 |
|---|---|---|
| Target sample rate | 48,000 Hz | 32,000 Hz |
| Clip length | 3s | 5s |
| readSize (bytes) | 288,000 (3s x 48kHz x 2) | 320,000 (5s x 32kHz x 2) |
| overlapSize | scaled from user config | scaled from user config |
| Data stored in buffer | native 48kHz PCM | already-resampled 32kHz PCM |

On each `Write()` call (e.g., 4096-byte frame at 48kHz):
1. Write raw frame to CaptureBuffer (clip export, always 48kHz)
2. BirdNET v2.4 target rate == source rate -> write `frame.Data` directly
3. Perch v2 target rate 32kHz != 48kHz -> resample once -> write resampled data

Each AnalysisBuffer stores audio at its model's native rate. Monitors read data that's already at the correct rate for inference.

### Monitor Management

Current `BufferManager` (analysis level) spawns one `analysisBufferMonitor` goroutine per source. Multi-model requires one per (source, model).

```go
// internal/analysis/buffer_manager.go

type monitorKey struct {
    sourceID string
    modelID  string
}

type monitorConfig struct {
    sourceID    string
    modelID     string
    spec        classifier.ModelSpec  // SampleRate, ClipLength
    readSize    int                   // bytes = ClipLength x SampleRate x 2
    overlapSize int                   // bytes, scaled from user config
}
```

API changes:
- `AddMonitor(source string) error` becomes `AddMonitors(source string, models []monitorConfig) error`
- `RemoveMonitor(source string) error` removes all monitors for that source (all models)
- Monitor goroutine: `analysisBufferMonitor(quit chan struct{}, cfg monitorConfig)`
- Monitor key in `sync.Map`: `monitorKey{sourceID, modelID}` -> quit channel

Monitor goroutine changes:
- Uses `cfg.readSize` instead of hardcoded `conf.BufferSize`
- Uses `cfg.spec.SampleRate` for timing calculations
- Passes `cfg.modelID` to `ProcessData`
- Buffer overrun tracking uses model-specific effective buffer duration

### Overlap Scaling

User configures a base overlap (e.g., 2.0s for the reference 3s clip length). For models with different clip lengths, overlap scales proportionally.

```go
// effectiveOverlap scales user overlap to a model's clip length.
// Example: 2.0s overlap for 3s base -> for 5s model: (2.0 x 5) / 3 = 3.33s
func effectiveOverlap(userOverlap, baseClipLength, modelClipLength time.Duration) time.Duration {
    return (userOverlap * modelClipLength) / baseClipLength
}
```

The base clip length is BirdNET v2.4's 3s. When converting to byte offsets, the result must be aligned to PCM sample boundaries (multiples of 2 bytes for 16-bit mono). Explicitly force even byte count: `overlapBytes = (overlapBytes / bytesPerSample) * bytesPerSample`.

Note: overlap scaling is proportional, not absolute. A user setting "2s overlap" means 67% of a 3s clip. For a 5s clip, that becomes 3.33s (same 67% ratio). This must be documented in user-facing configuration.

### Float32Pool Consolidation

Currently two separate float32 pools exist: `buffer.Manager` creates one at size 2048, and `process.go` creates its own at size 144,384. These must be consolidated into the Manager's `float32Pools` map. ProcessData will request a pool from the Manager by size rather than maintaining its own global pool. Pools are created lazily under a dedicated `sync.Mutex` to prevent concurrent creation races.

## Phase 3f: Pipeline Integration

### ProcessData Changes

Add modelID parameter. Use model-aware float32 pool sizing.

```go
func ProcessData(bn *Orchestrator, data []byte, startTime, audioCapturedAt time.Time, source, modelID string) error
```

Changes:
- Select float32 pool from Manager by model's `SampleRate x ClipLength` (not hardcoded 144,384)
- Call `bn.PredictModel(ctx, modelID, samples)` instead of `bn.Predict(ctx, samples)`
- Buffer overrun tracking: refactor global `bufferOverrunTracker` into a map keyed by `source:modelID` with its own mutex. Concurrent models interleaving on the current global tracker corrupt `windowStart` and `maxElapsed`.
- Results include modelID for downstream storage
- Create context with timeout for cancellation support
- Remove the standalone float32 pool from process.go (consolidated into Manager)

### Orchestrator Multi-Model Dispatch

The Orchestrator needs two levels of locking:
- `sync.RWMutex` on the Orchestrator itself -- protects the `models` map from concurrent read (PredictModel) and write (ReloadModel). Without this, a concurrent map read+write causes a fatal Go panic.
- `sync.Mutex` per modelEntry -- serializes inference calls per model.

**Locking protocol** (critical to avoid deadlock):
1. `PredictModel` acquires Orchestrator `RLock`, fetches `modelEntry`, releases `RUnlock`
2. Then acquires `modelEntry.mu.Lock`, runs inference, releases
3. Never hold both locks simultaneously -- holding RLock while waiting for modelEntry.mu blocks ReloadModel indefinitely

```go
type Orchestrator struct {
    mu            sync.RWMutex           // protects models map
    models        map[string]*modelEntry
    nameResolvers []NameResolver
    settings      *conf.Settings
}

// PredictModel runs inference on a specific model.
func (o *Orchestrator) PredictModel(ctx context.Context, modelID string, sample [][]float32) ([]datastore.Results, error) {
    // Step 1: fetch entry under read lock (fast)
    o.mu.RLock()
    entry, ok := o.models[modelID]
    o.mu.RUnlock()  // release BEFORE acquiring model lock

    if !ok {
        return nil, errors.Newf("unknown model: %s", modelID)
    }

    // Step 2: acquire per-model lock for inference (slow)
    entry.mu.Lock()
    defer entry.mu.Unlock()
    return entry.instance.Predict(ctx, sample)
}

// ReloadModel acquires full write lock to re-key models map.
func (o *Orchestrator) ReloadModel() error {
    o.mu.Lock()
    defer o.mu.Unlock()
    // ... re-key models map, addresses Forgejo #270
}
```

- BirdNET and Perch inference run independently with no cross-model blocking
- Existing `Predict()` stays as convenience for the primary model (backwards compat)
- Inference duration metrics scoped to `Predict()` call, excluding lock wait time
- **ModelInstance.Predict contract**: implementations MUST copy input sample data to their internal tensor before returning. The caller returns the float32 buffer to the pool immediately after Predict returns. Holding a reference to the input slice after return causes data corruption.

### Name Resolution in Results

After Predict returns, results pass through the name resolver chain. BirdNET results already have common names. Perch results have only scientific names.

```go
// In ProcessData, after Predict:
for i := range results {
    if results[i].CommonName == "" {
        results[i].CommonName = bn.ResolveName(results[i].ScientificName, locale)
    }
}
```

`ResolveName` walks the resolver chain: BirdNETLabelResolver (O(1) in-memory) -> future database/API resolver.

### Detection Storage

The v2 schema already has `detections.model_id` FK. Both models write independent detection rows sharing the same `ClipName`.

```
detections:
  ID=1001  ModelID=1(BirdNET)  Species="Turdus merula"  Confidence=0.85  ClipName="clip_0845.flac"
  ID=1002  ModelID=2(Perch)    Species="Turdus merula"  Confidence=0.72  ClipName="clip_0845.flac"
```

- `classifier.Results` struct gets a `ModelID string` field
- ResultsQueue consumers use ModelID when storing
- No merging or deduplication -- both results stored independently
- Future work can correlate cross-model detections by ClipName + species

### Thread Count Division

```go
func divideThreads(total int, modelCount int) map[string]int
```

- Equal division with remainder going to the primary model (BirdNET)
- Minimum 1 thread per model
- On constrained hardware (e.g., RPi with 4 threads, 3 models): log a warning
- Thread count passed to each model at construction time

### Configuration

Minimal configuration for this phase:
- `Settings.Models` -- list of enabled model IDs (default: just BirdNET v2.4)
- Model paths: existing `Settings.BirdNET.ModelPath` for BirdNET, new `Settings.Perch.ModelPath` for Perch
- No per-model thread override (keep simple, add later)
- No per-model confidence threshold (use global threshold)

### Orchestrator Construction

When multiple models are enabled:
1. Load BirdNET (primary, always loaded)
2. For each additional enabled model: load model instance, register in `models` map
3. Build name resolver chain from loaded models' label sets
4. Divide thread count among models

## Known Limitations

- **Static model targets**: BufferConsumer targets are set at construction time. Enabling/disabling models at runtime requires recreating the BufferConsumer and its resamplers. Dynamic reconfiguration is future work.
- **Thread rebalancing on ReloadModel**: Thread counts are divided at Orchestrator construction. ReloadModel does not re-evaluate thread allocation. Future work if needed.

## Not In Scope

- Cross-model result merging or deduplication
- Per-model confidence thresholds
- UI changes to show model attribution
- Per-model thread count configuration
- BirdNET v3.0 model support (model not yet available)
- Dynamic model enable/disable without service restart

## PR Boundaries

### PR 1: Phase 3e -- Buffer Architecture

Foundation changes, no behavior change for single-model users.

Scope:
- `buffer.Manager`: composite keys, multi-model allocation/deallocation
- `buffer.Manager`: per-size Float32Pool map with sync.Mutex lazy creation
- `BufferConsumer`: model targets, fan-out Write(), resampler integration
- `BufferManager` (analysis): per-(source, model) monitors with model-aware config
- `effectiveOverlap()` scaling function with PCM byte alignment
- All existing single-model behavior preserved (one target = same as today)

**PR 1/PR 2 interface**: `monitorConfig` includes `modelID` in PR 1, but `ProcessData` does not accept `modelID` until PR 2. In PR 1, the monitor populates `monitorConfig.modelID` but the value is not passed to `ProcessData`. PR 2 updates `ProcessData` signature to accept it.

Tests:
- Manager: allocate/deallocate multiple models per source, key isolation
- BufferConsumer: fan-out writes correct data to each buffer at correct rate
- BufferConsumer: resampler output goes to right buffers, source-rate targets get raw data
- Monitor: correct readSize per model spec, independent lifecycle
- Overlap scaling: integer math, edge cases (0 overlap, same clip length)

### PR 2: Phase 3f -- Pipeline Integration

Wires multi-model end-to-end. Depends on PR 1.

Scope:
- `ProcessData`: modelID parameter, per-model float32 pool, per-model overrun tracking
- `Orchestrator.PredictModel()`: per-model locked dispatch
- Name resolver chain in result enrichment
- `classifier.Results`: ModelID field
- Thread count division
- Configuration: enabled models, Perch model path
- Orchestrator construction: load all enabled models

Tests:
- ProcessData: dispatches to correct model via Orchestrator
- PredictModel: per-model locking, unknown model error
- Name resolver: Perch scientific names resolved via BirdNETLabelResolver
- Thread division: equal split, remainder to primary, minimum 1 per model
- Integration: two models produce independent results with shared ClipName

## Forgejo Issues Addressed

- #268: move modelVersion to BirdNET instance field (Phase 3e/3f)
- #269: add Orchestrator-level synchronization (Phase 3f, PredictModel per-model locking)
- #270: re-key models map after ReloadModel (Phase 3f)
