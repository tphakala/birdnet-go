# Audiocore Phase 2 — PR 1: Type Migrations

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move types, constants, and pure functions from `internal/myaudio` to their `internal/audiocore` equivalents across all consumers. No behavioral change.

**Architecture:** Import swaps for symbols that already exist identically in audiocore. Create `AudioLevelData` and `StreamTypeToSourceType()` in audiocore where they don't yet exist. Also migrate `SourceType` constant usage and pure file-utility functions (`GetAudioDuration`, `GetAudioInfo`, `GetTotalChunks`) that have identical signatures in audiocore. Functions with different signatures (ExportAudio, ValidateAudioFile, etc.) stay on myaudio imports — those are PR 3 scope. No symbols are removed from myaudio — the legacy package must remain functional for its own pipeline code until PR 3 deletes it.

**Tech Stack:** Go 1.26, testify, golangci-lint

**Spec:** `docs/superpowers/specs/2026-03-23-audiocore-phase2-migration-design.md`

---

## Pre-flight

Before starting, read these files:
- `internal/CLAUDE.md` — Go coding standards
- `TESTING.md` — Test patterns (testify required)
- `internal/audiocore/ffmpeg/stream.go` lines 95-258 — ProcessState, StateTransition, StreamHealth definitions
- `internal/audiocore/ffmpeg/error_context.go` lines 72-88 — ErrorContext definition
- `internal/audiocore/source.go` — SourceType constants, SourceRegistry
- `internal/audiocore/soundlevel/types.go` — OctaveBandData, SoundLevelData definitions

## File Structure

### New Files
- `internal/audiocore/audio_level.go` — AudioLevelData type definition

### Modified Files (Types/Constants Migration)
- `internal/api/server.go` — AudioLevelData import
- `internal/api/v2/api.go` — AudioLevelData import
- `internal/api/v2/audio_level.go` — AudioLevelData + StreamTypeToSourceType imports
- `internal/api/v2/sse.go` — SoundLevelData import (myaudio → soundlevel)
- `internal/api/v2/streams_health.go` — StreamHealth import (myaudio → ffmpeg)
- `internal/api/v2/streams_health_test.go` — StreamHealth, ProcessState, StateTransition, ErrorContext imports
- `internal/analysis/sound_level.go` — StreamTypeToSourceType import
- `internal/analysis/soundlevel_convert.go` — OctaveBandData, SoundLevelData already using soundlevel
- `internal/telemetry/telemetry_test.go` — ProcessState import if used

### NOT Modified (deferred to PR 2/3)
- Files using `myaudio.GetRegistry()`, `myaudio.GetStreamHealth()`, etc. (global singletons → PR 2)
- Files using `myaudio.CaptureAudio()`, `myaudio.ExportAudioWithFFmpeg()`, etc. (pipeline functions → PR 3)

---

### Task 1: Create AudioLevelData in audiocore

**Files:**
- Create: `internal/audiocore/audio_level.go`
- Test: `internal/audiocore/audio_level_test.go`

- [ ] **Step 1: Read the myaudio AudioLevelData definition**

Read `internal/myaudio/capture.go` around line 143 to understand the exact struct fields and JSON tags.

- [ ] **Step 2: Create the audiocore AudioLevelData type**

Create `internal/audiocore/audio_level.go`:

```go
package audiocore

// AudioLevelData represents real-time audio level information for a source.
// Used as the channel type for streaming audio levels to API consumers.
type AudioLevelData struct {
	Level    int    `json:"level"`    // 0-100 normalized level
	Clipping bool   `json:"clipping"` // true if clipping is detected
	Source   string `json:"source"`   // Source identifier
	Name     string `json:"name"`     // Human-readable name
}
```

- [ ] **Step 3: Write a basic test**

Create `internal/audiocore/audio_level_test.go`:

