package processor

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	audioBuffer "github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// logLineContaining returns the single captured log line carrying marker, so an
// assertion about a line's level or fields cannot be satisfied by a DIFFERENT
// line that happens to share the buffer. The export path emits several lines per
// clip, so a buffer-wide Contains is a much weaker claim than it appears.
func logLineContaining(t *testing.T, logs, marker string) string {
	t.Helper()
	var found string
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, marker) {
			require.Empty(t, found, "marker %q matched more than one line", marker)
			found = line
		}
	}
	require.NotEmpty(t, found, "no log line contains %q; captured:\n%s", marker, logs)
	return found
}

// A bat model at an ultrasonic capture rate: the combination needsBatFormatFallback
// downgrades to WAV, because no lossy container carries 256 kHz.
const (
	batModelName        = "BattyBirdNET"
	batSourceSampleRate = 256000
)

// captureExportLogs redirects the global logger to an in-memory buffer for the
// duration of the test and returns the buffer. The processor package logs
// through logger.Global().Module("analysis.processor"), so this captures
// everything the export path emits. It swaps process-wide global state, so
// tests using it must not run with t.Parallel().
func captureExportLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cl.Close()) })

	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	return &buf
}

// newExportLogAction builds a SaveAudioAction that Execute will carry all the
// way to an encoder: export enabled, a real output directory, and a second of
// PCM. Normalization is left off so the resolved gain is the static Export.Gain
// and the assertions stay deterministic.
func newExportLogAction(t *testing.T, exportDir, clipName, format string) *SaveAudioAction {
	t.Helper()
	return &SaveAudioAction{
		CorrelationID: "export-log-test",
		ClipName:      clipName,
		pcmData:       sinePCMBytes(8000, 1.0, 1000),
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Enabled: true,
						Path:    exportDir,
						Type:    format,
						Bitrate: "96k",
						Gain:    -2.5,
					},
				},
			},
		},
	}
}

// blockOutputPath makes the clip's destination unwritable by putting a
// directory where the file belongs. Every encoder on this path writes to a temp
// file and renames it into place, and a rename onto a directory always fails, so
// this forces a failure inside the encoder rather than before it.
func blockOutputPath(t *testing.T, exportDir, clipName string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(exportDir, clipName), 0o750))
}

// The success log is the primary support artefact for the clip export path. It
// must name the encoder that produced the file, so a support log answers "native
// or FFmpeg" without the reader having to reconstruct it from the format and the
// operator's environment.
func TestExecute_SuccessLogNamesNativeEncoder(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.flac", ffmpeg.FormatFLAC)

	require.NoError(t, a.Execute(t.Context(), nil))

	out := logs.String()
	assert.Contains(t, out, "Audio clip saved successfully")
	assert.Contains(t, out, "encoder="+encoderNativeFLAC,
		"the success log must name the encoder that wrote the clip")
	assert.Contains(t, out, "format="+ffmpeg.FormatFLAC)
	assert.Contains(t, out, "operation=audio_export_success")
}

// The success line is a data source, not just a support artefact:
// GET /api/v2/system/events/detections builds SpeciesEntry.ClipPaths by reading
// species and clip_path back off it (the audio_export_success case in
// internal/api/v2/system/events_aggregation.go). The species field went missing
// for the entire life of that endpoint, so every bucket it returned had an empty
// ClipPaths.
//
// The consumer-side test could not catch it: its fixture builds entries by hand
// and sets species on every one, so it only ever exercised input the production
// logger could not emit. This assertion is deliberately on the PRODUCER, against
// the field set really written to the log.
func TestExecute_SuccessLogNamesSpeciesForClipPathAggregation(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.flac", ffmpeg.FormatFLAC)
	// Lowercase, matching what buildSaveAudioAction stores and what the sibling
	// detection operations log; see the casing rationale on
	// TestBuildSaveAudioAction_PopulatesLowercasedSpeciesOnEveryBranch.
	a.species = "eurasian wren"

	require.NoError(t, a.Execute(t.Context(), nil))

	line := logLineContaining(t, logs.String(), "operation=audio_export_success")
	// Both halves of the pair the aggregator requires, on the SAME line; it drops
	// the entry unless species AND clip_path are non-empty.
	assert.Contains(t, line, `species="eurasian wren"`,
		"without species the events endpoint cannot attribute the clip to a bird")
	assert.Contains(t, line, "clip_path=clip.flac")
}

