package processor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// preRenderSpectrogramSize is a valid spectrogram size for the init tests.
const preRenderSpectrogramSize = "lg"

// preRenderShutdownTimeout bounds ShutdownWithContext in the shutdown test.
const preRenderShutdownTimeout = 5 * time.Second

// preRenderProbeName is a file name that does not exist in the export
// directory; opening it only shows whether the SecureFS root is still open.
const preRenderProbeName = "probe.png"

// newPreRenderInitSettings returns settings that pass every initPreRenderer
// check; no external binary is configured.
func newPreRenderInitSettings(t *testing.T) *conf.Settings {
	t.Helper()
	s := conftest.GetTestSettings()
	s.Realtime.Audio.Export.Path = t.TempDir()
	s.Realtime.Dashboard.Spectrogram.Size = preRenderSpectrogramSize
	return s
}

func TestInitPreRenderer_NeedsNoSoxBinary(t *testing.T) {
	t.Setenv("PATH", "")
	p := &Processor{Settings: newPreRenderInitSettings(t)}

	p.initPreRenderer()
	t.Cleanup(p.stopPreRenderer)

	require.NotNil(t, p.preRenderer)
	require.NotNil(t, p.preRendererSFS.Load())
}

// requireSecureFSClosed fails the test unless the root behind sfs is closed.
// The probe file does not exist, so an open root reports "not exist" and only
// a closed one reports os.ErrClosed. Linux removes a directory that still has
// an open handle, so the TempDir cleanup alone cannot show a leak.
func requireSecureFSClosed(t *testing.T, sfs *securefs.SecureFS) {
	t.Helper()
	_, err := sfs.OpenRegularRel(preRenderProbeName)
	require.ErrorIs(t, err, os.ErrClosed, "the root must be closed")
}

// TestStopPreRenderer_ClosesSecureFS pins that stopping the pre-renderer closes
// the export directory root the processor opened for it and drops the
// processor's reference, and that a second stop does not panic.
func TestStopPreRenderer_ClosesSecureFS(t *testing.T) {
	p := &Processor{Settings: newPreRenderInitSettings(t)}
	p.initPreRenderer()
	t.Cleanup(p.stopPreRenderer)
	require.NotNil(t, p.preRenderer)
	sfs := p.preRendererSFS.Load()
	require.NotNil(t, sfs)

	p.stopPreRenderer()

	assert.Nil(t, p.preRendererSFS.Load(), "the processor must drop its reference after closing the root")
	requireSecureFSClosed(t, sfs)
	assert.NotPanics(t, p.stopPreRenderer, "a second stop must be a no-op")
}

// TestShutdownWithContext_ClosesPreRendererSecureFS pins that a processor
// shutdown releases the export directory root held for the pre-renderer.
func TestShutdownWithContext_ClosesPreRendererSecureFS(t *testing.T) {
	p := &Processor{
		Settings: newPreRenderInitSettings(t),
		JobQueue: jobqueue.NewJobQueue(),
	}
	p.initPreRenderer()
	t.Cleanup(p.stopPreRenderer)
	require.NotNil(t, p.preRenderer)
	sfs := p.preRendererSFS.Load()
	require.NotNil(t, sfs)

	ctx, cancel := context.WithTimeout(t.Context(), preRenderShutdownTimeout)
	defer cancel()
	require.NoError(t, p.ShutdownWithContext(ctx))

	requireSecureFSClosed(t, sfs)
}
