package render

import (
	"image/color"
	"sync"

	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// palettes holds one built palette per spec. A palette is 255 entries of
// trigonometry and boxed colours, and only a handful of specs exist, so it is
// built once. The result is shared and must be treated as read-only; image
// and PNG encoding only read it.
//
//nolint:gochecknoglobals // process-wide cache of immutable palettes
var palettes sync.Map // soxchrome.PaletteSpec -> color.Palette

// paletteFor returns the shared palette for spec.
func paletteFor(spec soxchrome.PaletteSpec) color.Palette {
	if pal, ok := palettes.Load(spec); ok {
		return pal.(color.Palette) //nolint:forcetypeassert // only paletteFor stores into palettes
	}
	pal, _ := palettes.LoadOrStore(spec, soxchrome.Palette(spec))
	return pal.(color.Palette) //nolint:forcetypeassert // only paletteFor stores into palettes
}
