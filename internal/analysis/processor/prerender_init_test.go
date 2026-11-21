package processor

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// preRenderSpectrogramSize is a valid spectrogram size for the init tests.
const preRenderSpectrogramSize = "lg"

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

	require.NotNil(t, p.preRenderer)
	require.NotNil(t, p.preRendererSFS.Load())
	t.Cleanup(p.stopPreRenderer)
}

// TestStopPreRenderer_ClosesSecureFSOnce pins that the processor releases the
// export directory root it opened for the pre-renderer, and does so once even
// when stop runs again (a repeated Shutdown). Linux removes a directory that
// still has an open handle, so the TempDir cleanup alone cannot show a leak.
func TestStopPreRenderer_ClosesSecureFSOnce(t *testing.T) {
	p := &Processor{Settings: newPreRenderInitSettings(t)}
	p.initPreRenderer()
	require.NotNil(t, p.preRenderer)
	sfs := p.preRendererSFS.Load()
	require.NotNil(t, sfs)

	p.stopPreRenderer()

	assert.Nil(t, p.preRendererSFS.Load(), "the processor must drop its reference after closing the root")
	_, err := sfs.OpenRegularRel(preRenderProbeName)
	require.ErrorIs(t, err, os.ErrClosed, "the root must be closed after stop")
	assert.NotPanics(t, p.stopPreRenderer, "a second stop must be a no-op")
}

func TestStopPreRenderer_WithoutPreRenderer(t *testing.T) {
	p := &Processor{Settings: newPreRenderInitSettings(t)}

	assert.NotPanics(t, p.stopPreRenderer)
}