```go
package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAudioLevelData_Fields(t *testing.T) {
	t.Parallel()
	data := AudioLevelData{
		Level:    75,
		Clipping: true,
		Source:   "rtsp://cam1",
		Name:     "Front Yard",
	}
	assert.Equal(t, 75, data.Level)
	assert.True(t, data.Clipping)
	assert.Equal(t, "rtsp://cam1", data.Source)
	assert.Equal(t, "Front Yard", data.Name)
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race -v ./internal/audiocore/ -run TestAudioLevelData`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/audiocore/audio_level.go internal/audiocore/audio_level_test.go
git commit -m "feat: add AudioLevelData type to audiocore package"
```

---

### Task 2: Add StreamTypeToSourceType to audiocore

**Files:**
- Modify: `internal/audiocore/source.go`
- Test: `internal/audiocore/source_test.go` (may exist)

- [ ] **Step 1: Read the myaudio implementation**

Read `internal/myaudio/source_registry.go` around line 1093 to see the exact switch/map logic.

- [ ] **Step 2: Add the function to audiocore/source.go**

```go
// StreamTypeToSourceType converts a stream type string (e.g., "rtsp", "http")
// to the corresponding SourceType constant.
func StreamTypeToSourceType(streamType string) SourceType {
	switch strings.ToLower(streamType) {
	case "rtsp":
		return SourceTypeRTSP
	case "http":
		return SourceTypeHTTP
	case "hls":
		return SourceTypeHLS
	case "rtmp":
		return SourceTypeRTMP
	case "udp":
		return SourceTypeUDP
	default:
		return SourceTypeUnknown
	}
}
```

Ensure `"strings"` is imported.

- [ ] **Step 3: Write tests in source_test.go**

If `internal/audiocore/source_test.go` doesn't exist, create it with `package audiocore` and required imports (`testing`, `github.com/stretchr/testify/assert`). If it exists, add the test to it.

```go
func TestStreamTypeToSourceType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected SourceType
	}{
		{"rtsp", SourceTypeRTSP},
		{"RTSP", SourceTypeRTSP},
		{"http", SourceTypeHTTP},
		{"hls", SourceTypeHLS},
		{"rtmp", SourceTypeRTMP},
		{"udp", SourceTypeUDP},
		{"unknown", SourceTypeUnknown},
		{"", SourceTypeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, StreamTypeToSourceType(tt.input))
		})
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race -v ./internal/audiocore/ -run TestStreamTypeToSourceType`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/audiocore/source.go internal/audiocore/source_test.go
git commit -m "feat: add StreamTypeToSourceType function to audiocore"
```

---