// The wiring test for the relative clip path. Every other Execute-level test
// uses a bare-basename ClipName, so for them Rel and Base return the same string
// and the assertions pass either way. Production ClipName is
// filepath.Join(year, month, filename), so only a nested name distinguishes the
// two, and only this test would fail if the field regressed to a basename and
// left SpeciesEntry.ClipPaths unresolvable.
func TestExecute_SuccessLogClipPathKeepsDateSegments(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "2026/07/wren.flac", ffmpeg.FormatFLAC)

	require.NoError(t, a.Execute(t.Context(), nil))

	line := logLineContaining(t, logs.String(), "operation=audio_export_success")
	assert.Contains(t, line, "clip_path=2026/07/wren.flac",
		"the date segments are what let a consumer resolve the clip back to a file")
	assert.NotContains(t, line, dir,
		"the export directory must never reach a support log")
}

// The half of the species contract the log assertion above cannot reach:
// buildSaveAudioAction must actually POPULATE the field, on every branch, in the
// casing the aggregator keys on.
//
// This test exists because the original defect was a missing ASSIGNMENT, not a
// missing log field. A test that sets a.species by hand and then checks the
// logger emits it stays green with all three assignments deleted, which is the
// same fabricated-fixture trap that let the bug through on the consumer side.
//
// The casing is load-bearing, not cosmetic. getOrCreateSpecies
// (internal/api/v2/system/events_aggregation.go) keys species with an exact map
// lookup, and the three sibling operations that create those entries
// (approve_detection, discard_detection, flush_detection) all log
// strings.ToLower(CommonName) via processor.go's speciesName. Emitting the
// original case here would file the clip paths under a second, phantom species
// row with zero counts, leaving the real row's ClipPaths empty: the exact defect
// this work set out to fix, one layer down.
func TestBuildSaveAudioAction_PopulatesLowercasedSpeciesOnEveryBranch(t *testing.T) {
	const commonName = "Eurasian Wren"
	const wantSpecies = "eurasian wren"
	const sourceID = "test-source"

	newDetection := func(begin, end time.Time) *Detections {
		return &Detections{
			CorrelationID: "build-species-test",
			Result: detection.Result{
				Species:     detection.Species{CommonName: commonName},
				ClipName:    "2026/07/clip.wav",
				BeginTime:   begin,
				EndTime:     end,
				AudioSource: detection.AudioSource{ID: sourceID, SampleRate: conf.SampleRate},
			},
		}
	}

	tests := []struct {
		name      string
		build     func(t *testing.T) (*Processor, *Detections)
		wantEager bool
	}{
		{
			// Extended Capture: the tail is still being written, so the action
			// carries buffer coordinates and reads later.
			name: "deferred read",
			build: func(t *testing.T) (*Processor, *Detections) {
				t.Helper()
				mgr := audioBuffer.NewManager(GetLogger())
				require.NoError(t, mgr.AllocateCapture(sourceID, 10, conf.SampleRate, conf.BitDepth/8))
				now := time.Now()
				return &Processor{
						Settings:  buildSpeciesTestSettings(t),
						BufferMgr: mgr,
					},
					newDetection(now, now.Add(30*time.Second))
			},
		},
		{
			// The capture buffer cannot be read, so the action is built as a
			// no-op. It still reaches the export logger via Execute.
			name: "capture read error",
			build: func(t *testing.T) (*Processor, *Detections) {
				t.Helper()
				now := time.Now()
				return &Processor{
						Settings:  buildSpeciesTestSettings(t),
						BufferMgr: audioBuffer.NewManager(GetLogger()), // no buffer allocated
					},
					newDetection(now.Add(-10*time.Second), now.Add(-8*time.Second))
			},
		},
		{
			// The ordinary path, and the one most exports take: PCM is read
			// eagerly at build time.
			//
			// Reaching it needs the requested window to be entirely in the PAST
			// (otherwise buildSaveAudioAction defers) while still being readable
			// from the ring. A freshly written buffer cannot satisfy both: its
			// startTime is the moment of the first Write, so any window inside it
			// ends in the future. Overfilling the ring forces a wrap, and on wrap
			// the buffer re-anchors startTime to now-bufferDuration
			// (buffer/capture.go), which puts the whole readable window behind the
			// clock.
			name: "eager read",
			build: func(t *testing.T) (*Processor, *Detections) {
				t.Helper()
				const bufferSeconds = 3
				mgr := audioBuffer.NewManager(GetLogger())
				require.NoError(t, mgr.AllocateCapture(sourceID, bufferSeconds, conf.SampleRate, conf.BitDepth/8))
				cb, err := mgr.CaptureBuffer(sourceID)
				require.NoError(t, err)
				// Twice the ring, so it wraps and re-anchors startTime.
				require.NoError(t, cb.Write(make([]byte, 2*bufferSeconds*conf.SampleRate*(conf.BitDepth/8))))
				start := cb.StartTime()
				require.False(t, start.Add(2*time.Second).After(time.Now()),
					"the requested window must be in the past or this row silently retests the deferred branch")
				return &Processor{
						Settings:  buildSpeciesTestSettings(t),
						BufferMgr: mgr,
					},
					newDetection(start, start.Add(2*time.Second))
			},
			// Distinguishes this branch from the other two: only the eager path
			// pre-reads PCM and leaves bufferMgr unset.
			wantEager: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, det := tt.build(t)

			action := p.buildSaveAudioAction(det, &DetectionContext{})

			require.NotNil(t, action)
			// Pin WHICH branch ran. Without this the eager row silently fell back
			// to the deferred branch and the eager return site, the one most
			// production exports take, was never executed at all.
			if tt.wantEager {
				require.NotEmpty(t, action.pcmData, "the eager branch pre-reads the PCM")
				require.Nil(t, action.bufferMgr, "the eager branch does not defer")
			} else {
				require.Empty(t, action.pcmData, "only the eager branch pre-reads the PCM")
			}
			assert.Equal(t, wantSpecies, action.species,
				"every construction branch must carry the species, lowercased to match approve_detection")
		})
	}
}

