# Audiocore Phase 2 Migration Design

## Goal

Wire `AudioEngine` into the application, replace all `internal/myaudio` global singletons and pipeline functions with dependency-injected audiocore equivalents, then delete `internal/myaudio/`.

## Approach

Constructor injection (Approach A). The `AudioEngine` is created once in `cmd/serve/` and passed to services that need audio subsystem access. No new global singletons.

## Decomposition: 3 PRs

### PR 1: Type Migrations (Low Risk)

Move types and constants from `myaudio` to their audiocore equivalents. Pure mechanical import swaps with no behavioral change.

#### Types to Relocate

| myaudio symbol | Target location | Notes |
|---|---|---|
| `AudioLevelData` | `audiocore.AudioLevelData` | New type in audiocore package |
| `StreamHealth`, `StateTransition`, `ErrorContext` | `audiocore/ffmpeg` | Already exist |
| `ProcessState*` constants | `audiocore/ffmpeg` | Already exist |
| `AudioSource`, `SourceType*`, `StreamTypeToSourceType()` | `audiocore` | Already exist |
| `QuietHoursScheduler` | `audiocore/schedule` | Already exists |
| `OctaveBandData` | `audiocore/soundlevel` | Already exists |

#### Functions to Re-import

| myaudio function | audiocore equivalent |
|---|---|
| `SavePCMDataToWAV()` | `convert.SavePCMDataToWAV()` |
| `EncodePCMtoWAVWithContext()` | `convert.EncodePCMtoWAVWithContext()` |
| `ExportAudioWithFFmpeg()` | `ffmpeg.ExportAudio()` |
| `ExportAudioWithCustomFFmpegArgsContext()` | `ffmpeg.ExportAudioWithCustomArgs()` |
| `AnalyzeAudioLoudnessWithContext()` | `ffmpeg.AnalyzeAudioLoudness()` |
| `GetFileExtension()` | `ffmpeg.GetFileExtension()` |
| `InitFloat32Pool()` | `buffer.NewFloat32Pool()` |
| `ReadAudioFileBuffered()` | `readfile.ReadAudioFileBuffered()` |
| `GetAudioInfo()` | `readfile.GetAudioInfo()` |
| `GetTotalChunks()` | `readfile.GetTotalChunks()` |
| `GetAudioDuration()` | `ffmpeg.GetAudioDuration()` |
| `ValidateAudioFile()` | `ffmpeg.ValidateAudioFile()` |

#### AudioLevelData

`AudioLevelData` is used as a channel type across API server and analysis packages. Define it in `audiocore` package:

```go
// AudioLevelData represents real-time audio level information for a source.
type AudioLevelData struct {
    SourceID   string
    Level      float64
    Peak       float64
    Timestamp  time.Time
}
```

#### Exported Constants Audit

Audit all exported constants in `myaudio` (`grep -r "const [A-Z]" internal/myaudio`) and map any that are referenced outside `myaudio` to their audiocore equivalents. Format/bitrate constants in `ffmpeg_clip.go` and `ffmpeg_export.go` are already in `audiocore/ffmpeg`.

#### Affected Files (~25)

All files currently importing `myaudio` for types/constants/pure functions. Tests updated to use new import paths.

#### Success Criteria

- `internal/myaudio` still exists but has fewer importers
- No behavioral change — only import paths differ
- All tests pass with `-race`
- Linter clean

---

### PR 2: Wire AudioEngine + Replace Global Singletons (Medium Risk)

Create `AudioEngine` in the application startup path and pass it via constructors to services that need audio subsystem access. Replace global getter/setter patterns in API and analysis consumers. The `myaudio` package retains its own internal globals for pipeline functions that still depend on them — those are removed in PR 3.

#### Engine Creation

In `cmd/serve/serve.go` (or the service registration code):

```go
audioEngine := engine.New(ctx, &engine.Config{
    Settings: settings,
    // Metrics wired here
}, scheduler)
// Pass to services:
apiService := analysis.NewAPIServerService(settings, analyzer, dbService, metrics, audioEngine)
pipelineService := analysis.NewAudioPipelineService(settings, analyzer, dbService, apiService, audioEngine)
```

Note: `engine.New()` takes a context, config, and scheduler. There is no `engine.Start()` — subsystems initialize during construction and sources are started via `engine.AddSource()`.

#### Constructor Changes

| Service | New parameter | Stores as |
|---|---|---|
| `AudioPipelineService` | `*engine.AudioEngine` | `s.engine` field |
| `APIServerService` | `*engine.AudioEngine` | `s.engine` field |
| API v2 `Controller` | `*engine.AudioEngine` | `c.engine` field |

#### Global Singleton Replacements

