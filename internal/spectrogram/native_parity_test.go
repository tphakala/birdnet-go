package spectrogram

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/spectrogram/render"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// Parity thresholds against the committed sox renders. Tighten, never loosen
// silently.
const (
	// minRasterEqualReference is the fraction of raster pixels with equal palette
	// index against the committed sox references, which measure 0.9999 or better.
	minRasterEqualReference = 0.995
	maxRasterIndexDiff      = 2    // largest allowed index difference in the raster
	minGridEqual            = 0.98 // fraction of grid pixels away from text that match
	minNativeTextInSox      = 0.95 // native text pixels inside sox's dilated text mask
	minSoxTextInNative      = 0.90 // sox text pixels inside native's dilated text mask
	textDilation            = 2    // pixels
	soxFooterRows           = 14   // image rows at the bottom holding sox's footer comment

	// The committed reference clip.
	parityClipRate    = 24000
	parityClipSeconds = 2
	parityClipDir     = "render/testdata/sox"
	parityClipName    = "clip.wav"
)

// parityCase is one sox reference render of the committed clip.
type parityCase struct {
	name  string
	width int
	style string
	dr    string
	raw   bool
}

func parityCases() []parityCase {
	return []parityCase{
		{"default_sm", 258, conf.SpectrogramStyleDefault, "100", false},
		{"scientific_dark_md", 514, conf.SpectrogramStyleScientificDark, "100", false},
		{"high_contrast_dark_md_z80", 514, conf.SpectrogramStyleHighContrastDark, "80", false},
		{"scientific_md_z120", 514, conf.SpectrogramStyleScientific, "120", false},
		{"default_w100", 100, conf.SpectrogramStyleDefault, "100", false},
		{"default_md_raw", 514, conf.SpectrogramStyleDefault, "100", true},
	}
}

// xorshift is a fixed-seed integer PRNG so the committed clip is reproducible.
type xorshift uint32

func (x *xorshift) next() uint32 {
	v := uint32(*x)
	v ^= v << 13
	v ^= v >> 17
	v ^= v << 5
	*x = xorshift(v)
	return v
}

// clipSpec describes a synthetic test clip: noise, two tones, a linear chirp and
// a wideband burst. Times are in seconds from the start of the clip.
type clipSpec struct {
	rate, seconds        int
	noiseAmp             float64
	toneAHz, toneAAmp    float64
	toneBHz, toneBAmp    float64
	chirpLoHz, chirpHiHz float64
	chirpAmp             float64
	chirpStart, chirpEnd float64
	burstAmp             float64
	burstStart, burstEnd float64
}

// synthClip renders s as 16-bit samples. The noise comes from a fixed-seed
// xorshift, so a clip is reproducible sample for sample.
func synthClip(s *clipSpec) []int16 {
	out := make([]int16, s.rate*s.seconds)
	rng := xorshift(0x9e3779b9)
	for i := range out {
		t := float64(i) / float64(s.rate)
		noise := (float64(rng.next()>>8)/float64(1<<24) - 0.5) * 2
		v := s.noiseAmp*noise + s.toneAAmp*math.Sin(2*math.Pi*s.toneAHz*t) + s.toneBAmp*math.Sin(2*math.Pi*s.toneBHz*t)
		if t >= s.chirpStart && t < s.chirpEnd {
			u := (t - s.chirpStart) / (s.chirpEnd - s.chirpStart)
			phase := 2 * math.Pi * (s.chirpLoHz*(t-s.chirpStart) + (s.chirpHiHz-s.chirpLoHz)*(s.chirpEnd-s.chirpStart)*u*u/2)
			v += s.chirpAmp * math.Sin(phase)
		}
		if t >= s.burstStart && t < s.burstEnd {
			v += s.burstAmp * noise
		}
		out[i] = int16(math.Round(v * math.MaxInt16))
	}
	return out
}

// parityClipSamples builds the reference clip: noise, two tones, a chirp and a
// wideband burst near the end.
func parityClipSamples() []int16 {
	return synthClip(&clipSpec{
		rate: parityClipRate, seconds: parityClipSeconds,
		noiseAmp: 0.01,
		toneAHz:  1500, toneAAmp: 0.25,
		toneBHz: 4200, toneBAmp: 0.12,
		chirpLoHz: 2000, chirpHiHz: 9000, chirpAmp: 0.2, chirpStart: 0.3, chirpEnd: 1.2,
		burstAmp: 0.3, burstStart: 1.7, burstEnd: 1.85,
	})
}

