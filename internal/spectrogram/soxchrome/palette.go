// Derived from SoX src/spectrogram.c, (c) 2008-9 robs@users.sourceforge.net,
// licensed under the GNU Lesser General Public License version 2.1 or later.
// Ported via github.com/tphakala/go-spectrogram ebf0c64 sox/palette.go. Changes
// made in October 2026: only the default, high colour (-h), monochrome (-m) and
// monochrome light background (-m -l) branches are kept, the permutation is fixed
// at its default, and the colour mapping works on float32 dB values.

package soxchrome

import (
	"image/color"
	"math"
)

// Fixed palette indices, as in SoX.
const (
	// IndexBackground is the image background.
	IndexBackground uint8 = iota
	// IndexText is axis titles and the legend title.
	IndexText
	// IndexLabels is tick labels.
	IndexLabels
	// IndexGrid is the frame and tick marks.
	IndexGrid
	// IndexFirstSpectrum is the first of the spectrum entries.
	IndexFirstSpectrum
)

const (
	// spectrumPoints is the number of spectrum entries: SoX's default of 249
	// quantisation levels plus the two end points (spectrogram.c:199, :261).
	spectrumPoints = 249 + 2

	// paletteSize is the number of palette entries, fixed plus spectrum.
	paletteSize = int(IndexFirstSpectrum) + spectrumPoints

	// colourLevels is the number of levels between the floor and full scale
	// entries, spectrum_points - 2 in spectrogram.c:580.
	colourLevels = spectrumPoints - 2

	// topIndex is the palette index of full scale and above.
	topIndex = int(IndexFirstSpectrum) + spectrumPoints - 1
)

// PaletteSpec selects one of the palettes the renderer supports.
type PaletteSpec struct {
	// Monochrome is SoX -m: grey levels. HighColour is ignored with it.
	Monochrome bool
	// HighColour is SoX -h: the high saturation palette.
	HighColour bool
	// LightBackground is SoX -l: a light background with inverted levels.
	LightBackground bool
}

// ColourIndex maps a level in dB relative to full scale to a palette index. A
// level below -rangeDB maps to the first spectrum entry, a level at or above 0
// to the last, and NaN to the first.
func ColourIndex(dB float32, rangeDB float64) uint8 {
	return colourIndex(float64(dB), rangeDB)
}

// colourIndex is ColourIndex for a double precision level, which the legend bar
// needs to match SoX at the quantisation boundaries.
func colourIndex(x, rangeDB float64) uint8 {
	if x < -rangeDB {
		return IndexFirstSpectrum
	}
	if x >= 0 {
		return uint8(topIndex)
	}
	c := 1 + (1+x/rangeDB)*colourLevels
	if !(c > 0) { // also true for NaN
		return IndexFirstSpectrum
	}
	return IndexFirstSpectrum + uint8(c)
}

// Palette returns the 255 entry palette for spec: the four fixed entries, then
// the spectrum.
func Palette(spec PaletteSpec) color.Palette {
	pal := make(color.Palette, paletteSize)
	var (
		black = color.RGBA{0x00, 0x00, 0x00, 0xff}
		dgrey = color.RGBA{0x3f, 0x3f, 0x3f, 0xff}
		mgrey = color.RGBA{0x7f, 0x7f, 0x7f, 0xff}
		lgrey = color.RGBA{0xbf, 0xbf, 0xbf, 0xff}
		white = color.RGBA{0xff, 0xff, 0xff, 0xff}
		lbgnd = color.RGBA{0xdd, 0xd8, 0xd0, 0xff}
		mbgnd = color.RGBA{0xdf, 0xdf, 0xdf, 0xff}
	)
	if spec.LightBackground {
		bg := lbgnd
		if spec.Monochrome {
			bg = mbgnd
		}
		pal[IndexBackground], pal[IndexText], pal[IndexLabels], pal[IndexGrid] = bg, black, dgrey, dgrey
	} else {
		pal[IndexBackground], pal[IndexText], pal[IndexLabels], pal[IndexGrid] = black, white, lgrey, mgrey
	}

	for i := range spectrumPoints {
		x := float64(i) / float64(spectrumPoints-1)
		at := i
		if spec.LightBackground {
			at = spectrumPoints - 1 - i
		}
		var c [3]float64
		switch {
		case spec.Monochrome:
			c = [3]float64{x, x, x}
		case spec.HighColour:
			c = highColour(x)
		default:
			c = defaultColour(x)
		}
		pal[int(IndexFirstSpectrum)+at] = color.RGBA{toByte(0.5 + 255*c[0]), toByte(0.5 + 255*c[1]), toByte(0.5 + 255*c[2]), 0xff}
	}
	return pal
}

// Colour curve breakpoints of the default palette (spectrogram.c:631-642).
const (
	redStart, redFull        = 0.13, 0.73
	greenStart, greenFull    = 0.60, 0.91
	blueRiseEnd, blueRiseTop = 0.60, 0.78
	blueRiseGain             = 0.5
)

// defaultColour is the SoX default spectrum at position x in [0, 1].
func defaultColour(x float64) [3]float64 {
	var c [3]float64
	switch {
	case x < redStart:
		c[0] = 0
	case x < redFull:
		c[0] = math.Sin((x - redStart) / (redFull - redStart) * math.Pi / 2)
	default:
		c[0] = 1
	}
	switch {
	case x < greenStart:
		c[1] = 0
	case x < greenFull:
		c[1] = math.Sin((x - greenStart) / (greenFull - greenStart) * math.Pi / 2)
	default:
		c[1] = 1
	}
	switch {
	case x < blueRiseEnd:
		c[2] = blueRiseGain * math.Sin(x/blueRiseEnd*math.Pi)
	case x < blueRiseTop:
		c[2] = 0
	default:
		c[2] = (x - blueRiseTop) / (1 - blueRiseTop)
	}
	return c
}

// highColourPhases is the number of phases of the high colour palette.
const highColourPhases = 7

// Curve shapes of one channel in one phase of the high colour palette
// (spectrogram.c:621-625).
const (
	curveZero    = iota // constant 0
	curveOne            // constant 1
	curveSinRise        // rises as sin
	curveCosFall        // falls as cos
	curveRise           // rises linearly
	curveFall           // falls linearly
)

// highColourStates gives, per colour channel and phase, the curve used.
var highColourStates = [3][highColourPhases]int{
	{curveRise, curveFall, curveZero, curveZero, curveSinRise, curveOne, curveOne},
	{curveZero, curveZero, curveSinRise, curveOne, curveOne, curveCosFall, curveSinRise},
	{curveRise, curveOne, curveOne, curveCosFall, curveZero, curveZero, curveSinRise},
}

// highColour is the SoX -h spectrum at position x in [0, 1].
func highColour(x float64) [3]float64 {
	phase := min(int(highColourPhases*x), highColourPhases-1)
	frac := highColourPhases*x - float64(phase)
	var c [3]float64
	for j := range c {
		switch highColourStates[j][phase] {
		case curveZero:
			c[j] = 0
		case curveOne:
			c[j] = 1
		case curveSinRise:
			c[j] = math.Sin(frac * math.Pi / 2)
		case curveCosFall:
			c[j] = math.Cos(frac * math.Pi / 2)
		case curveRise:
			c[j] = frac
		case curveFall:
			c[j] = 1 - frac
		}
	}
	return c
}

// toByte truncates like C's assignment to png_byte; inputs lie in [0, 256).
func toByte(v float64) uint8 { return uint8(v) }
