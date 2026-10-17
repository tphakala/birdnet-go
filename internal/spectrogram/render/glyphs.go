package render

import (
	"fmt"
	"image"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// Glyph rasterization parameters. Go Mono at 9.5 px has a 6 pixel advance and
// glyphs that fit five columns, which reproduces sox's 5 pixel glyph plus one
// pixel gap in a 6 by 12 cell.
const (
	glyphFontSize       = 9.5
	glyphDPI            = 72
	glyphAlphaThreshold = 0x78

	// firstGlyph and lastGlyph bound the printable ASCII range the atlas holds.
	firstGlyph = 0x20
	lastGlyph  = 0x7e
	glyphCount = lastGlyph - firstGlyph + 1

	// fallbackGlyph draws for runes outside the atlas range.
	fallbackGlyph = '?'

	// subpixelShift converts a 26.6 fixed point value to whole pixels.
	subpixelShift = 6
)

// chromeRunes lists every character the axis text prints for the supported
// sample rates (8 kHz to 1 MHz) and clip lengths: the titles, digits, signs, the
// decimal point, the SI prefixes k and m, "DC" and the exponent letter. The other
// SI prefixes cannot occur on these axes; if one did, it would draw clipped to
// the cell.
const chromeRunes = " 0123456789+-.()DCTimesFrquncykHzdBS"

// glyphBits is one character as a bitmap of CellH rows; bit CellW-1-c of a row is
// column c.
type glyphBits [soxchrome.CellH]uint8

// glyphAtlas holds the rasterized characters. It is immutable after construction,
// so renders share it without locking.
type glyphAtlas struct {
	glyphs [glyphCount]glyphBits
}

// ErrGlyphFit marks a font that does not fit the sox text cell.
var ErrGlyphFit = errors.NewStd("font does not fit the text cell")

// atlas builds the shared glyph atlas once.
//
//nolint:gochecknoglobals // immutable lazily built glyph atlas shared by all renders
var atlas = sync.OnceValues(buildAtlas)

// glyph returns the bitmap for r, or the fallback glyph outside the atlas range.
func (a *glyphAtlas) glyph(r byte) *glyphBits {
	if r < firstGlyph || r > lastGlyph {
		r = fallbackGlyph
	}
	return &a.glyphs[r-firstGlyph]
}

// inkPixel is one thresholded pixel relative to the glyph origin on the baseline.
type inkPixel struct{ x, y int }

// buildAtlas rasterizes Go Mono into the sox cell. It returns an error wrapping
// ErrGlyphFit when the face advance is not CellW, or when a chrome character's ink
// does not fit CellW-1 columns and CellH rows.
func buildAtlas() (*glyphAtlas, error) {
	ttf, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(ttf, &opentype.FaceOptions{Size: glyphFontSize, DPI: glyphDPI, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer func() { _ = face.Close() }()

	var ink [glyphCount][]inkPixel
	minY, maxY := 0, 0
	first := true
	for r := rune(firstGlyph); r <= lastGlyph; r++ {
		adv, ok := face.GlyphAdvance(r)
		if !ok || int(adv>>subpixelShift) != soxchrome.CellW {
			return nil, fmt.Errorf("%w: advance of %q is not %d", ErrGlyphFit, r, soxchrome.CellW)
		}
		dr, mask, maskp, _, ok := face.Glyph(fixed.Point26_6{}, r)
		if !ok {
			continue
		}
		pixels := thresholdMask(dr, mask, maskp)
		ink[r-firstGlyph] = pixels
		if !isChromeRune(r) {
			continue
		}
		for _, p := range pixels {
			if p.x < 0 || p.x > soxchrome.CellW-2 {
				return nil, fmt.Errorf("%w: ink of %q spans column %d outside 0..%d", ErrGlyphFit, r, p.x, soxchrome.CellW-2)
			}
			if first {
				minY, maxY, first = p.y, p.y, false
			}
			minY, maxY = min(minY, p.y), max(maxY, p.y)
		}
	}
	inkH := maxY - minY + 1
	if first || inkH > soxchrome.CellH {
		return nil, fmt.Errorf("%w: ink height %d exceeds %d rows", ErrGlyphFit, inkH, soxchrome.CellH)
	}
	// Centre the union ink box in the cell rows, as sox's labels sit on their ticks.
	baseline := (soxchrome.CellH-inkH+1)/2 - minY

	a := &glyphAtlas{}
	for i := range ink {
		pixels := ink[i]
		for _, p := range pixels {
			row := baseline + p.y
			if p.x < 0 || p.x >= soxchrome.CellW || row < 0 || row >= soxchrome.CellH {
				continue
			}
			a.glyphs[i][row] |= 1 << (soxchrome.CellW - 1 - p.x)
		}
	}
	return a, nil
}

// thresholdMask returns the pixels of a glyph mask whose alpha reaches the
// threshold, relative to the glyph origin.
func thresholdMask(dr image.Rectangle, mask image.Image, maskp image.Point) []inkPixel {
	if mask == nil {
		return nil
	}
	var out []inkPixel
	for y := dr.Min.Y; y < dr.Max.Y; y++ {
		for x := dr.Min.X; x < dr.Max.X; x++ {
			_, _, _, a := mask.At(maskp.X+x-dr.Min.X, maskp.Y+y-dr.Min.Y).RGBA()
			if a>>8 >= glyphAlphaThreshold {
				out = append(out, inkPixel{x: x, y: y})
			}
		}
	}
	return out
}

// isChromeRune reports whether r can appear in chrome text.
func isChromeRune(r rune) bool {
	return strings.ContainsRune(chromeRunes, r)
}