func TestParityClipMatchesGenerator(t *testing.T) {
	want := parityClipSamples()
	path := filepath.Join(parityClipDir, parityClipName)
	src, err := transcode.DecodeMono(t.Context(), path, 0)
	require.NoError(t, err)
	var got []float32
	require.NoError(t, src.Stream(t.Context(), func(b []float32) error { got = append(got, b...); return nil }))
	require.Len(t, got, len(want))
	for i, s := range want {
		require.InDelta(t, float64(s), float64(got[i])*32768, 1, "sample %d", i)
	}
}

// parityMetrics are the measured differences between a native and a sox render.
type parityMetrics struct {
	rasterEqual, gridEqual, nativeInSox, soxInNative float64
	maxRasterDiff, outliers                          int
	outlierFraction                                  float64
}

func (m parityMetrics) String() string {
	return fmt.Sprintf("raster equal %.4f (max diff %d, %d beyond %d), grid equal %.4f, native text in sox %.4f, sox text in native %.4f",
		m.rasterEqual, m.maxRasterDiff, m.outliers, maxRasterIndexDiff, m.gridEqual, m.nativeInSox, m.soxInNative)
}

// assertParity compares a native render with a sox render of the same clip and
// returns the measured numbers.
func assertParity(t *testing.T, native, sox *image.Paletted, raw bool, rangeDB int, minRasterEqual, maxOutlierFraction float64) parityMetrics {
	t.Helper()
	require.Equal(t, sox.Bounds(), native.Bounds(), "dimensions")
	require.Equal(t, sox.Palette, native.Palette, "PLTE")
	w, h := sox.Bounds().Dx(), sox.Bounds().Dy()
	var m parityMetrics

	// Raster, in image coordinates (row 0 at the top).
	rx0, ry0, rcols, rrows := 0, 0, w, h
	if !raw {
		rcols = w - (soxchrome.MarginLeft + soxchrome.GapBetween + soxchrome.LegendWidth + soxchrome.MarginRight)
		rrows = h - (soxchrome.MarginBelow + soxchrome.MarginAbove)
		rx0, ry0 = soxchrome.MarginLeft, soxchrome.MarginAbove
	}
	equal := 0
	for y := ry0; y < ry0+rrows; y++ {
		for x := rx0; x < rx0+rcols; x++ {
			a, b := int(native.ColorIndexAt(x, y)), int(sox.ColorIndexAt(x, y))
			if a == b {
				equal++
			}
			m.maxRasterDiff = max(m.maxRasterDiff, abs(a-b))
			if abs(a-b) > maxRasterIndexDiff {
				m.outliers++
			}
		}
	}
	m.rasterEqual = float64(equal) / float64(rcols*rrows)
	assert.GreaterOrEqual(t, m.rasterEqual, minRasterEqual, "raster equality")
	m.outlierFraction = float64(m.outliers) / float64(rcols*rrows)
	if maxOutlierFraction == 0 {
		assert.LessOrEqual(t, m.maxRasterDiff, maxRasterIndexDiff, "largest raster index difference")
	} else {
		assert.LessOrEqual(t, m.outlierFraction, maxOutlierFraction, "fraction of pixels beyond the index difference bound")
	}
	if raw {
		return m
	}

	// Legend bar.
	k := min(soxchrome.LegendMaxRows, rrows)
	base := soxchrome.MarginBelow + (rrows-k)/2
	for j := range k {
		y := h - 1 - (base + j)
		for i := range soxchrome.LegendWidth {
			x := w - soxchrome.MarginRight - 1 - i
			require.Equal(t, sox.ColorIndexAt(x, y), native.ColorIndexAt(x, y), "legend bar at %d,%d", x, y)
		}
	}

	// Text masks and grid, outside the raster and the footer comment rows.
	outside := func(x, y int) bool {
		inRaster := x >= rx0 && x < rx0+rcols && y >= ry0 && y < ry0+rrows
		return !inRaster && y < h-soxFooterRows
	}
	isText := func(img *image.Paletted, x, y int) bool {
		i := img.ColorIndexAt(x, y)
		return i == soxchrome.IndexText || i == soxchrome.IndexLabels
	}
	soxMask, nativeMask := newMask(w, h), newMask(w, h)
	var nativeText, soxText []image.Point
	for y := range h {
		for x := range w {
			if !outside(x, y) {
				continue
			}
			if isText(sox, x, y) {
				soxMask.set(x, y)
				soxText = append(soxText, image.Pt(x, y))
			}
			if isText(native, x, y) {
				nativeMask.set(x, y)
				nativeText = append(nativeText, image.Pt(x, y))
			}
		}
	}
	soxDil, nativeDil := soxMask.dilate(textDilation), nativeMask.dilate(textDilation)
	m.nativeInSox = fractionIn(nativeText, soxDil)
	m.soxInNative = fractionIn(soxText, nativeDil)
	assert.GreaterOrEqual(t, m.nativeInSox, minNativeTextInSox, "native text inside sox text")
	assert.GreaterOrEqual(t, m.soxInNative, minSoxTextInNative, "sox text inside native text")

	gridTotal, gridEqual := 0, 0
	for y := range h {
		for x := range w {
			if !outside(x, y) || sox.ColorIndexAt(x, y) != soxchrome.IndexGrid {
				continue
			}
			if soxDil.at(x, y) || nativeDil.at(x, y) {
				continue
			}
			gridTotal++
			if native.ColorIndexAt(x, y) == soxchrome.IndexGrid {
				gridEqual++
			}
		}
	}
	m.gridEqual = 1
	if gridTotal > 0 {
		m.gridEqual = float64(gridEqual) / float64(gridTotal)
	}
	assert.GreaterOrEqual(t, m.gridEqual, minGridEqual, "grid equality")
	_ = rangeDB
	return m
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// mask is a boolean bitmap.
type mask struct {
	w, h int
	bits []bool
}

func newMask(w, h int) *mask { return &mask{w: w, h: h, bits: make([]bool, w*h)} }
func (m *mask) set(x, y int) { m.bits[y*m.w+x] = true }
func (m *mask) at(x, y int) bool {
	return x >= 0 && y >= 0 && x < m.w && y < m.h && m.bits[y*m.w+x]
}

// dilate returns the mask grown by r pixels in every direction (square kernel).
func (m *mask) dilate(r int) *mask {
	out := newMask(m.w, m.h)
	for y := range m.h {
		for x := range m.w {
			if !m.bits[y*m.w+x] {
				continue
			}
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					if xx, yy := x+dx, y+dy; xx >= 0 && yy >= 0 && xx < m.w && yy < m.h {
						out.bits[yy*m.w+xx] = true
					}
				}
			}
		}
	}
	return out
}