// buildSpeciesTestSettings is the minimal settings object buildSaveAudioAction
// needs: an enabled export with a real directory.
func buildSpeciesTestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	return conftest.NewTestSettings().
		WithAudioExport(t.TempDir(), "wav", "192k").
		Build()
}

// The troubleshooting fields alongside the encoder: the applied loudness gain
// ("my clips are too quiet") and the clip duration next to the file size ("my
// clips are cut off"). Both used to be reachable only at Debug, or not at all.
func TestExecute_SuccessLogCarriesGainAndDuration(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.wav", ffmpeg.FormatWAV)

	require.NoError(t, a.Execute(t.Context(), nil))

	out := logs.String()
	assert.Contains(t, out, "gain_db=-2.5", "the resolved gain must be visible per clip")
	// By value, not key-only: the fixture is exactly one second of PCM at
	// conf.SampleRate, so a rewiring that dropped the real duration (and logged
	// a zero, or the PCM length at the wrong rate) would otherwise pass.
	assert.Contains(t, out, "duration_ms=1000", "clip duration must accompany the file size")
	assert.Contains(t, out, "file_size_bytes=")
	// Encoding and loudness measurement are reported separately: with
	// normalization on, measurement is a full-clip EBU R128 pass that can dwarf
	// the encode, and one combined figure blames the codec for it.
	//
	// measure_ms is pinned BY VALUE. This fixture leaves normalization off, so
	// resolving the gain is a struct read and must cost 0 ms. Asserting only that
	// the key exists would still pass if both fields were fed by one timer, which
	// is the exact regression the split exists to prevent.
	assert.Contains(t, out, "measure_ms=0",
		"with normalization off there is nothing to measure; a non-zero value means the encode was folded in")
	assert.Contains(t, out, "encode_ms=")
	// WAV is lossless, so there is no bitrate to report and the field is omitted
	// rather than logged as a misleading zero.
	assert.NotContains(t, out, "bitrate_kbps=")
}

// A lossy format reports the bitrate the encode used. 96k is within every
// codec's range, so this covers the ordinary case only; the clamping the field
// also has to survive is the test below.
func TestExecute_SuccessLogCarriesBitrateForLossyFormats(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")
	resetNativeSkipOnce()
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.m4a", ffmpeg.FormatAAC)

	require.NoError(t, a.Execute(t.Context(), nil))

	out := logs.String()
	assert.Contains(t, out, "encoder="+encoderNativeAAC)
	assert.Contains(t, out, "bitrate_kbps=96")
}