### Task 3: Migrate API server AudioLevelData imports

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/v2/api.go`
- Modify: `internal/api/v2/audio_level.go`

- [ ] **Step 1: Read each file to find exact myaudio.AudioLevelData usage**

Read all three files, search for `myaudio.AudioLevelData`. Note exact line numbers and usages.

- [ ] **Step 2: Update imports in server.go**

Replace `myaudio.AudioLevelData` with `audiocore.AudioLevelData`. Update the import from `"github.com/tphakala/birdnet-go/internal/myaudio"` to `"github.com/tphakala/birdnet-go/internal/audiocore"`. If myaudio is still used for other symbols in the same file, keep both imports.

- [ ] **Step 3: Update imports in api.go**

Same pattern: replace `myaudio.AudioLevelData` with `audiocore.AudioLevelData`.

- [ ] **Step 4: Update imports in audio_level.go**

Replace `myaudio.AudioLevelData` with `audiocore.AudioLevelData`. Also replace `myaudio.StreamTypeToSourceType` with `audiocore.StreamTypeToSourceType` and `myaudio.SourceType*` constants with `audiocore.SourceType*`. Keep the myaudio import if the file still uses other myaudio symbols (like `GetRegistry()`).

- [ ] **Step 5: Run linter to verify**

Run: `golangci-lint run ./internal/api/...`
Expected: 0 issues

- [ ] **Step 6: Run tests**

Run: `go test -race -v ./internal/api/v2/ -run TestAudioLevel`
Expected: PASS (or skip if tests require full server)

- [ ] **Step 7: Commit**

```bash
git add internal/api/server.go internal/api/v2/api.go internal/api/v2/audio_level.go
git commit -m "refactor: migrate AudioLevelData imports from myaudio to audiocore"
```

---

### Task 4: Migrate SSE SoundLevelData import

**Files:**
- Modify: `internal/api/v2/sse.go`

- [ ] **Step 1: Read sse.go to find myaudio.SoundLevelData usage**

The file embeds `myaudio.SoundLevelData` in a struct. This should become `soundlevel.SoundLevelData`.

- [ ] **Step 2: Update the import and type reference**

Replace `myaudio.SoundLevelData` with `soundlevel.SoundLevelData`. Add import for `"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"`. Remove myaudio import if no longer needed.

- [ ] **Step 3: Run linter**

Run: `golangci-lint run ./internal/api/v2/...`
Expected: 0 issues

- [ ] **Step 4: Commit**

```bash
git add internal/api/v2/sse.go
git commit -m "refactor: migrate SoundLevelData import from myaudio to audiocore/soundlevel"
```

---

### Task 5: Migrate streams_health types

**Files:**
- Modify: `internal/api/v2/streams_health.go`
- Modify: `internal/api/v2/streams_health_test.go`

- [ ] **Step 1: Read both files for myaudio type usage**

Find all uses of `myaudio.StreamHealth`, `myaudio.ProcessState`, `myaudio.StateTransition`, `myaudio.ErrorContext`, and `myaudio.State*` constants.

- [ ] **Step 2: Update streams_health.go**

Replace type references from `myaudio.X` to `ffmpeg.X`. Add import `"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"`. Keep myaudio import if file still uses `myaudio.GetStreamHealth()` (PR 2 scope).

- [ ] **Step 3: Update streams_health_test.go**

This file has 60+ occurrences. Replace all `myaudio.StreamHealth` → `ffmpeg.StreamHealth`, `myaudio.StateRunning` → `ffmpeg.StateRunning`, `myaudio.ErrorContext` → `ffmpeg.ErrorContext`, etc. Remove myaudio import if no longer needed.

- [ ] **Step 4: Run tests**

Run: `go test -race -v ./internal/api/v2/ -run TestStreams`
Expected: PASS

- [ ] **Step 5: Run linter**

Run: `golangci-lint run ./internal/api/v2/...`
Expected: 0 issues

- [ ] **Step 6: Commit**

```bash
git add internal/api/v2/streams_health.go internal/api/v2/streams_health_test.go
git commit -m "refactor: migrate StreamHealth/ProcessState types from myaudio to audiocore/ffmpeg"
```

---

### Task 6: Migrate analysis StreamTypeToSourceType usage

**Files:**
- Modify: `internal/analysis/sound_level.go`

- [ ] **Step 1: Read sound_level.go for myaudio.StreamTypeToSourceType usage**

Find exact lines using `myaudio.StreamTypeToSourceType()`.

- [ ] **Step 2: Replace with audiocore.StreamTypeToSourceType**

Update import to include `"github.com/tphakala/birdnet-go/internal/audiocore"`. Replace calls. Keep myaudio import if file still uses other myaudio symbols.

- [ ] **Step 3: Run linter**

Run: `golangci-lint run ./internal/analysis/...`
Expected: 0 issues

- [ ] **Step 4: Commit**

```bash
git add internal/analysis/sound_level.go
git commit -m "refactor: migrate StreamTypeToSourceType from myaudio to audiocore"
```

---

### Task 7: Migrate file utility function imports

**Files:**
- Modify: `internal/api/v2/media.go` (if it uses `myaudio.GetAudioDuration`)
- Modify: any files using `myaudio.GetAudioInfo`, `myaudio.GetTotalChunks`

These functions already exist with identical signatures in audiocore:
- `myaudio.GetAudioDuration()` → `ffmpeg.GetAudioDuration()` (audiocore/ffmpeg/common.go)
- `myaudio.GetAudioInfo()` → `readfile.GetAudioInfo()` (audiocore/readfile/reader.go)
- `myaudio.GetTotalChunks()` → `readfile.GetTotalChunks()` (audiocore/readfile/reader.go)

Note: Functions with DIFFERENT signatures stay on myaudio: `ExportAudioWithFFmpeg()`, `ValidateAudioFile()`, `EncodePCMtoWAVWithContext()`, `AnalyzeAudioLoudnessWithContext()`, `ReadAudioFileBuffered()`, `ListAudioSources()`, `HasCaptureBuffer()`, `ReadSegmentFromCaptureBuffer()`. These are PR 3 scope.

- [ ] **Step 1: Find all external usages of these functions**

```bash
grep -rn 'myaudio\.\(GetAudioDuration\|GetAudioInfo\|GetTotalChunks\)' internal/ --include="*.go" | grep -v myaudio/ | grep -v .claude/
```

- [ ] **Step 2: Replace imports at each call site**

For each file found, replace `myaudio.GetAudioDuration` with `ffmpeg.GetAudioDuration` (import `audiocore/ffmpeg`), or `myaudio.GetAudioInfo` with `readfile.GetAudioInfo` (import `audiocore/readfile`), etc.

- [ ] **Step 3: Run linter**

Run: `golangci-lint run ./internal/...`
Expected: 0 issues

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: migrate file utility functions from myaudio to audiocore"
```

