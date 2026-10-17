package spectrogram

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

// Parity thresholds against sox renders. Record measured numbers in the PR;
// tighten, never loosen silently.
const (
	// minRasterEqualReference is the fraction of raster pixels with equal palette
	// index against the committed sox references, which measure 0.9999 or better.
	// This is the only parity test that runs without a sox binary, so it keeps the
	// tight bound.
	minRasterEqualReference = 0.995
	// minRasterEqualLive is the same bound for the live sox cases. Two measure
	// 0.9901 (3 s tone clip at width 258 with a Dolph window at 120 dB, every
	// difference one palette step), so the reference bound of 0.995 is relaxed for the
	// live cases only.
	minRasterEqualLive = 0.985
	maxRasterIndexDiff = 2 // largest allowed index difference in the raster
	// maxOutlierFractionLive is the fraction of raster pixels the live cases may
	// have beyond maxRasterIndexDiff. The resampled cases compare two different
	// resamplers, so a few isolated pixels differ by more than the reference
	// renders allow; the committed references keep the strict maximum. The
	// worst measured case is 0.0014 (9 pixels of a 100 column raster).
	maxOutlierFractionLive = 0.002
	minGridEqual           = 0.98 // fraction of grid pixels away from text that match
	minNativeTextInSox     = 0.95 // native text pixels inside sox's dilated text mask
	minSoxTextInNative     = 0.90 // sox text pixels inside native's dilated text mask
	textDilation           = 2    // pixels
	soxFooterRows          = 14   // image rows at the bottom holding sox's footer comment

	// The committed reference clip.
	parityClipRate    = 24000
	parityClipSeconds = 2
	parityClipDir     = "render/testdata/sox"
	parityClipName    = "clip.wav"
	parityRegenEnv    = "SOX_REGEN"
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

// parityClipSamples builds the reference clip: noise, two tones, a chirp and a
// wideband burst near the end.
func parityClipSamples() []int16 {
	const (
		noiseAmp   = 0.01
		toneAAmp   = 0.25
		toneBAmp   = 0.12
		toneAHz    = 1500.0
		toneBHz    = 4200.0
		chirpAmp   = 0.2
		chirpStart = 0.3
		chirpEnd   = 1.2
		chirpLoHz  = 2000.0
		chirpHiHz  = 9000.0
		burstStart = 1.7
		burstEnd   = 1.85
		burstAmp   = 0.3
	)
	n := parityClipRate * parityClipSeconds
	out := make([]int16, n)
	rng := xorshift(0x9e3779b9)
	for i := range out {
		t := float64(i) / parityClipRate
		noise := (float64(rng.next()>>8)/float64(1<<24) - 0.5) * 2
		v := noiseAmp*noise + toneAAmp*math.Sin(2*math.Pi*toneAHz*t) + toneBAmp*math.Sin(2*math.Pi*toneBHz*t)
		if t >= chirpStart && t < chirpEnd {
			u := (t - chirpStart) / (chirpEnd - chirpStart)
			phase := 2 * math.Pi * (chirpLoHz*(t-chirpStart) + (chirpHiHz-chirpLoHz)*(chirpEnd-chirpStart)*u*u/2)
			v += chirpAmp * math.Sin(phase)
		}
		if t >= burstStart && t < burstEnd {
			v += burstAmp * noise
		}
		out[i] = int16(math.Round(v * math.MaxInt16))
	}
	return out
}

func TestParityClipMatchesGenerator(t *testing.T) {
	want := parityClipSamples()
	path := filepath.Join(parityClipDir, parityClipName)
	if os.Getenv(parityRegenEnv) != "" {
		writeNativeWAV(t, filepath.Dir(path), parityClipName, want, parityClipRate)
	}
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

// soxRenderer renders with the real sox through the generator, gate off.
func soxRender(t *testing.T, soxPath, audio string, c parityCase, profile FrequencyProfile, secs float64) string {
	t.Helper()
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "")
	gen, env := newNativeGenerator(t)
	env.Settings.Realtime.Audio.SoxPath = soxPath
	env.Settings.Realtime.Audio.SoxAudioTypes = []string{"wav"}
	env.Settings.Realtime.Dashboard.Spectrogram.Style = c.style
	env.Settings.Realtime.Dashboard.Spectrogram.DynamicRange = c.dr
	out := filepath.Join(env.TempDir, "sox.png")
	require.NoError(t, gen.GenerateFromFile(t.Context(), audio, out, c.width, c.raw, WithDuration(secs), WithFrequencyProfile(profile)))
	return out
}

// nativeRender renders with the native renderer through the generator, gate on.
func nativeRender(t *testing.T, audio string, c parityCase, profile FrequencyProfile) string {
	t.Helper()
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
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
// the committed sox renders. It needs no sox binary. With SOX_REGEN=1 it
// regenerates the references with the installed sox first.
func TestSoxReferenceParity(t *testing.T) {
	clip, err := filepath.Abs(filepath.Join(parityClipDir, parityClipName))
	require.NoError(t, err)
	for _, c := range parityCases() {
		t.Run(c.name, func(t *testing.T) {
			ref := filepath.Join(parityClipDir, c.name+".png")
			if os.Getenv(parityRegenEnv) != "" {
				soxPath := requireSoxAvailable(t)
				require.NoError(t, os.MkdirAll(parityClipDir, 0o750))
				data, err := os.ReadFile(soxRender(t, soxPath, clip, c, BirdProfile(), parityClipSeconds))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(ref, data, 0o600))
			}
			out := nativeRender(t, clip, c, BirdProfile())
			m := assertParity(t, decodePNG(t, out), decodePNG(t, ref), c.raw, rangeOf(t, c.dr), minRasterEqualReference, 0)
			t.Logf("%s: %s", c.name, m)
		})
	}
}

// liveInput is one input signal form for TestSoxParityLive.
type liveInput struct {
	name     string
	rate     int
	channels int
	profile  FrequencyProfile
}

// liveParityInputs are the source forms compared against the live sox binary.
func liveParityInputs() []liveInput {
	return []liveInput{
		{"mono24k", 24000, 1, BirdProfile()},
		{"mono48k", 48000, 1, BirdProfile()},
		{"stereo48k", 48000, 2, BirdProfile()},
		{"mono256k", 256000, 1, BatProfile()},
		{"mono384k", 384000, 1, BatProfile()},
	}
}

// liveCase is one live parity comparison.
type liveCase struct {
	in liveInput
	c  parityCase
}

// liveParityCases is a deterministic pairwise subset over input, size, style,
// range and raw.
func liveParityCases() []liveCase {
	const total = 40
	inputs := liveParityInputs()
	widths := []int{100, 258, 514, 1026, 2050}
	styles := []string{conf.SpectrogramStyleDefault, conf.SpectrogramStyleScientificDark, conf.SpectrogramStyleHighContrastDark, conf.SpectrogramStyleScientific}
	ranges := []string{"80", "100", "120"}
	out := make([]liveCase, 0, total)
	for i := range total {
		in, wi := inputs[i%len(inputs)], i/len(inputs)
		if i >= len(inputs)*len(widths) {
			in, wi = inputs[(i*3)%len(inputs)], i*2+1
		}
		c := parityCase{
			width: widths[wi%len(widths)],
			style: styles[i%len(styles)],
			dr:    ranges[i%len(ranges)],
			raw:   i%7 == 0,
		}
		c.name = fmt.Sprintf("%s_w%d_%s_z%s_raw%v", in.name, c.width, c.style, c.dr, c.raw)
		out = append(out, liveCase{in, c})
	}
	return out
}

// writeLiveWAV writes a tonal clip (tones and a chirp) at the input's rate and
// channel count. Both channels carry the same signal, so the mono mix equals it.
func writeLiveWAV(t *testing.T, path string, in liveInput, secs int) {
	t.Helper()
	nyq := float64(in.rate) / 2
	frames := in.rate * secs
	samples := make([]int16, frames*in.channels)
	for i := range frames {
		tm := float64(i) / float64(in.rate)
		v := 0.25*math.Sin(2*math.Pi*0.12*nyq*tm) + 0.15*math.Sin(2*math.Pi*0.31*nyq*tm)
		v += 0.2 * math.Sin(2*math.Pi*(0.05*nyq+0.2*nyq*tm/float64(secs))*tm)
		for c := range in.channels {
			samples[i*in.channels+c] = int16(math.Round(v * math.MaxInt16))
		}
	}
	data := samplesToPCM(samples)
	const hdr = 44
	buf := make([]byte, hdr+len(data))
	copy(buf, "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(hdr-8+len(data))) //nolint:gosec // G115: bounded test sizes
	copy(buf[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], uint16(in.channels))           //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint32(buf[24:], uint32(in.rate))               //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint32(buf[28:], uint32(in.rate*in.channels*2)) //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint16(buf[32:], uint16(in.channels*2))         //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(len(data))) //nolint:gosec // G115: bounded test sizes
	copy(buf[hdr:], data)
	require.NoError(t, os.WriteFile(path, buf, 0o600))
}

// TestSoxParityLive compares native renders with the real sox binary over a
// pairwise subset of sizes, styles, ranges and input forms. Skips without sox.
func TestSoxParityLive(t *testing.T) {
	soxPath := requireSoxAvailable(t)
	if runtime.GOOS == osWindows {
		t.Skip("live sox parity runs on POSIX hosts")
	}
	const secs = 3
	for _, lc := range liveParityCases() {
		t.Run(lc.c.name, func(t *testing.T) {
			dir := t.TempDir()
			audio := filepath.Join(dir, "in.wav")
			writeLiveWAV(t, audio, lc.in, secs)
			soxOut := soxRender(t, soxPath, audio, lc.c, lc.in.profile, secs)
			natOut := nativeRender(t, audio, lc.c, lc.in.profile)
			m := assertParity(t, decodePNG(t, natOut), decodePNG(t, soxOut), lc.c.raw, rangeOf(t, lc.c.dr), minRasterEqualLive, maxOutlierFractionLive)
			t.Logf("%s", m)
		})
	}
}