// The logged bitrate is the EFFECTIVE one, not the configured one: it exists to
// explain the size of the file on disk, so a setting the codec refused would be
// actively misleading. Opus caps at 256 kbps, so a 320k configuration is the
// case that tells the two apart.
func TestExecute_SuccessLogCarriesClampedBitrate(t *testing.T) {
	t.Setenv(conf.EnvNativeOpusEncoder, "native")
	resetNativeSkipOnce()
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.opus", ffmpeg.FormatOpus)
	a.Settings.Realtime.Audio.Export.Bitrate = "320k"

	require.NoError(t, a.Execute(t.Context(), nil))

	out := logs.String()
	assert.Contains(t, out, "encoder="+encoderNativeOpus)
	assert.Contains(t, out, "bitrate_kbps=256",
		"the clamped bitrate explains the file on disk; the configured 320k does not")
	assert.NotContains(t, out, "bitrate_kbps=320")
}

// The gap this change closes: a failed export used to leave nothing but the job
// queue's own line ("Job failed permanently", or "Job scheduled for retry after
// failure" on the attempts before it), which names neither the encoder nor the
// format. An operator who opted AAC into the native encoder could not tell
// whether go-aac or FFmpeg produced the failure.
func TestExecute_FailureLogNamesNativeEncoder(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")
	resetNativeSkipOnce()
	logs := captureExportLogs(t)
	dir := t.TempDir()
	blockOutputPath(t, dir, "clip.m4a")
	a := newExportLogAction(t, dir, "clip.m4a", ffmpeg.FormatAAC)

	require.Error(t, a.Execute(t.Context(), nil))

	out := logs.String()
	assert.Contains(t, out, "Audio clip export failed")
	assert.Contains(t, out, "encoder="+encoderNativeAAC,
		"a failed export must be attributable to the encoder that failed")
	assert.Contains(t, out, "format="+ffmpeg.FormatAAC)
	assert.Contains(t, out, "operation=audio_export_failed")
	assert.Contains(t, out, "detection_id=export-log-test")
	// A rejected bitrate is one of the ways a lossy encode fails, so the value
	// that was going to be used must not be a second question.
	assert.Contains(t, out, "bitrate_kbps=96")
}

// The failure line is WARN, not ERROR: the error is returned unchanged, so the
// job queue logs the same root cause as ERROR right afterwards. Two ERROR lines
// for one failure would double-count and double-alert.
func TestExecute_FailureLogIsWarnNotError(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.mp3", ffmpeg.FormatMP3)
	a.Settings.Realtime.Audio.FfmpegPath = ""

	require.Error(t, a.Execute(t.Context(), nil))

	out := logs.String()
	require.Contains(t, out, "Audio clip export failed")
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, "Audio clip export failed") {
			assert.Contains(t, line, "level=WARN",
				"the job queue owns the ERROR for this failure; this line only adds context")
		}
	}
}

// The other half of the pair: the same failure on the FFmpeg path is attributed
// to FFmpeg, so the two are distinguishable in a support log.
func TestExecute_FailureLogNamesFFmpeg(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.mp3", ffmpeg.FormatMP3)
	// No FFmpeg binary configured, so the export fails inside ffmpeg.ExportAudio.
	a.Settings.Realtime.Audio.FfmpegPath = ""

	require.Error(t, a.Execute(t.Context(), nil))

	out := logs.String()
	assert.Contains(t, out, "Audio clip export failed")
	assert.Contains(t, out, "encoder="+encoderFFmpeg)
	assert.Contains(t, out, "format="+ffmpeg.FormatMP3)
}

// encodeClip documents that it populates clipEncoding "as far as the export
// got", and the pre-gain failure is the branch where that matters: the encoder
// is known but the gain is not. gain_db used to be emitted unconditionally, so
// this case logged gain_db=0, which is also the default Export.Gain and reads as
// a measured value rather than an absence.
//
// Reaching it needs normalization on (so gain resolution does real work) and a
// context already cancelled (so planNativeNormalizationGain refuses before
// measuring), which is the shape a shutdown mid-measurement produces.
func TestExecute_FailureBeforeGainResolutionOmitsGain(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.flac", ffmpeg.FormatFLAC)
	a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
		Enabled:    true,
		TargetLUFS: -23,
		TruePeak:   -2,
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.Error(t, a.Execute(ctx, nil))

	// Scoped to the failure line: planNativeNormalizationGain legitimately logs
	// its own gain_db on a Debug line, so a buffer-wide NotContains would break
	// for the wrong reason the moment the failure point moved past measurement.
	line := logLineContaining(t, logs.String(), "operation=audio_export_failed")
	assert.Contains(t, line, "encoder="+encoderNativeFLAC,
		"the encoder is known before the gain is, so it must still be reported")
	assert.NotContains(t, line, "gain_db=",
		"an export that failed before resolving the gain has none to report")
	assert.NotContains(t, line, "encode_ms=",
		"the encoder never ran, so encode_ms=0 would read as an instant rejection")
	assert.Contains(t, line, "measure_ms=",
		"measurement is what failed, so its duration is still meaningful")
}