| Current global | Replacement | Call sites |
|---|---|---|
| `myaudio.GetRegistry()` | `c.engine.Registry()` (API) or `s.engine.Registry()` (analysis) | ~12 files |
| `myaudio.GetGlobalScheduler()` | `c.engine.Scheduler()` | 1 file |
| `myaudio.GetStreamHealth()` | `c.engine.FFmpegManager().AllStreamHealth()` | 2 files |
| `myaudio.SetGlobalScheduler(sched)` | Engine owns scheduler — created during `engine.New()` | 1 file |
| `myaudio.SetOnStreamReset(cb)` | `engine.FFmpegManager().SetOnStreamReset(cb)` | 1 file |
| `myaudio.SetCurrentAudioChan(ch)` | Deferred to PR 3 (pipeline replacement) | 2 files |

**Important:** PR 2 migrates the *consumers* of these globals (API handlers, analysis modules) to use the engine. The `myaudio` package's own internal usage of its globals is untouched — those globals still exist and are used by the pipeline functions that remain until PR 3.

#### API v2 Handler Pattern

Before:
```go
func (c *Controller) handleStreamsHealth(w http.ResponseWriter, r *http.Request) {
    health := myaudio.GetStreamHealth()
}
```

After:
```go
func (c *Controller) handleStreamsHealth(w http.ResponseWriter, r *http.Request) {
    health := c.engine.FFmpegManager().AllStreamHealth()
}
```

#### Affected Files (~12 production + tests)

- `cmd/serve/serve.go` — engine creation
- `internal/analysis/audio_pipeline_service.go` — constructor + field
- `internal/analysis/api_service.go` — constructor + field
- `internal/analysis/control_monitor.go` — registry/health access
- `internal/analysis/sound_level.go` — registry access
- `internal/analysis/processor/processor.go` — registry access
- `internal/analysis/processor/mqtt.go` — registry access
- `internal/api/v2/api.go` — Controller struct + constructor
- `internal/api/v2/streams_health.go` — health access
- `internal/api/v2/quiet_hours.go` — scheduler access
- `internal/api/v2/settings_audio.go` — registry access
- `internal/api/v2/audio_level.go` — registry access

#### Success Criteria

- `AudioEngine` instantiated in startup, passed via constructors
- Zero calls to `myaudio.Get*()` / `myaudio.Set*()` from API and analysis consumers (except `SetCurrentAudioChan` deferred to PR 3)
- myaudio internal globals still functional for its own pipeline code
- No behavioral change — same data, different access path
- All tests pass with `-race`
- Linter clean

---

### PR 3: Replace Pipeline Functions + Delete myaudio (High Risk)

Replace the remaining pipeline functions with AudioEngine equivalents and delete `internal/myaudio/`.

#### Buffer Configuration

The engine currently uses hardcoded buffer defaults (`defaultCaptureDuration = 15`, `defaultAnalysisCapacity = 288000`). These must be made configurable via `SourceConfig` to preserve user settings:

```go
type SourceConfig struct {
    // ... existing fields ...
    CaptureBufferDuration int  // seconds, from settings.Realtime.ExtendedCapture
    AnalysisBufferSize    int  // bytes, from settings
    AnalysisOverlap       int  // bytes
}
```

The hardcoded defaults become fallbacks when config values are zero.

#### Pipeline Replacements

| Current | Replacement | Notes |
|---|---|---|
| `CaptureAudio(settings, wg, done, restart, audioChan)` | Iterate configured sources, call `engine.AddSource()` for each | Engine manages per-source capture lifecycle |
| `InitAnalysisBuffers()` / `InitCaptureBuffers()` | `engine.BufferManager()` creates buffers per source via `SourceConfig` | Buffer sizes derived from settings |
| `ReconfigureStreams(settings, wg, quit, restart, audioChan)` | `engine.ReconfigureSource()` | Engine handles stream restart |
| `RegisterSoundLevelProcessor()` / `UnregisterSoundLevelProcessor()` | `engine.Router().AddRoute()` with `SoundLevelConsumer` | Consumer already exists in analysis/ |
| `RegisterBroadcastCallback()` / `UnregisterBroadcastCallback()` | `engine.Router().AddRoute()` with `HLSConsumer` | New consumer wrapping callback |
| `AnalysisBufferMonitor()` | `BufferConsumer` handles via routing | Already exists in analysis/ |
| `SetCurrentAudioChan(ch)` | Removed — AudioRouter dispatches to consumers | No global channel needed |
| `ShutdownFFmpegManagerWithContext(ctx)` | `engine.Stop()` | Engine coordinates all shutdown |

#### Restart Loop Rewrite

The `AudioPipelineService` has a background goroutine looping on `p.restartChan` that triggers `p.restartAudioCapture()`. Currently this stops the demuxer, creates a new unified audio channel, and starts a new `CaptureAudio` goroutine.

New approach:
```go
func (p *AudioPipelineService) restartAudioCapture() {
    // 1. Stop all existing sources
    for _, src := range p.engine.Registry().Sources() {
        p.engine.RemoveSource(src.ID)
    }
    // 2. Re-read settings for current source configuration
    // 3. Re-add all configured sources
    for _, sourceCfg := range p.buildSourceConfigs() {
        p.engine.AddSource(sourceCfg)
    }
}
```