func fractionIn(points []image.Point, m *mask) float64 {
	if len(points) == 0 {
		return 1
	}
	in := 0
	for _, p := range points {
		if m.at(p.X, p.Y) {
			in++
		}
	}
	return float64(in) / float64(len(points))
}

// nativeRender renders through the generator.
func nativeRender(t *testing.T, audio string, c parityCase, profile FrequencyProfile) string {
	t.Helper()
	gen, env := newNativeGenerator(t)
	env.Settings.Realtime.Dashboard.Spectrogram.Style = c.style
	env.Settings.Realtime.Dashboard.Spectrogram.DynamicRange = c.dr
	out := filepath.Join(env.TempDir, "native.png")
	require.NoError(t, gen.GenerateFromFile(t.Context(), audio, out, c.width, c.raw, WithFrequencyProfile(profile)))
	return out
}

func rangeOf(t *testing.T, dr string) int {
	t.Helper()
	v := nativeRangeDB(dr)
	require.NotZero(t, v)
	return v
}

// TestSoxReferenceParity compares the native render of the committed clip with
// the committed sox renders. It needs no sox binary; the references were
// rendered once by sox 14.4.2 (see render/testdata/sox/README.md).
func TestSoxReferenceParity(t *testing.T) {
	clip, err := filepath.Abs(filepath.Join(parityClipDir, parityClipName))
	require.NoError(t, err)
	for _, c := range parityCases() {
		t.Run(c.name, func(t *testing.T) {
			ref := filepath.Join(parityClipDir, c.name+".png")
			out := nativeRender(t, clip, c, BirdProfile())
			m := assertParity(t, decodePNG(t, out), decodePNG(t, ref), c.raw, rangeOf(t, c.dr), minRasterEqualReference, 0)
			t.Logf("%s: %s", c.name, m)
		})
	}
}