// The other half of the timing contract: a failure that got as far as the
// encoder reports how long each phase ran. That is what separates a hung encoder
// which burned the whole job deadline from one that rejected its arguments
// instantly, and both numbers were already measured before this change; only the
// success line consumed them.
func TestExecute_FailureLogCarriesPhaseTimings(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.flac", ffmpeg.FormatFLAC)
	blockOutputPath(t, dir, "clip.flac")

	require.Error(t, a.Execute(t.Context(), nil))

	line := logLineContaining(t, logs.String(), "operation=audio_export_failed")
	assert.Contains(t, line, "measure_ms=0", "normalization is off in this fixture")
	assert.Contains(t, line, "encode_ms=",
		"the encoder ran and failed, so its duration is known and worth reporting")
	assert.Contains(t, line, "gain_db=-2.5",
		"the gain resolved before the encoder failed, so it is still reportable")
}

// clip_path is the clip's name relative to the export directory: the identifier
// the media endpoint resolves, and what GET /api/v2/system/events/detections
// hands back as SpeciesEntry.ClipPaths. The year/month segments are load-bearing
// (a bare basename cannot be resolved back to a file) and the export directory
// must never appear (support dumps are read by someone other than the operator).
//
// The escape cases matter because the value goes into a support artefact. If the
// output ever lands outside the export directory, filepath.Rel does NOT fail; it
// walks up with "..", which would both leak the layout above the export
// directory and produce an unresolvable name. The basename is the safe answer.
func TestRelativeClipPath(t *testing.T) {
	t.Parallel()

	const exportDir = "/srv/birdnet/clips"
	tests := []struct {
		name       string
		exportPath string
		outputPath string
		want       string
	}{
		{"nested clip keeps its date segments", exportDir, exportDir + "/2026/07/wren.wav", "2026/07/wren.wav"},
		{"trailing separator on the export path", exportDir + "/", exportDir + "/2026/07/wren.wav", "2026/07/wren.wav"},
		{"clip directly in the export root", exportDir, exportDir + "/wren.wav", "wren.wav"},
		{"escaping path falls back to the basename", exportDir, "/etc/passwd", "passwd"},
		{"sibling directory falls back to the basename", exportDir, "/srv/birdnet/other/wren.wav", "wren.wav"},
		{"unrelatable paths fall back to the basename", "relative/dir", "/absolute/wren.wav", "wren.wav"},
		{"empty export path falls back to the basename", "", exportDir + "/wren.wav", "wren.wav"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &SaveAudioAction{Settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{Export: conf.ExportSettings{Path: tt.exportPath}},
				},
			}}

			got := a.relativeClipPath(tt.outputPath)

			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "..", "a support log must not carry the layout above the export directory")
			assert.False(t, filepath.IsAbs(got), "a support log must not carry an absolute path")
		})
	}
}

// The failure log must not leak the export directory. Only the clip's basename
// is logged, matching the success line, because support logs and uploaded
// support dumps are read by someone other than the operator.
func TestExecute_FailureLogOmitsFullPath(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.mp3", ffmpeg.FormatMP3)
	a.Settings.Realtime.Audio.FfmpegPath = ""

	require.Error(t, a.Execute(t.Context(), nil))

	assert.Contains(t, logs.String(), "clip_path=clip.mp3")
	assert.NotContains(t, logs.String(), "clip_path="+dir)
}

// A cancelled export is shutdown, not a defect: an export in flight when the
// process stops must not be recorded as an error.
//
// FLAC, because it is a path that genuinely reports cancellation: flac.EncodePCM
// checks ctx before it writes and returns ctx.Err(). The classification reads
// that error, so the test has to produce one rather than merely arranging for
// the context to be cancelled (see the sibling test below, which is the case
// this one used to be testing by accident).
func TestExecute_CancelledExportLogsAtDebug(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.flac", ffmpeg.FormatFLAC)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := a.Execute(ctx, nil)
	require.ErrorIs(t, err, context.Canceled, "the export must fail with a real cancellation")

	out := logs.String()
	assert.Contains(t, out, "Audio clip export cancelled")
	assert.NotContains(t, out, "Audio clip export failed")
}