The `AudioDemuxManager` that reads from the unified channel is deleted. Its responsibilities are replaced by AudioRouter consumers.

#### Audio Level Delivery to API

The current `AudioDemuxManager` reads from the unified channel and pushes to `apiService.AudioLevelChan()`. With the unified channel removed, create an `AudioLevelConsumer`:

```go
// AudioLevelConsumer implements audiocore.AudioConsumer and pushes
// AudioLevelData to the API server's audio level channel.
type AudioLevelConsumer struct {
    sourceID string
    levelCh  chan<- audiocore.AudioLevelData
}

func (c *AudioLevelConsumer) Consume(frame audiocore.AudioFrame) {
    level := calculateLevel(frame.Data)
    c.levelCh <- audiocore.AudioLevelData{
        SourceID: c.sourceID, Level: level, ...
    }
}
```

Register on the AudioRouter for each source.

#### HLS Broadcast Callbacks

The `AudioDataCallback` type in myaudio is `func(sourceID string, data []byte)`. The `HLSConsumer` must preserve this signature:

```go
type HLSConsumer struct {
    sourceID string
    callback func(sourceID string, data []byte)
}

func (h *HLSConsumer) Consume(frame audiocore.AudioFrame) {
    h.callback(h.sourceID, frame.Data)
}
```

#### Metrics Wiring

```go
audioEngine := engine.New(ctx, &engine.Config{
    Settings:      settings,
    RouterMetrics: prometheusRouterMetrics,
    StreamMetrics: prometheusStreamMetrics,
    BufferMetrics: prometheusBufferMetrics,
    DeviceMetrics: prometheusDeviceMetrics,
}, scheduler)
```

The five `myaudio.Set*Metrics()` calls in `observability/metrics.go` are replaced by passing concrete metrics implementations into the engine config at creation time.

#### Sound Level Registration

Current pattern:
```go
myaudio.RegisterSoundLevelProcessor(sourceID, callback)
```

New pattern:
```go
consumer := analysis.NewSoundLevelConsumer(sourceID, callback)
engine.Router().AddRoute(sourceID, consumer)
```

The `SoundLevelConsumer` already exists in `internal/analysis/`.

#### Delete

- `internal/myaudio/` — entire directory
- `internal/analysis/soundlevel_convert.go` — transitional helpers
- `internal/analysis/audio_demux_manager.go` — replaced by AudioRouter consumers
- `internal/observability/metrics/myaudio.go` — myaudio-specific metrics adapter
- Any remaining myaudio imports

#### Affected Files (~10 production + tests)

- `internal/analysis/audio_pipeline_service.go` — pipeline startup/shutdown/restart rewrite
- `internal/analysis/buffer_manager.go` — buffer initialization
- `internal/analysis/birdnet_service.go` — pool init
- `internal/analysis/control_monitor.go` — reconfigure
- `internal/analysis/sound_level.go` — processor registration
- `internal/analysis/sound_level_manager.go` — debug settings
- `internal/api/v2/audio_hls.go` — broadcast callbacks → HLSConsumer
- `internal/api/v2/audio_level.go` — AudioLevelConsumer registration
- `internal/observability/metrics.go` — metrics wiring
- `internal/audiocore/engine/engine.go` — configurable buffer params in SourceConfig

#### Success Criteria

- `internal/myaudio/` directory deleted
- Zero imports of `internal/myaudio` anywhere in the codebase
- Audio capture, analysis, HLS streaming, sound level monitoring all work
- Metrics reported correctly
- Quiet hours scheduling works
- Stream reconfiguration and restart loop work
- All tests pass with `-race`
- Linter clean

---

## Risk Mitigation

1. **PR ordering matters.** Each PR must be merged before the next starts. PR 1 reduces myaudio surface area. PR 2 eliminates globals from consumers. PR 3 replaces pipeline and deletes.
2. **Integration test after each PR.** Run the full test suite including testcontainer tests.
3. **PR 3 is the riskiest.** The audio pipeline startup flow, restart loop, and data routing all change. Manual smoke test with a real audio source recommended.
4. **Rollback.** Each PR is independently revertible since they target `next`, not `main`.
5. **myaudio internal globals preserved during PR 2.** Only consumer-side access is migrated. The myaudio pipeline functions still use their own globals until PR 3 deletes them.

## Out of Scope

- `EnhancedError.Is()` category matching (tracked in Forgejo #64)
- Metrics interface implementations (tracked in Forgejo #60 — unblocked by PR 3)
- FFmpeg stream resilience improvements (completed in PR #2497)
- CaptureBuffer improvements (completed in PR #2498)
- myaudio missing `Free()` calls (tracked in Forgejo #65 — resolved when myaudio is deleted)