// Bounds for TestNativeRateMatchesResampledBelowRollOff.
const (
	// rateClipRate and rateClipSeconds describe the 48 kHz clip compared.
	rateClipRate    = 48000
	rateClipSeconds = 15
	// resamplerPassbandHz is the top of the band the resampler passes without
	// attenuation; the roll-off starts near 11.4 kHz and rows above it differ.
	resamplerPassbandHz = 11000.0
	// edgeColumnFraction is the share of columns skipped at each side, where the
	// two analyses frame the clip edges differently.
	edgeColumnFraction = 0.05
	// maxRowMeanIndexDiff is the largest allowed difference of a row's mean
	// palette index between the two renders. Measured worst case 0.49.
	maxRowMeanIndexDiff = 1.0
	// rateClipDisplayHz is the display rate of the compared renders.
	rateClipDisplayHz = 24000
)

// rateClipSamples is a 15 s 48 kHz clip of noise, two tones, a chirp below 9 kHz
// and a wideband burst, with the events placed by fractions of the clip length so
// they lie inside the compared columns.
func rateClipSamples() []int16 {
	const secs = float64(rateClipSeconds)
	return synthClip(&clipSpec{
		rate: rateClipRate, seconds: rateClipSeconds,
		noiseAmp: 0.01,
		toneAHz:  2300, toneAAmp: 0.15,
		toneBHz: 5100, toneBAmp: 0.15,
		chirpLoHz: 3000, chirpHiHz: 9000, chirpAmp: 0.2, chirpStart: 0.2 * secs, chirpEnd: 0.4 * secs,
		burstAmp: 0.3, burstStart: 0.8 * secs, burstEnd: 0.85 * secs,
	})
}

// A 48 kHz clip analysed at its own rate must look like the same clip resampled
// to 24 kHz where the resampler passes the signal. Pixel by pixel comparison does
// not apply: the resampler advances its output by half its latency, which
// decorrelates the noise texture, so the mean palette index of each row over the
// interior columns is compared instead. This needs no sox binary. Time alignment
// is pinned by the render package tests.
func TestNativeRateMatchesResampledBelowRollOff(t *testing.T) {
	native, err := transcode.MonoFromPCM16(samplesToPCM(rateClipSamples()), rateClipRate, 0)
	require.NoError(t, err)
	resampled, err := native.WithRate(rateClipDisplayHz)
	require.NoError(t, err)

	for _, width := range []int{258, 2050} {
		for _, window := range []render.WindowKind{render.WindowHann, render.WindowDolph} {
			t.Run(fmt.Sprintf("w%d_window%d", width, window), func(t *testing.T) {
				rows := fftFriendlyHeight(width)
				opt := render.Options{Width: width, Rows: rows, Raw: true, RangeDB: nativeRangeExtendedDB, Style: render.Style{Window: window}, DisplayRate: rateClipDisplayHz}
				wantImg, wantCols, err := render.Render(t.Context(), resampled, opt)
				require.NoError(t, err)
				gotImg, gotCols, err := render.Render(t.Context(), native, opt)
				require.NoError(t, err)
				require.Equal(t, wantCols, gotCols)

				want, got := wantImg.(*image.Paletted), gotImg.(*image.Paletted)
				skip := int(edgeColumnFraction * float64(gotCols))
				interior := gotCols - 2*skip
				for y := range rows {
					hz := float64(rows-1-y) / float64(rows-1) * rateClipDisplayHz / 2
					if hz > resamplerPassbandHz {
						continue
					}
					var wantSum, gotSum int
					for x := skip; x < gotCols-skip; x++ {
						wantSum += int(want.ColorIndexAt(x, y))
						gotSum += int(got.ColorIndexAt(x, y))
					}
					assert.InDelta(t, float64(wantSum)/float64(interior), float64(gotSum)/float64(interior), maxRowMeanIndexDiff, "row %d (%.0f Hz)", y, hz)
				}
			})
		}
	}
}