// The regression this pair exists to prevent: a genuine defect that merely
// COINCIDES with shutdown is still a defect. The guard used to test the context
// as well as the error, so ENOSPC, corrupt PCM or a missing FFmpeg binary hit
// while the process was stopping were all logged at Debug under a message
// asserting they had been cancelled. On a container that OOM-restarts that
// misattributes every in-flight export failure in the restart window.
func TestExecute_RealErrorDuringShutdownStaysAtWarn(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.mp3", ffmpeg.FormatMP3)
	// Not a cancellation: FFmpeg is simply not there.
	a.Settings.Realtime.Audio.FfmpegPath = ""

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := a.Execute(ctx, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled,
		"the failure under test must be a real error, not a cancellation")

	out := logs.String()
	assert.Contains(t, out, "Audio clip export failed",
		"a real failure must not be filed as shutdown just because the context was cancelled")
	assert.NotContains(t, out, "Audio clip export cancelled")
}

// The other side of that classification: an EXPIRED context is not shutdown. The
// job queue wraps every execution in a timeout, so a hung encoder blows the
// deadline, and that is exactly the failure this line exists to surface. It must
// not be filed under "cancelled" at Debug.
func TestExecute_TimedOutExportStaysAtWarn(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.mp3", ffmpeg.FormatMP3)
	a.Settings.Realtime.Audio.FfmpegPath = ""

	// An already-expired deadline, which is what the job queue's execution
	// timeout leaves on the context when an encode runs long.
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "the test context must be expired, not cancelled")

	require.Error(t, a.Execute(ctx, nil))

	out := logs.String()
	assert.Contains(t, out, "Audio clip export failed",
		"a timeout is a real failure and must not be filed as shutdown")
	assert.NotContains(t, out, "Audio clip export cancelled")
}

// An ultrasonic capture whose configured container cannot carry the sample rate
// is exported as WAV. That downgrade used to be entirely silent, leaving an
// operator configured for a lossy format with no explanation for the .wav files
// on disk.
func TestResolveExportParams_BatDowngradeIsLogged(t *testing.T) {
	resetBatFormatDowngradeOnce()
	logs := captureExportLogs(t)
	a := newExportLogAction(t, t.TempDir(), "clip.mp3", ffmpeg.FormatMP3)
	a.modelName = batModelName
	a.sourceSampleRate = batSourceSampleRate

	_, format, path := a.resolveExportParams("/clips/clip.mp3")

	require.Equal(t, ffmpeg.FormatWAV, format, "the downgrade itself must still happen")
	assert.Equal(t, "/clips/clip.wav", path)
	assert.Contains(t, logs.String(), "operation=audio_export_bat_format_fallback")
	assert.Contains(t, logs.String(), "requested_format="+ffmpeg.FormatMP3)
}

// The downgrade fires on every single detection of a bat install, so repeating
// the SAME condition must not repeat the log. (The companion test below covers
// the other half: a DIFFERENT condition still gets its own line.)
func TestResolveExportParams_BatDowngradeLoggedOncePerCondition(t *testing.T) {
	resetBatFormatDowngradeOnce()
	logs := captureExportLogs(t)
	a := newExportLogAction(t, t.TempDir(), "clip.mp3", ffmpeg.FormatMP3)
	a.modelName = batModelName
	a.sourceSampleRate = batSourceSampleRate

	for range 3 {
		_, _, _ = a.resolveExportParams("/clips/clip.mp3")
	}

	assert.Equal(t, 1, strings.Count(logs.String(), "audio_export_bat_format_fallback"),
		"a bat install takes this path on every detection; the log must not repeat")
}

// The guard is keyed on the inputs the decision is made from, not on "have we
// ever logged this". Both inputs vary at runtime: Export.Type is hot-reloadable,
// and the capture rate belongs to the source, so a multi-source install runs
// several at once. A bare sync.Once let the first condition silence every later,
// DIFFERENT one, leaving the operator an explanation naming a format they had
// since changed away from or a rate belonging to another source.
func TestResolveExportParams_BatDowngradeLogsEachDistinctCondition(t *testing.T) {
	resetBatFormatDowngradeOnce()
	logs := captureExportLogs(t)
	a := newExportLogAction(t, t.TempDir(), "clip.mp3", ffmpeg.FormatMP3)
	a.modelName = batModelName
	a.sourceSampleRate = batSourceSampleRate

	_, _, _ = a.resolveExportParams("/clips/clip.mp3")

	// A second source at a different ultrasonic rate: same format, new condition.
	const otherBatRate = 192000
	a.sourceSampleRate = otherBatRate
	_, _, _ = a.resolveExportParams("/clips/clip.mp3")

	// The operator switches the export format in the UI without restarting.
	a.Settings.Realtime.Audio.Export.Type = ffmpeg.FormatOpus
	a.sourceSampleRate = batSourceSampleRate
	_, _, _ = a.resolveExportParams("/clips/clip.opus")

	out := logs.String()
	assert.Equal(t, 3, strings.Count(out, "audio_export_bat_format_fallback"),
		"each distinct format/rate combination must explain itself exactly once")
	assert.Contains(t, out, "sample_rate="+strconv.Itoa(otherBatRate),
		"the second source's rate must not be hidden by the first")
	assert.Contains(t, out, "requested_format="+ffmpeg.FormatOpus,
		"a hot-reloaded format must re-explain itself")
}

