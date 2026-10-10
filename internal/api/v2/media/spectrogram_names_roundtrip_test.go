package media

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/spectrogram/specfile"
)

// TestSpectrogramNames_RoundTripThroughSpecfile builds render names with the real
// writers and checks that the filename grammar accepts them for their own clip and
// rejects them for another clip, so a change to a writer cannot silently strand
// renders from retention, the reconciler, serving or deletion.
func TestSpectrogramNames_RoundTripThroughSpecfile(t *testing.T) {
	t.Parallel()

	const (
		clip      = "2026/01/bubo_bubo_80p_20260102T030405Z.wav"
		clipBase  = "bubo_bubo_80p_20260102T030405Z"
		otherBase = "bubo_bubo_80p_20260102T030405Z_15s" // extended-capture sibling
		otherClip = "2026/01/" + otherBase + ".wav"
	)
	widths := []int{SpectrogramSizeSm, SpectrogramSizeMd, SpectrogramSizeLg, SpectrogramSizeXl, 800}
	styles := []string{"", conf.SpectrogramStyleDefault, conf.SpectrogramStyleScientificDark, conf.SpectrogramStyleHighContrastDark, conf.SpectrogramStyleScientific}
	ranges := []string{"", conf.SpectrogramDynamicRangeHighContrast, conf.SpectrogramDynamicRangeStandard, conf.SpectrogramDynamicRangeExtended}
	profiles := []string{"", spectrogram.ProfileSuffix(spectrogram.ProfileForModelType("bat"))}

	for _, width := range widths {
		for _, raw := range []bool{true, false} {
			for _, style := range styles {
				for _, dr := range ranges {
					for _, freq := range profiles {
						_, _, name, _ := buildSpectrogramPaths(clip, width, raw, style, dr, freq)

						render, ok := specfile.ParseRender(name, clipBase)
						require.Truef(t, ok, "%s must be a render of %s", name, clipBase)
						assert.Equal(t, width, render.Width, name)
						assert.Equal(t, raw, render.Raw, name)
						assert.False(t, render.Unsized, name)
						assert.Falsef(t, specfile.IsRenderFor(name, otherBase), "%s must not be a render of %s", name, otherBase)

						_, _, otherName, _ := buildSpectrogramPaths(otherClip, width, raw, style, dr, freq)
						assert.Falsef(t, specfile.IsRenderFor(otherName, clipBase), "%s must not be a render of %s", otherName, clipBase)
					}
				}
			}
		}
	}
}

func TestSpectrogramNames_PrerenderFileRoundTripsThroughSpecfile(t *testing.T) {
	t.Parallel()

	for _, clipPath := range []string{
		"2026/01/bubo_bubo_80p_20260102T030405Z.wav",
		"2026/01/bubo_bubo_80p_20260102T030405Z.m4a",
	} {
		pngPath, err := spectrogram.BuildSpectrogramPath(clipPath)
		require.NoError(t, err)
		clipBase := filepath.Base(clipPath[:len(clipPath)-len(filepath.Ext(clipPath))])

		render, ok := specfile.ParseRender(filepath.Base(pngPath), clipBase)
		require.True(t, ok, pngPath)
		assert.True(t, render.Unsized, pngPath)
		assert.False(t, specfile.IsRenderFor(filepath.Base(pngPath), "other_clip"), pngPath)
	}
}
