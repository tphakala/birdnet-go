# Audiocore Phase 2 — PR 2: Wire AudioEngine + Replace Globals

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create AudioEngine at application startup, pass it through DI to services and API controller, replace scheduler globals and stream reset callback with engine methods, and migrate StreamHealth types from myaudio to audiocore/ffmpeg.

**Architecture:** The AudioEngine is created in `cmd/serve/serve.go` and passed to `APIServerService` and `AudioPipelineService` via constructor injection. The API v2 Controller gets engine access via a new `WithAudioEngine()` functional option. `GetRegistry()` and `GetStreamHealth()` remain on myaudio for now — they move to the engine in PR 3 when `AddSource` replaces `CaptureAudio`.

**Tech Stack:** Go 1.26, testify, golangci-lint

**Spec:** `docs/superpowers/specs/2026-03-23-audiocore-phase2-migration-design.md`

---

## Pre-flight

Before starting, read these files:
- `internal/CLAUDE.md` — Go coding standards
- `TESTING.md` — Test patterns (testify required)
- `internal/audiocore/engine/engine.go` — AudioEngine New(), Config, public methods
- `internal/api/v2/api.go` — Controller struct, functional options pattern
- `internal/api/server.go` — Server constructor, ServerOption pattern
- `internal/analysis/audio_pipeline_service.go` — AudioPipelineService struct + constructor
- `internal/analysis/api_service.go` — APIServerService struct + constructor

## Scope Clarification

**IN SCOPE (PR 2):**
- AudioEngine creation in startup path
- Engine field in service/controller constructors
- Replace `myaudio.SetGlobalScheduler()` / `GetGlobalScheduler()` → `engine.Scheduler()`
- Replace `myaudio.SetOnStreamReset()` → `engine.FFmpegManager().SetOnStreamReset()`
- Migrate StreamHealth/ProcessState types in streams_health.go/.test from myaudio → ffmpeg

**OUT OF SCOPE (PR 3):**
- `myaudio.GetRegistry()` — engine registry not populated until AddSource replaces CaptureAudio
- `myaudio.GetStreamHealth()` — engine ffmpeg manager has no streams until PR 3
- Pipeline functions (CaptureAudio, InitBuffers, etc.)

---

### Task 1: Add WithAudioEngine functional option to API v2 Controller

**Files:**
- Modify: `internal/api/v2/api.go` — add engine field to Controller, add WithAudioEngine option
- Test: existing tests should still compile

- [ ] **Step 1: Read api.go to understand Controller struct and Option pattern**

Read `internal/api/v2/api.go` lines 43-160 for the Controller struct fields and existing functional options (WithAuthMiddleware, WithAuthService, etc.).

- [ ] **Step 2: Add engine field to Controller struct**

Add after the existing fields (around line 120):
```go
// engine is the audiocore AudioEngine providing access to audio subsystems.
// May be nil during tests or when audio is not configured.
engine *engine.AudioEngine
```

Add import: `"github.com/tphakala/birdnet-go/internal/audiocore/engine"`

- [ ] **Step 3: Add WithAudioEngine functional option**

Add after the existing Option functions:
```go
// WithAudioEngine sets the AudioEngine for audio subsystem access.
func WithAudioEngine(e *engine.AudioEngine) Option {
	return func(c *Controller) {
		c.engine = e
	}
}
```

- [ ] **Step 4: Run linter**

Run: `golangci-lint run ./internal/api/v2/...`
Expected: 0 issues (engine field unused for now — that's OK, it'll be used in later tasks)

- [ ] **Step 5: Commit**

```bash
git add internal/api/v2/api.go
git commit -m "feat: add WithAudioEngine functional option to API v2 Controller"
```

---

### Task 2: Add WithAudioEngine to api.Server

**Files:**
- Modify: `internal/api/server.go` — add engine field to Server, add ServerOption, wire to Controller

- [ ] **Step 1: Read server.go to understand Server struct and ServerOption pattern**

Read `internal/api/server.go` lines 50-170 for the Server struct and existing ServerOption functions.

- [ ] **Step 2: Add engine field to Server struct and ServerOption**

Add field to Server struct:
```go
engine *engine.AudioEngine
```

Add ServerOption:
```go
// WithAudioEngine sets the AudioEngine for audio subsystem access.
func WithAudioEngine(e *engine.AudioEngine) ServerOption {
	return func(s *Server) {
		s.engine = e
	}
}
```

Add import: `"github.com/tphakala/birdnet-go/internal/audiocore/engine"`

- [ ] **Step 3: Wire engine to Controller creation**

