package processor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

// preRenderSpectrogramSize is a valid spectrogram size for the init tests.
const preRenderSpectrogramSize = "lg"

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
	pr, ok := p.preRenderer.(*spectrogram.PreRenderer)
	require.True(t, ok)
	t.Cleanup(pr.Stop)
}