---

### Task 8: Audit remaining myaudio imports and exported constants

**Files:**
- Various — depends on audit results

**Important:** Do NOT remove any symbols from `myaudio`. The legacy package must remain functional for its own pipeline code until PR 3 deletes it. If constants are used externally, create duplicates/aliases in audiocore rather than removing from myaudio.

- [ ] **Step 1: Audit remaining myaudio imports**

Run: `grep -rn '"github.com/tphakala/birdnet-go/internal/myaudio"' internal/ --include="*.go" | grep -v myaudio/ | grep -v .claude/`

Categorize remaining imports into:
- **PR 2 scope:** Files using `GetRegistry()`, `GetStreamHealth()`, `GetGlobalScheduler()`, `SetOnStreamReset()`
- **PR 3 scope:** Files using `CaptureAudio()`, `ExportAudioWithFFmpeg()`, `InitAnalysisBuffers()`, etc.
- **Missed PR 1 work:** Any remaining type-only imports that could be migrated now

- [ ] **Step 2: Audit exported constants**

Run: `grep -rn 'myaudio\.\(Format\|Err\|Min\|Max\|Float32\|Temp\)' internal/ --include="*.go" | grep -v myaudio/ | grep -v .claude/`

If any constants are used externally, duplicate them in the appropriate audiocore package. Do NOT remove from myaudio.

- [ ] **Step 3: Fix any missed migrations**

If the audit finds additional type-only imports that should have been migrated, fix them now.

- [ ] **Step 4: Run full linter**

Run: `golangci-lint run ./internal/...`
Expected: 0 issues (or only pre-existing issues)

- [ ] **Step 5: Run full test suite**

Run: `go test -race ./internal/...`
Expected: All pass (except known embed issue in worktrees)

- [ ] **Step 6: Final commit**

```bash
git add -A
git commit -m "refactor: complete type migration audit and fix remaining imports"
```

---

### Task 9: Push and create PR

- [ ] **Step 1: Push branch**

```bash
git push origin refactor/audiocore-type-migrations
```

- [ ] **Step 2: Create PR**

```bash
gh pr create --base next --head refactor/audiocore-type-migrations \
  --title "refactor: migrate types and constants from myaudio to audiocore (Phase 2, PR 1/3)" \
  --body "## Summary
- Move AudioLevelData, StreamHealth, ProcessState, StateTransition, ErrorContext, SoundLevelData, SourceType to audiocore equivalents
- Add StreamTypeToSourceType() function to audiocore
- Pure import swaps — no behavioral change

Part 1 of 3 for Phase 2 migration (#57).

## Test plan
- All existing tests pass (import swaps only)
- New tests for AudioLevelData and StreamTypeToSourceType
- golangci-lint clean"
```

- [ ] **Step 3: Update Forgejo issue #57**

Post comment on issue #57 noting PR 1 is submitted for review.
