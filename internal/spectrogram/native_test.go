package spectrogram

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

const (
	// nativeTestWidth is the raster width used by the gate tests (the sm size).
	nativeTestWidth = 258
	// nativeTestRows is fftFriendlyHeight(nativeTestWidth).
	nativeTestRows = 129
	// nativeTestSeconds is the length of the synthetic clip.
	nativeTestSeconds = 2
	// nativeTestMinCols is the fewest raster columns a 2 s clip may produce at
	// nativeTestWidth.
	nativeTestMinCols = 250

	soxStubMarker = "SOXSTUB\n"
	pngSignature  = "\x89PNG\r\n\x1a\n"
)

// nativeTestSamples returns a mono 16-bit clip: a tone plus a sweep.
func nativeTestSamples(rate int) []int16 {
	n := rate * nativeTestSeconds
	out := make([]int16, n)
	for i := range out {
		t := float64(i) / float64(rate)
		v := 0.3*math.Sin(2*math.Pi*2000*t) + 0.2*math.Sin(2*math.Pi*(500+1500*t)*t)
		out[i] = int16(v * math.MaxInt16)
	}
	return out
}

func samplesToPCM(samples []int16) []byte {
	b := make([]byte, 2*len(samples))
	for i, s := range samples {
		b[2*i] = byte(s)
		b[2*i+1] = byte(uint16(s) >> 8) //nolint:gosec // G115: PCM bit reinterpretation in a test fixture
	}
	return b
}

// writeNativeWAV writes samples as a WAV file at dir/name.
func writeNativeWAV(t *testing.T, dir, name string, samples []int16, rate int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{PCMData: samplesToPCM(samples), OutputPath: path, SampleRate: rate, BitDepth: 16}))
	return path
}

// markerSoxStub returns a sox stand-in that writes the marker to its "-o" target
// and records each run in the returned file. POSIX only.
func markerSoxStub(t *testing.T) (stub, ranFile string) {
	t.Helper()
	dir := t.TempDir()
	stub = filepath.Join(dir, "sox")
	ranFile = filepath.Join(dir, "ran")
	script := "#!/bin/sh\n" +
		"echo run >> '" + ranFile + "'\n" +
		"prev=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then printf 'SOXSTUB\\n' > \"$a\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o700)) //nolint:gosec // G306: test stub must be executable
	return stub, ranFile
}

func stubRuns(t *testing.T, ranFile string) int {
	t.Helper()
	data, err := os.ReadFile(ranFile) //nolint:gosec // G304: test path
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return bytes.Count(data, []byte("run"))
}

func newNativeGenerator(t *testing.T) (*Generator, *testEnv) {
	t.Helper()
	env := setupTestEnv(t)
	return NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test")), env
}

// decodePNG decodes the file and returns it as a paletted image.
func decodePNG(t *testing.T, path string) *image.Paletted {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: test path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	require.NoError(t, err)
	pal, ok := img.(*image.Paletted)
	require.True(t, ok, "native output is a paletted PNG")
	return pal
}

// requireNativePNG asserts that path holds the native render of the test clip.
func requireNativePNG(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test path
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(raw, []byte(pngSignature)), "file is a PNG")
	b := decodePNG(t, path).Bounds()
	assert.Equal(t, nativeTestRows+soxchrome.MarginBelow+soxchrome.MarginAbove, b.Dy())
	cols := b.Dx() - (soxchrome.MarginLeft + soxchrome.GapBetween + soxchrome.LegendWidth + soxchrome.MarginRight)
	assert.GreaterOrEqual(t, cols, nativeTestMinCols)
	assert.LessOrEqual(t, cols, nativeTestWidth)
}

func requireStubFile(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, soxStubMarker, string(raw))
}

func skipWithoutPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == osWindows {
		t.Skip("sox stub requires a POSIX shell")
	}
}

