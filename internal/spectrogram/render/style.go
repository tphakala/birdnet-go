package render

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// WindowKind selects the analysis window.
type WindowKind int

const (
	// WindowHann is the Hann window.
	WindowHann WindowKind = iota
	// WindowDolph is the Dolph-Chebyshev window sized to the display range.
	WindowDolph
)

// Style is the look of a rendered spectrogram: the analysis window and the
// palette. It is the seam for new themes.
type Style struct {
	// Window is the analysis window.
	Window WindowKind
	// Palette selects the colour palette.
	Palette soxchrome.PaletteSpec
}

// StyleFor maps a spectrogram style preset name to a Style. Unknown and empty
// names give the default style. The mapping mirrors the sox arguments each preset
// selects: scientific_dark is -m with a Dolph window, high_contrast_dark is -h,
// scientific is -m -l with a Dolph window.
func StyleFor(name string) Style {
	switch name {
	case conf.SpectrogramStyleScientificDark:
		return Style{Window: WindowDolph, Palette: soxchrome.PaletteSpec{Monochrome: true}}
	case conf.SpectrogramStyleHighContrastDark:
		return Style{Palette: soxchrome.PaletteSpec{HighColour: true}}
	case conf.SpectrogramStyleScientific:
		return Style{Window: WindowDolph, Palette: soxchrome.PaletteSpec{Monochrome: true, LightBackground: true}}
	default:
		return Style{}
	}
}