// The sibling downgrade: an install with no FFmpeg whose opted-in native encoder
// cannot carry the clip either. It means the same thing as the bat downgrade
// ("you are not getting the format you configured"), so it is logged the same
// way. It used to be the opposite of its sibling on both counts: WARN on every
// single clip forever, against Info exactly once.
func TestResolveExportParams_StrandedFallbackIsWarnedOncePerCondition(t *testing.T) {
	t.Setenv(conf.EnvNativeOpusEncoder, "native")
	resetNativeSkipOnce()
	resetStrandedFormatOnce()
	logs := captureExportLogs(t)

	// 44.1 kHz is a rate Opus does not accept, and with no FFmpeg to fall back
	// on there is no encoder left for the configured format.
	const unsupportedOpusRate = 44100
	a := newExportLogAction(t, t.TempDir(), "clip.opus", ffmpeg.FormatOpus)
	a.Settings.Realtime.Audio.FfmpegPath = ""
	a.sourceSampleRate = unsupportedOpusRate

	for range 3 {
		_, format, _ := a.resolveExportParams("/clips/clip.opus")
		require.Equal(t, ffmpeg.FormatWAV, format, "the recording must still survive as WAV")
	}

	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, "audio_export_no_encoder_fallback"),
		"the condition is static per clip shape; it must not warn on every detection")
	// Scoped to the fallback line itself. A buffer-wide "level=WARN" is already
	// satisfied by the native-encoder skip warning this same path emits, so it
	// would pass even if the fallback were still logged at Info.
	assert.Contains(t, logLineContaining(t, out, "audio_export_no_encoder_fallback"), "level=WARN",
		"it means the same thing to the operator as the bat downgrade, so it takes the same level")
}

// The stranded guard is keyed like its sibling, so a second distinct condition
// still explains itself. Without this, a bare sync.Once would pass the
// once-per-condition test above identically.
func TestResolveExportParams_StrandedFallbackLogsEachDistinctCondition(t *testing.T) {
	t.Setenv(conf.EnvNativeOpusEncoder, "native")
	t.Setenv(conf.EnvNativeAACEncoder, "native")
	resetNativeSkipOnce()
	resetStrandedFormatOnce()
	logs := captureExportLogs(t)

	a := newExportLogAction(t, t.TempDir(), "clip.opus", ffmpeg.FormatOpus)
	a.Settings.Realtime.Audio.FfmpegPath = ""
	a.sourceSampleRate = 44100
	_, _, _ = a.resolveExportParams("/clips/clip.opus")

	// A different format the native encoder also rejects at this rate.
	a.Settings.Realtime.Audio.Export.Type = ffmpeg.FormatAAC
	a.sourceSampleRate = 22050
	_, _, _ = a.resolveExportParams("/clips/clip.m4a")

	out := logs.String()
	assert.Equal(t, 2, strings.Count(out, "audio_export_no_encoder_fallback"),
		"each distinct format/rate combination must explain itself exactly once")
	// The two native-encoder skip warnings share one guard now that the format is
	// part of the key; both formats must still get their own line. Asserted by
	// counting the operation, not by Contains("format=aac"), which is also a
	// substring of the stranded line's own requested_format=aac and would pass
	// even if these warnings were never emitted.
	assert.Equal(t, 2, strings.Count(out, "audio_export_native_unsupported"),
		"one shared guard must not let the first format silence the second")
}