func TestNativeGate_Transitions(t *testing.T) {
	skipWithoutPOSIXShell(t)
	gen, env := newNativeGenerator(t)
	stub, ran := markerSoxStub(t)
	env.Settings.Realtime.Audio.SoxPath = stub
	env.Settings.Realtime.Audio.SoxAudioTypes = []string{"wav"}
	audio := writeNativeWAV(t, env.TempDir, "in.wav", nativeTestSamples(24000), 24000)

	sequences := map[string][]bool{
		"off on off": {false, true, false},
		"on on":      {true, true},
	}
	for name, steps := range sequences {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(ran)
			wantRuns := 0
			for i, on := range steps {
				if on {
					t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
				} else {
					t.Setenv(conf.EnvNativeSpectrogramRenderer, "")
				}
				out := filepath.Join(env.TempDir, name, string(rune('a'+i))+".png")
				require.NoError(t, gen.GenerateFromFile(t.Context(), audio, out, nativeTestWidth, false, WithDuration(nativeTestSeconds)))
				if on {
					requireNativePNG(t, out)
				} else {
					requireStubFile(t, out)
					wantRuns++
				}
				assert.Equal(t, wantRuns, stubRuns(t, ran), "step %d", i)
			}
		})
	}
}

func TestNativeGate_ExistingFileSurvivesPrerenderWithGateOn(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	pr, env := createMinimalPreRenderer(t, 1)
	clip := filepath.Join(env.TempDir, "clip.wav")
	spec, err := BuildSpectrogramPath(clip)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(spec, []byte(soxStubMarker), 0o600))

	job := createTestJob(clip, 1)
	require.NoError(t, pr.Submit(job))

	assert.Equal(t, int64(1), pr.GetStats().Skipped)
	requireStubFile(t, spec)
}

func TestNativeGate_NoSoxNeeded(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	env.Settings.Realtime.Audio.SoxPath = filepath.Join(env.TempDir, "missing-sox")
	samples := nativeTestSamples(24000)

	wavPath := writeNativeWAV(t, env.TempDir, "in.wav", samples, 24000)
	flacPath := filepath.Join(env.TempDir, "in.flac")
	require.NoError(t, flac.EncodePCM(t.Context(), &flac.Options{PCMData: samplesToPCM(samples), OutputPath: flacPath, SampleRate: 24000, Channels: 1, BitDepth: 16}))

	for _, in := range []string{wavPath, flacPath} {
		out := filepath.Join(env.TempDir, filepath.Base(in)+".png")
		require.NoError(t, gen.GenerateFromFile(t.Context(), in, out, nativeTestWidth, false))
		requireNativePNG(t, out)
	}
	out := filepath.Join(env.TempDir, "pcm.png")
	require.NoError(t, gen.GenerateFromPCM(t.Context(), samplesToPCM(samples), out, nativeTestWidth, false, 24000))
	requireNativePNG(t, out)
}

func TestNativeGate_FailureNoFallback(t *testing.T) {
	skipWithoutPOSIXShell(t)
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	stub, ran := markerSoxStub(t)
	env.Settings.Realtime.Audio.SoxPath = stub
	env.Settings.Realtime.Audio.SoxAudioTypes = []string{"wav", "mp3"}

	corrupt := filepath.Join(env.TempDir, "corrupt.wav")
	require.NoError(t, os.WriteFile(corrupt, []byte("RIFF....WAVEjunk"), 0o600))
	unsupported := filepath.Join(env.TempDir, "clip.xyz")
	require.NoError(t, os.WriteFile(unsupported, []byte("not audio"), 0o600))

	for _, in := range []string{corrupt, unsupported} {
		out := filepath.Join(env.TempDir, filepath.Base(in)+".png")
		err := gen.GenerateFromFile(t.Context(), in, out, nativeTestWidth, false)
		require.Error(t, err, in)
		assert.NoFileExists(t, out)
		assertNoSpectrogramTemp(t, out)
	}
	assert.Zero(t, stubRuns(t, ran), "sox must not run as a fallback")
}