Find where `apiv2.New()` or `apiv2.NewWithOptions()` is called (around line 310-326). Add `apiv2.WithAudioEngine(s.engine)` to the options list.

- [ ] **Step 4: Run linter**

Run: `golangci-lint run ./internal/api/...`
Expected: 0 issues

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go
git commit -m "feat: add WithAudioEngine to api.Server, wire to Controller"
```

---

### Task 3: Add engine field to analysis services

**Files:**
- Modify: `internal/analysis/api_service.go` — add engine to APIServerService constructor
- Modify: `internal/analysis/audio_pipeline_service.go` — add engine to AudioPipelineService constructor

- [ ] **Step 1: Read both service constructors**

Read `internal/analysis/api_service.go` (NewAPIServerService) and `internal/analysis/audio_pipeline_service.go` (NewAudioPipelineService) to understand current fields and constructor params.

- [ ] **Step 2: Add engine field to APIServerService**

Add to struct:
```go
engine *engine.AudioEngine
```

Update constructor signature:
```go
func NewAPIServerService(settings *conf.Settings, bnAnalyzer *BirdNETAnalyzer, dbService *DatabaseService, metrics *observability.Metrics, audioEngine *engine.AudioEngine) *APIServerService {
```

Store in constructor body:
```go
engine: audioEngine,
```

Add import: `"github.com/tphakala/birdnet-go/internal/audiocore/engine"`

- [ ] **Step 3: Wire engine to api.Server in APIServerService.Start()**

Find where `api.New()` is called in `APIServerService.Start()`. Add `api.WithAudioEngine(s.engine)` to the server options.

- [ ] **Step 4: Add engine field to AudioPipelineService**

Add to struct:
```go
engine *engine.AudioEngine
```

Update constructor signature:
```go
func NewAudioPipelineService(settings *conf.Settings, bnAnalyzer *BirdNETAnalyzer, dbService *DatabaseService, apiService *APIServerService, audioEngine *engine.AudioEngine) *AudioPipelineService {
```

Store in constructor body.

Add import: `"github.com/tphakala/birdnet-go/internal/audiocore/engine"`

- [ ] **Step 5: Run linter to catch compilation errors**

Run: `golangci-lint run ./internal/analysis/...`

This will likely fail because the callers of these constructors haven't been updated yet. That's expected — we fix callers in Task 4.

- [ ] **Step 6: Commit (even if linter fails — callers fixed next)**

```bash
git add internal/analysis/api_service.go internal/analysis/audio_pipeline_service.go
git commit -m "feat: add AudioEngine field to analysis service constructors"
```

---

### Task 4: Add SetScheduler method to AudioEngine

**Files:**
- Modify: `internal/audiocore/engine/engine.go` — add SetScheduler method

The `QuietHoursScheduler` requires `SunCalc` and `ControlChan` which aren't available at application startup — they're created inside `APIServerService.Start()`. So the engine must be created with `nil` scheduler, and the scheduler set later.

- [ ] **Step 1: Read engine.go to understand current Scheduler() getter**

- [ ] **Step 2: Add SetScheduler method**

```go
// SetScheduler replaces the engine's quiet hours scheduler.
// This is needed because the scheduler depends on SunCalc and ControlChan
// which are only available after the API service starts.
func (e *AudioEngine) SetScheduler(s *schedule.QuietHoursScheduler) {
	e.scheduler = s
}
```

- [ ] **Step 3: Run linter**

Run: `golangci-lint run ./internal/audiocore/engine/...`
Expected: 0 issues

- [ ] **Step 4: Commit**

```bash
git add internal/audiocore/engine/engine.go
git commit -m "feat: add SetScheduler method to AudioEngine for deferred initialization"
```

---

### Task 5: Create AudioEngine in cmd/serve and pass to services

**Files:**
- Modify: `cmd/serve/serve.go` — create engine, update service constructor calls

- [ ] **Step 1: Read serve.go to find service creation**

Read `cmd/serve/serve.go` to find where `NewAPIServerService` and `NewAudioPipelineService` are called.

- [ ] **Step 2: Create AudioEngine before service creation**

Add after settings are loaded, before service creation:
```go
import "github.com/tphakala/birdnet-go/internal/audiocore/engine"

// Create the AudioEngine with nil scheduler — scheduler is set later
// in AudioPipelineService.Start() once SunCalc and ControlChan are available.
audioEngine := engine.New(cmd.Context(), &engine.Config{}, nil)
```

- [ ] **Step 3: Update constructor calls**

Pass `audioEngine` to both service constructors:
```go
apiService := analysis.NewAPIServerService(settings, bnAnalyzer, dbService, metrics, audioEngine)
audioService := analysis.NewAudioPipelineService(settings, bnAnalyzer, dbService, apiService, audioEngine)
```

- [ ] **Step 4: Run linter to verify compilation**

Run: `golangci-lint run ./cmd/... ./internal/analysis/... ./internal/api/...`
Expected: 0 issues (or only pre-existing)

- [ ] **Step 5: Run tests**

Run: `go test -race ./internal/api/v2/... -run Test`
Expected: Tests may need updating if they call service constructors directly. Fix any that fail.

- [ ] **Step 6: Commit**

```bash
git add cmd/serve/serve.go
git commit -m "feat: create AudioEngine at startup, pass to services via DI"
```

---

### Task 6: Replace scheduler globals

**Files:**
- Modify: `internal/analysis/audio_pipeline_service.go` — replace SetGlobalScheduler with engine.SetScheduler()
- Modify: `internal/api/v2/quiet_hours.go` — replace GetGlobalScheduler with engine.Scheduler()

- [ ] **Step 1: Read audio_pipeline_service.go for SetGlobalScheduler usage**

Find the call to `myaudio.SetGlobalScheduler()` (around line 199). Replace with `s.engine.SetScheduler(scheduler)`. The scheduler is still created in `AudioPipelineService.Start()` where `SunCalc` and `ControlChan` are available, but stored in the engine instead of the myaudio global.

- [ ] **Step 2: Read quiet_hours.go for GetGlobalScheduler usage**

Find the call to `myaudio.GetGlobalScheduler()` (around line 32). Replace with `c.engine.Scheduler()`. Add nil check since engine might be nil in tests.

- [ ] **Step 3: Update quiet_hours.go**

```go
// Before:
scheduler := myaudio.GetGlobalScheduler()

// After:
var scheduler *schedule.QuietHoursScheduler
if c.engine != nil {
    scheduler = c.engine.Scheduler()
}
```

Remove myaudio import if no longer used in this file.

- [ ] **Step 4: Run linter**

Run: `golangci-lint run ./internal/api/v2/... ./internal/analysis/...`
Expected: 0 issues

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/audio_pipeline_service.go internal/api/v2/quiet_hours.go
git commit -m "refactor: replace scheduler globals with engine.SetScheduler/Scheduler()"
```

---

### Task 7: Replace SetOnStreamReset

**Files:**
- Modify: `internal/analysis/audio_pipeline_service.go` — replace myaudio.SetOnStreamReset with engine

- [ ] **Step 1: Read the SetOnStreamReset call site**

Find `myaudio.SetOnStreamReset()` (around line 169). This registers a callback that fires when an FFmpeg stream resets.

- [ ] **Step 2: Replace with engine.FFmpegManager().SetOnStreamReset()**

```go
// Before:
myaudio.SetOnStreamReset(callback)

// After:
s.engine.FFmpegManager().SetOnStreamReset(callback)
```

Remove myaudio import if no longer used for this purpose (keep if other symbols still used).

- [ ] **Step 3: Run linter**

Run: `golangci-lint run ./internal/analysis/...`
Expected: 0 issues

- [ ] **Step 4: Commit**

```bash
git add internal/analysis/audio_pipeline_service.go
git commit -m "refactor: replace SetOnStreamReset with engine.FFmpegManager()"
```

---

### Task 8: Migrate StreamHealth types in streams_health

**Files:**
- Modify: `internal/api/v2/streams_health.go` — change myaudio types to ffmpeg types
- Modify: `internal/api/v2/streams_health_test.go` — update all test type references

This was deferred from PR 1 because the types have nested sub-types (`*ErrorContext`, `[]StateTransition`) from different packages. Now that we have the engine wired in, we can migrate these.

**Important:** `GetStreamHealth()` still returns myaudio types (PR 3 scope). For now, we need to either:
- (a) Create a thin conversion function `myaudioHealthToFFmpeg()`, OR
- (b) Leave GetStreamHealth on myaudio but convert the response types

Choose option (a) — a conversion function in streams_health.go.

- [ ] **Step 1: Read streams_health.go and understand the response conversion**

Read the file to understand how `myaudio.StreamHealth` is used in the response builders.

- [ ] **Step 2: Write a conversion function**

```go
// convertMyaudioHealth converts a myaudio.StreamHealth to ffmpeg.StreamHealth.
// This is a transitional helper removed in PR 3 when GetStreamHealth() moves to the engine.
func convertMyaudioHealth(mh *myaudio.StreamHealth) *ffmpeg.StreamHealth {
    fh := &ffmpeg.StreamHealth{
        IsHealthy:          mh.IsHealthy,
        LastDataReceived:   mh.LastDataReceived,
        RestartCount:       mh.RestartCount,
        Error:              mh.Error,
        TotalBytesReceived: mh.TotalBytesReceived,
        BytesPerSecond:     mh.BytesPerSecond,
        IsReceivingData:    mh.IsReceivingData,
        ProcessState:       ffmpeg.ProcessState(mh.ProcessState),
    }
    // Convert state history
    for _, st := range mh.StateHistory {
        fh.StateHistory = append(fh.StateHistory, ffmpeg.StateTransition{
            From:      ffmpeg.ProcessState(st.From),
            To:        ffmpeg.ProcessState(st.To),
            Timestamp: st.Timestamp,
            Reason:    st.Reason,
        })
    }
    // Convert error context
    if mh.LastErrorContext != nil {
        fh.LastErrorContext = convertErrorContext(mh.LastErrorContext)
    }
    for _, ec := range mh.ErrorHistory {
        fh.ErrorHistory = append(fh.ErrorHistory, convertErrorContext(ec))
    }
    return fh
}

func convertErrorContext(ec *myaudio.ErrorContext) *ffmpeg.ErrorContext {
    return &ffmpeg.ErrorContext{
        ErrorType:       ec.ErrorType,
        PrimaryMessage:  ec.PrimaryMessage,
        TargetHost:      ec.TargetHost,
        TargetPort:      ec.TargetPort,
        TimeoutDuration: ec.TimeoutDuration,
        HTTPStatus:      ec.HTTPStatus,
        RTSPMethod:      ec.RTSPMethod,
        RawFFmpegOutput: ec.RawFFmpegOutput,
        UserFacingMsg:   ec.UserFacingMsg,
        TroubleShooting: ec.TroubleShooting,
        Timestamp:       ec.Timestamp,
    }
}
```

- [ ] **Step 3: Update handler functions to use ffmpeg types**

Replace all `myaudio.StreamHealth` in function signatures and local variables with `ffmpeg.StreamHealth`. Call `convertMyaudioHealth()` at the boundary where `myaudio.GetStreamHealth()` returns data.

- [ ] **Step 4: Update streams_health_test.go**

Replace all ~60 occurrences of `myaudio.StreamHealth` → `ffmpeg.StreamHealth`, `myaudio.State*` → `ffmpeg.State*`, `myaudio.ErrorContext` → `ffmpeg.ErrorContext`, etc.

- [ ] **Step 5: Run tests**

Run: `go test -race -v ./internal/api/v2/ -run TestStreams`
Expected: PASS

- [ ] **Step 6: Run linter**

Run: `golangci-lint run ./internal/api/v2/...`
Expected: 0 issues

- [ ] **Step 7: Commit**

```bash
git add internal/api/v2/streams_health.go internal/api/v2/streams_health_test.go
git commit -m "refactor: migrate StreamHealth types from myaudio to audiocore/ffmpeg"
```

---

### Task 9: Audit and verify

- [ ] **Step 1: Check that SetGlobalScheduler and GetGlobalScheduler are no longer called**

```bash
grep -rn 'myaudio\.\(SetGlobalScheduler\|GetGlobalScheduler\|SetOnStreamReset\)' internal/ --include="*.go" | grep -v myaudio/ | grep -v .claude/
```
Expected: No matches

- [ ] **Step 2: Verify engine is properly wired**

```bash
grep -rn 'engine.*AudioEngine\|WithAudioEngine' internal/ --include="*.go" | grep -v .claude/
```
Expected: Matches in api.go, server.go, api_service.go, audio_pipeline_service.go, serve.go

- [ ] **Step 3: Run full test suite**

Run: `go test -race ./internal/api/v2/... ./internal/analysis/... ./internal/audiocore/...`
Expected: All pass

- [ ] **Step 4: Run full linter**

Run: `golangci-lint run ./internal/...`
Expected: 0 issues

- [ ] **Step 5: Commit any final fixes**

---

### Task 10: Push and create PR

- [ ] **Step 1: Push branch**

```bash
git push origin refactor/audiocore-wire-engine
```

- [ ] **Step 2: Create PR**

Target `next`, title: `refactor: wire AudioEngine into application via DI (Phase 2, PR 2/3)`

- [ ] **Step 3: Update Forgejo issue #57**

Post comment noting PR 2 is submitted.
