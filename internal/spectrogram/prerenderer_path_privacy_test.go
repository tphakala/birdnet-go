package spectrogram

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// capturePreRenderLogs points one PreRenderer at an in-memory buffer. Scoped to
// the instance rather than swapping the global logger, so these tests stay
// isolated from the package's other tests.
func capturePreRenderLogs(t *testing.T, pr *PreRenderer) *bytes.Buffer {
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

	pr.logger = cl.Module("spectrogram.test")
	return &buf
}

// TestExportRelPathStripsExportDirectory pins the privacy contract of
// exportRelPath: nothing it returns may carry the export directory, because
// every path-valued log field and error context in this package goes through
// it, and support logs plus uploaded support dumps are read by someone other
// than the operator. An absolute path there leaks the account name and the
// directory layout of the reporting machine.
func TestExportRelPathStripsExportDirectory(t *testing.T) {
	t.Parallel()

	pr, env := createTestPreRenderer(t, nil)
	exportDir := env.TempDir

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clip under the export directory keeps year/month",
			input: filepath.Join(exportDir, "2026", "07", "turdus_merula_82p.wav"),
			want:  "2026/07/turdus_merula_82p.wav",
		},
		{
			name:  "spectrogram under the export directory keeps year/month",
			input: filepath.Join(exportDir, "2026", "07", "turdus_merula_82p.png"),
			want:  "2026/07/turdus_merula_82p.png",
		},
		{
			name:  "file directly in the export directory is its own name",
			input: filepath.Join(exportDir, "clip.wav"),
			want:  "clip.wav",
		},
		{
			name: "path outside the export directory degrades to the basename " +
				"rather than a .. walk that would leak the layout above it",
			input: "/var/lib/somebodyelse/clips/2026/07/clip.wav",
			want:  "clip.wav",
		},
		{
			name:  "empty stays empty rather than becoming the current directory",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := exportRelPath(env.Settings, pr.sfs, tt.input)
			assert.Equal(t, tt.want, got)
			assert.False(t, filepath.IsAbs(got), "result must never be absolute")
			assert.NotContains(t, got, "..", "result must never walk up out of the export directory")
			if tt.input != "" {
				assert.NotContains(t, got, exportDir, "result must not carry the export directory")
			}
		})
	}
}

// TestExportRelPathHandlesRelativeExportPath covers the configuration where
// Export.Path is relative (e.g. "clips") while SecureFS resolved it to an
// absolute base at construction. The clip path is joined against the former, so
// the fallback to the latter is what makes the value strippable at all.
func TestExportRelPathHandlesRelativeExportPath(t *testing.T) {
	t.Parallel()

	pr, env := createTestPreRenderer(t, nil)

	// SecureFS keeps the absolute base captured at construction; point the
	// hot-reloadable setting at a relative directory the way a config file
	// written as `path: clips` would.
	env.Settings.Realtime.Audio.Export.Path = "clips"

	// A path joined against the relative setting resolves on the first root.
	assert.Equal(t, "2026/07/clip.wav", exportRelPath(env.Settings, pr.sfs, filepath.Join("clips", "2026", "07", "clip.wav")))

	// A path joined against the absolute SecureFS base resolves on the second,
	// and must still come back stripped rather than absolute.
	got := exportRelPath(env.Settings, pr.sfs, filepath.Join(env.TempDir, "2026", "07", "clip.wav"))
	assert.Equal(t, "2026/07/clip.wav", got)
	assert.False(t, filepath.IsAbs(got))
}

// TestBuildSpectrogramPathErrorOmitsDirectory guards the Sentry side of the
// same contract: this error is reported upstream, so its context must name the
// offending file without the directory it sits in.
//
// The assertions read the error's Context map, not err.Error(). The message is
// a fixed string that never carried a path, so asserting on it would pass just
// as well before the change as after, and would not discriminate a revert.
func TestBuildSpectrogramPathErrorOmitsDirectory(t *testing.T) {
	t.Parallel()

	_, err := BuildSpectrogramPath("/home/someuser/birdnet/clips/2026/07/extensionless")
	require.Error(t, err)

	var enhanced *errors.EnhancedError
	require.ErrorAs(t, err, &enhanced, "the error must carry a reportable context")

	assert.Equal(t, "extensionless", enhanced.Context["clip_basename"],
		"the context must still identify the offending file by name")
	for key, value := range enhanced.Context {
		text, ok := value.(string)
		if !ok {
			continue
		}
		assert.NotContains(t, text, "someuser",
			"context key %q must not carry the account name of the reporting machine", key)
		assert.NotContains(t, text, "/home/",
			"context key %q must not carry the directory layout", key)
	}
}

// TestBuildSpectrogramPathWithParamsErrorOmitsDirectory covers the sibling that
// builds the same class of error from the same kind of input. It was left
// leaking the full path when its twin was fixed, which is the asymmetry that
// makes a one-site privacy fix worth checking for.
func TestBuildSpectrogramPathWithParamsErrorOmitsDirectory(t *testing.T) {
	t.Parallel()

	_, err := BuildSpectrogramPathWithParams("/home/someuser/birdnet/clips/2026/07/extensionless", sizeLargePx, false)
	require.Error(t, err)

	var enhanced *errors.EnhancedError
	require.ErrorAs(t, err, &enhanced)

	assert.Equal(t, "extensionless", enhanced.Context["clip_basename"])
	for key, value := range enhanced.Context {
		if text, ok := value.(string); ok {
			assert.NotContains(t, text, "someuser",
				"context key %q must not carry the account name", key)
		}
	}
}

// TestSubmitRejectionLogsNoAbsolutePath drives a real call site rather than the
// helper in isolation. The privacy contract lives at the call sites, not in the
// helper: with only a unit test on the helper, reverting any single site back to
// the raw path leaves the whole suite green.
//
// Scope, stated honestly: this covers the Submit rejection branch. The Generator
// sites cannot be reached from here, because Generator logs through the
// process-global GetLogger() rather than an injected logger, so capturing them
// would need a global swap. They are covered by construction instead: every one
// is an inline argument to a log field, verified by grep at review time.
func TestSubmitRejectionLogsNoAbsolutePath(t *testing.T) {
	pr, env := createTestPreRenderer(t, nil)
	logs := capturePreRenderLogs(t, pr)

	// No extension, so BuildSpectrogramPath rejects the job and takes the
	// logging branch under test.
	outside := filepath.Join(env.TempDir, "2026", "07", "extensionless")
	err := pr.Submit(&Job{ClipPath: outside, NoteID: 42})
	require.Error(t, err)

	out := logs.String()
	require.NotEmpty(t, out, "the rejection must have been logged")
	assert.NotContains(t, out, env.TempDir,
		"the export directory must not reach the log from a call site")
	assert.Contains(t, out, "2026/07/extensionless",
		"the clip must still be identifiable by its export-relative name")
}
