package processor

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

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
	t.Cleanup(func() { _ = cl.Close() })

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
	assert.Contains(t, out, "duration_ms=", "clip duration must accompany the file size")
	assert.Contains(t, out, "encode_ms=")
	assert.Contains(t, out, "file_size_bytes=")
	// WAV is lossless, so there is no bitrate to report and the field is omitted
	// rather than logged as a misleading zero.
	assert.NotContains(t, out, "bitrate_kbps=")
}

// A lossy format reports the bitrate the encoder actually used, which is the
// clamped effective value rather than the raw setting.
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

// The gap this change closes: a failed export used to leave nothing but the job
// queue's generic "Job failed" line, which names neither the encoder nor the
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

// A cancelled context is shutdown, not a defect: an export in flight when the
// process stops must not be recorded as an error.
func TestExecute_CancelledExportLogsAtDebug(t *testing.T) {
	logs := captureExportLogs(t)
	dir := t.TempDir()
	a := newExportLogAction(t, dir, "clip.mp3", ffmpeg.FormatMP3)
	a.Settings.Realtime.Audio.FfmpegPath = ""

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.Error(t, a.Execute(ctx, nil))

	out := logs.String()
	assert.Contains(t, out, "Audio clip export cancelled")
	assert.NotContains(t, out, "Audio clip export failed")
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

// The downgrade fires on every single detection of a bat install, so it is
// logged once per process rather than once per clip.
func TestResolveExportParams_BatDowngradeLoggedOncePerProcess(t *testing.T) {
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

func TestClipDurationMs(t *testing.T) {
	t.Parallel()

	bytesPerFrame := (conf.BitDepth / 8) * conf.NumChannels

	assert.Equal(t, int64(1000), clipDurationMs(bytesPerFrame*conf.SampleRate, conf.SampleRate))
	assert.Equal(t, int64(500), clipDurationMs(bytesPerFrame*conf.SampleRate/2, conf.SampleRate))
	assert.Equal(t, int64(0), clipDurationMs(0, conf.SampleRate))
	assert.Equal(t, int64(0), clipDurationMs(bytesPerFrame*100, 0),
		"an unknown sample rate must not divide by zero")
}