// selectEncoder is the whole routing table, extracted so a failed export can
// name its encoder. The gated formats are covered from both sides: with the gate
// off, AAC and Opus must still resolve to FFmpeg, which is what every install
// that has not opted in does on every clip. Asserting nativeAACSelected directly
// (as the gate tests do) checks the predicate but not the routing built on it.
func TestSelectEncoder_RoutingTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		aacGate    string
		opusGate   string
		format     string
		rate       int
		wantEncode string
	}{
		{"wav is always native", "", "", ffmpeg.FormatWAV, conf.SampleRate, encoderNativeWAV},
		{"flac is always native", "", "", ffmpeg.FormatFLAC, conf.SampleRate, encoderNativeFLAC},
		{"wav ignores the gates", "native", "native", ffmpeg.FormatWAV, conf.SampleRate, encoderNativeWAV},
		{"flac ignores the gates", "native", "native", ffmpeg.FormatFLAC, conf.SampleRate, encoderNativeFLAC},
		{"aac without the gate stays on ffmpeg", "", "", ffmpeg.FormatAAC, conf.SampleRate, encoderFFmpeg},
		{"opus without the gate stays on ffmpeg", "", "", ffmpeg.FormatOpus, conf.SampleRate, encoderFFmpeg},
		{"aac with the gate goes native", "native", "", ffmpeg.FormatAAC, conf.SampleRate, encoderNativeAAC},
		{"opus with the gate goes native", "", "native", ffmpeg.FormatOpus, conf.SampleRate, encoderNativeOpus},
		// Gated on, but the encoder cannot carry the clip's rate: FFmpeg takes it
		// rather than the export failing outright.
		{"aac falls back when the rate is unsupported", "native", "", ffmpeg.FormatAAC, 22050, encoderFFmpeg},
		{"opus falls back when the rate is unsupported", "", "native", ffmpeg.FormatOpus, 22050, encoderFFmpeg},
		// Neither gate touches the formats FFmpeg owns outright.
		{"mp3 is always ffmpeg", "native", "native", ffmpeg.FormatMP3, conf.SampleRate, encoderFFmpeg},
		{"alac is always ffmpeg", "native", "native", ffmpeg.FormatALAC, conf.SampleRate, encoderFFmpeg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: t.Setenv, and the skip warning uses a package-level Once.
			t.Setenv(conf.EnvNativeAACEncoder, tc.aacGate)
			t.Setenv(conf.EnvNativeOpusEncoder, tc.opusGate)
			resetNativeSkipOnce()

			assert.Equal(t, tc.wantEncode, selectEncoder(tc.format, tc.rate))
		})
	}
}

// The bitrate reported alongside the encoder is the effective one, and the
// lossless formats report none at all rather than a misleading zero.
func TestLossyBitrateKbps(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 96, lossyBitrateKbps(ffmpeg.FormatMP3, "96k"))
	assert.Equal(t, 96, lossyBitrateKbps(ffmpeg.FormatAAC, "96k"))
	assert.Equal(t, 96, lossyBitrateKbps(ffmpeg.FormatOpus, "96k"))

	assert.Equal(t, 0, lossyBitrateKbps(ffmpeg.FormatWAV, "96k"), "WAV is lossless")
	assert.Equal(t, 0, lossyBitrateKbps(ffmpeg.FormatFLAC, "96k"), "FLAC is lossless")
	assert.Equal(t, 0, lossyBitrateKbps(ffmpeg.FormatALAC, "96k"), "ALAC is lossless")

	// A malformed setting parses to 0, which the log then omits rather than
	// reporting a bitrate the encoder never used.
	assert.Equal(t, 0, lossyBitrateKbps(ffmpeg.FormatMP3, "not-a-bitrate"))

	// MP3 and Opus are clamped to what the codec accepts, so the logged value
	// tracks the encode rather than echoing an over-large setting back.
	assert.Positive(t, lossyBitrateKbps(ffmpeg.FormatMP3, "9999k"))
	assert.Less(t, lossyBitrateKbps(ffmpeg.FormatMP3, "9999k"), 9999)
}

func TestClipDurationMs(t *testing.T) {
	t.Parallel()

	bytesPerFrame := (conf.BitDepth / 8) * conf.NumChannels

	assert.Equal(t, int64(1000), clipDurationMs(bytesPerFrame*conf.SampleRate, conf.SampleRate))
	assert.Equal(t, int64(500), clipDurationMs(bytesPerFrame*conf.SampleRate/2, conf.SampleRate))
	assert.Equal(t, int64(0), clipDurationMs(0, conf.SampleRate))
	assert.Equal(t, int64(0), clipDurationMs(bytesPerFrame*100, 0),
		"an unknown sample rate must not divide by zero")
}