func TestNativeGate_WidthBelowMinimum(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	audio := writeNativeWAV(t, env.TempDir, "in.wav", nativeTestSamples(24000), 24000)

	err := gen.GenerateFromFile(t.Context(), audio, filepath.Join(env.TempDir, "w.png"), 99, false)

	require.Error(t, err)
	enhanced, ok := errors.AsType[*errors.EnhancedError](err)
	require.True(t, ok)
	assert.Equal(t, errors.CategoryValidation, enhanced.Category)
}

func TestNativeGate_CanceledIsOperational(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	samples := nativeTestSamples(24000)
	audio := writeNativeWAV(t, env.TempDir, "in.wav", samples, 24000)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	errFile := gen.GenerateFromFile(ctx, audio, filepath.Join(env.TempDir, "f.png"), nativeTestWidth, false)
	errPCM := gen.GenerateFromPCM(ctx, samplesToPCM(samples), filepath.Join(env.TempDir, "p.png"), nativeTestWidth, false, 24000)

	for name, err := range map[string]error{"file": errFile, "pcm": errPCM} {
		require.Error(t, err, name)
		assert.True(t, IsOperationalError(err), name)
	}
}

func TestNativeGate_StyleHotReload(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	audio := writeNativeWAV(t, env.TempDir, "in.wav", nativeTestSamples(24000), 24000)

	env.Settings.Realtime.Dashboard.Spectrogram.Style = conf.SpectrogramStyleDefault
	first := filepath.Join(env.TempDir, "default.png")
	require.NoError(t, gen.GenerateFromFile(t.Context(), audio, first, nativeTestWidth, false))
	env.Settings.Realtime.Dashboard.Spectrogram.Style = conf.SpectrogramStyleScientific
	second := filepath.Join(env.TempDir, "scientific.png")
	require.NoError(t, gen.GenerateFromFile(t.Context(), audio, second, nativeTestWidth, false))

	assert.NotEqual(t, decodePNG(t, first).Palette, decodePNG(t, second).Palette)
}

func TestNativeGate_FileAndPCMAgree(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	samples := nativeTestSamples(48000)
	audio := writeNativeWAV(t, env.TempDir, "in.wav", samples, 48000)

	fromFile := filepath.Join(env.TempDir, "file.png")
	fromPCM := filepath.Join(env.TempDir, "pcm.png")
	require.NoError(t, gen.GenerateFromFile(t.Context(), audio, fromFile, nativeTestWidth, false))
	require.NoError(t, gen.GenerateFromPCM(t.Context(), samplesToPCM(samples), fromPCM, nativeTestWidth, false, 48000))

	a, err := os.ReadFile(fromFile) //nolint:gosec // G304: test path
	require.NoError(t, err)
	b, err := os.ReadFile(fromPCM) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestNativeGate_ProcessedPreviewPath(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	audio := writeNativeWAV(t, env.TempDir, "in.wav", nativeTestSamples(24000), 24000)
	// The processed preview creates its output file before rendering.
	out := filepath.Join(env.TempDir, "preview.png")
	require.NoError(t, os.WriteFile(out, nil, 0o600))

	require.NoError(t, gen.GenerateFromFile(t.Context(), audio, out, nativeTestWidth, false))

	requireNativePNG(t, out)
}

func TestNativeGate_DecodeErrorReportedOnce(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	in := filepath.Join(env.TempDir, "clip.xyz")
	require.NoError(t, os.WriteFile(in, []byte("not audio"), 0o600))

	err := gen.GenerateFromFile(t.Context(), in, filepath.Join(env.TempDir, "o.png"), nativeTestWidth, false)

	require.ErrorIs(t, err, transcode.ErrUnsupportedSource)
	enhanced, ok := errors.AsType[*errors.EnhancedError](err)
	require.True(t, ok)
	assert.Equal(t, "audiocore/transcode", enhanced.GetComponent(), "the transcode error is kept, not rebuilt")
	assert.Contains(t, err.Error(), "native spectrogram "+nativeOperationFile)
}
