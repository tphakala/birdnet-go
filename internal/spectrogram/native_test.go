package spectrogram

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/spectrogram/render"
	"github.com/tphakala/birdnet-go/internal/spectrogram/soxchrome"
)

const (
	// nativeTestWidth is the raster width used by the gate tests (the sm size).
	nativeTestWidth = 258
	// nativeTestSize is the size name that maps to nativeTestWidth.
	nativeTestSize = "sm"
	// nativeCodecRate is a sample rate every clip encoder accepts.
	nativeCodecRate = 48000
	// nativeTestRows is fftFriendlyHeight(nativeTestWidth).
	nativeTestRows = 129
	// nativeTestSeconds is the length of the synthetic clip.
	nativeTestSeconds = 2
	// nativeTestMinCols is the fewest raster columns a 2 s clip may produce at
	// nativeTestWidth.
	nativeTestMinCols = 250

	// nativeStreamSeconds and nativeStreamToneHz describe a clip of three decode
	// chunks, long enough for transcode.OpenMono to stream it.
	nativeStreamSeconds = 3.3
	nativeStreamToneHz  = 2000.0

	// xingSearchBytes bounds the search for the Xing or Info tag at the start of an
	// MP3. The flags word follows the tag name, and the frame count follows the
	// flags.
	xingSearchBytes  = 400
	xingFlagsOffset  = 4
	xingFramesOffset = 8

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
		"cat > /dev/null\n" +
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

// TestNativeGate_PrerenderSubmitLeavesExistingFile pins that a prerender submit
// skips a spectrogram path that already holds a file, whichever renderer made it.
func TestNativeGate_PrerenderSubmitLeavesExistingFile(t *testing.T) {
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
	t.Setenv("PATH", "")
	gen, env := newNativeGenerator(t)
	env.Settings.Realtime.Audio.SoxPath = filepath.Join(env.TempDir, "missing-sox")
	samples := nativeTestSamples(24000)
	pcm48 := samplesToPCM(nativeTestSamples(nativeCodecRate))

	wavPath := writeNativeWAV(t, env.TempDir, "in.wav", samples, 24000)
	flacPath := filepath.Join(env.TempDir, "in.flac")
	require.NoError(t, flac.EncodePCM(t.Context(), &flac.Options{PCMData: samplesToPCM(samples), OutputPath: flacPath, SampleRate: 24000, Channels: 1, BitDepth: 16}))
	mp3Path := filepath.Join(env.TempDir, "in.mp3")
	require.NoError(t, mp3.EncodePCM(t.Context(), &mp3.Options{PCMData: pcm48, OutputPath: mp3Path, SampleRate: nativeCodecRate, Channels: 1, BitDepth: 16}))
	m4aPath := filepath.Join(env.TempDir, "in.m4a")
	require.NoError(t, aac.EncodePCM(t.Context(), &aac.Options{PCMData: pcm48, OutputPath: m4aPath, SampleRate: nativeCodecRate, Channels: 1, BitDepth: 16}))
	opusPath := filepath.Join(env.TempDir, "in.opus")
	require.NoError(t, opus.EncodePCM(t.Context(), &opus.Options{PCMData: pcm48, OutputPath: opusPath, SampleRate: nativeCodecRate, Channels: 1, BitDepth: 16}))

	for _, in := range []string{wavPath, flacPath, mp3Path, m4aPath, opusPath} {
		out := filepath.Join(env.TempDir, filepath.Base(in)+".png")
		require.NoError(t, gen.GenerateFromFile(t.Context(), in, out, nativeTestWidth, false), in)
		requireNativePNG(t, out)
	}
	out := filepath.Join(env.TempDir, "pcm.png")
	require.NoError(t, gen.GenerateFromPCM(t.Context(), samplesToPCM(samples), out, nativeTestWidth, false, 24000))
	requireNativePNG(t, out)
}

// TestNativeGate_PrerenderRendersWithoutBinaries renders a prerender job through
// processJob with no sox configured and no binary on PATH.
func TestNativeGate_PrerenderRendersWithoutBinaries(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	t.Setenv("PATH", "")
	pr, env := createTestPreRenderer(t, &PreRendererTestOptions{SoxPath: "", SpectrogramSize: nativeTestSize})
	clip := filepath.Join(env.TempDir, "clip.wav")
	spec, err := BuildSpectrogramPath(clip)
	require.NoError(t, err)

	job := createTestJob(clip, 1)
	job.PCMData = samplesToPCM(nativeTestSamples(24000))
	job.SampleRate = 24000
	pr.processJob(job, 0)

	requireNativePNG(t, spec)
	assert.Zero(t, pr.GetStats().Failed)
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
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()

	for name, ctx := range map[string]context.Context{"canceled": canceled, "deadline": expired} {
		errFile := gen.GenerateFromFile(ctx, audio, filepath.Join(env.TempDir, name+"-f.png"), nativeTestWidth, false)
		errPCM := gen.GenerateFromPCM(ctx, samplesToPCM(samples), filepath.Join(env.TempDir, name+"-p.png"), nativeTestWidth, false, 24000)
		for entry, err := range map[string]error{"file": errFile, "pcm": errPCM} {
			require.Error(t, err, name+" "+entry)
			assert.True(t, IsOperationalError(err), name+" "+entry)
			enhanced, ok := errors.AsType[*errors.EnhancedError](err)
			require.True(t, ok, name+" "+entry)
			assert.Equal(t, errors.CategorySystem, enhanced.Category, name+" "+entry)
			assert.Equal(t, errors.PriorityLow, enhanced.GetPriority(), name+" "+entry)
		}
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
	unsupported := filepath.Join(env.TempDir, "clip.xyz")
	require.NoError(t, os.WriteFile(unsupported, []byte("not audio"), 0o600))
	// A truncated WAV fails while the render streams it, not before.
	whole := writeNativeWAV(t, env.TempDir, "whole.wav", toneSamples(nativeCodecRate, nativeStreamSeconds, nativeStreamToneHz), nativeCodecRate)
	wholeBytes, err := os.ReadFile(whole) //nolint:gosec // G304: test path
	require.NoError(t, err)
	truncated := filepath.Join(env.TempDir, "cut.wav")
	require.NoError(t, os.WriteFile(truncated, wholeBytes[:len(wholeBytes)/2], 0o600))

	tests := []struct {
		name string
		in   string
		want error
	}{
		{name: "unsupported extension", in: unsupported, want: transcode.ErrUnsupportedSource},
		{name: "truncated WAV", in: truncated, want: transcode.ErrTruncatedSource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(env.TempDir, tt.name+".png")

			err := gen.GenerateFromFile(t.Context(), tt.in, out, nativeTestWidth, false)

			require.ErrorIs(t, err, tt.want)
			enhanced, ok := errors.AsType[*errors.EnhancedError](err)
			require.True(t, ok)
			assert.Equal(t, "audiocore/transcode", enhanced.GetComponent(), "the transcode error is kept, not rebuilt")
			assert.Contains(t, err.Error(), "native spectrogram "+nativeOperationFile)
			assert.NoFileExists(t, out)
			assertNoSpectrogramTemp(t, out)
		})
	}
}

// renderBuffered renders path the way the native file path did before clips
// were streamed: decoded into memory, then drawn.
func renderBuffered(t *testing.T, gen *Generator, env *testEnv, path, out string) {
	t.Helper()
	decoded, err := transcode.DecodeMono(t.Context(), path, 0)
	require.NoError(t, err)
	src, displayRate, err := analysisSource(decoded, BirdProfile())
	require.NoError(t, err)
	require.NoError(t, gen.renderNative(t.Context(), env.Settings, src, displayRate, out, nativeTestWidth, false, nativeOperationFile, 0, false, 0))
}

// mp3WithDoubledFrameCount returns an MP3 whose Xing or Info tag declares twice
// the frames it holds, so a decoder reports a length twice the audio.
func mp3WithDoubledFrameCount(t *testing.T, data []byte) []byte {
	t.Helper()
	head := data[:min(len(data), xingSearchBytes)]
	idx := bytes.Index(head, []byte("Xing"))
	if idx < 0 {
		idx = bytes.Index(head, []byte("Info"))
	}
	require.GreaterOrEqual(t, idx, 0, "the encoder writes a length tag")
	require.NotZero(t, binary.BigEndian.Uint32(data[idx+xingFlagsOffset:])&1, "the tag carries a frame count")
	out := bytes.Clone(data)
	binary.BigEndian.PutUint32(out[idx+xingFramesOffset:], 2*binary.BigEndian.Uint32(out[idx+xingFramesOffset:]))
	return out
}

// A clip long enough to stream must produce the picture the buffered render
// produces, including a file whose header declares more audio than it holds, which
// is drawn again from memory instead of as a narrow picture.
func TestGenerateNativeFromFile_MatchesTheBufferedRender(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	samples := toneSamples(nativeCodecRate, nativeStreamSeconds, nativeStreamToneHz)
	wavPath := writeNativeWAV(t, env.TempDir, "in.wav", samples, nativeCodecRate)
	mp3Path := filepath.Join(env.TempDir, "good.mp3")
	require.NoError(t, mp3.EncodePCM(t.Context(), &mp3.Options{PCMData: samplesToPCM(samples), OutputPath: mp3Path, SampleRate: nativeCodecRate, Channels: 1, BitDepth: 16}))
	goodMP3, err := os.ReadFile(mp3Path) //nolint:gosec // G304: test path
	require.NoError(t, err)
	doubledPath := filepath.Join(env.TempDir, "doubled.mp3")
	require.NoError(t, os.WriteFile(doubledPath, mp3WithDoubledFrameCount(t, goodMP3), 0o600))

	tests := []struct {
		name         string
		in           string
		wantStreamed bool
	}{
		{name: "WAV", in: wavPath, wantStreamed: true},
		{name: "MP3 declaring twice its length", in: doubledPath, wantStreamed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filepath.Join(env.TempDir, tt.name+"-got.png")
			want := filepath.Join(env.TempDir, tt.name+"-want.png")
			logs := logtest.CaptureBuffer(t)

			require.NoError(t, gen.GenerateFromFile(t.Context(), tt.in, got, nativeTestWidth, false))
			rendered := logs.String()
			renderBuffered(t, gen, env, tt.in, want)

			gotBytes, err := os.ReadFile(got) //nolint:gosec // G304: test path
			require.NoError(t, err)
			wantBytes, err := os.ReadFile(want) //nolint:gosec // G304: test path
			require.NoError(t, err)
			assert.Equal(t, wantBytes, gotBytes)
			assert.Contains(t, rendered, fmt.Sprintf("streamed=%t", tt.wantStreamed))
			assert.NotContains(t, rendered, fmt.Sprintf("streamed=%t", !tt.wantStreamed))
			assert.Equal(t, !tt.wantStreamed, strings.Contains(rendered, "shorter than its header declares"), "only the over-declared file is rendered again from memory")
		})
	}
}

// The short-source signal is not a failure to classify or report: nativeError
// returns it as it is so the caller can render the clip again from memory.
func TestNativeError_ShortSourceSignalIsPassedThrough(t *testing.T) {
	gen, _ := newNativeGenerator(t)
	signal := fmt.Errorf("%w: read 1 of 2 declared frames", transcode.ErrShorterThanDeclared)

	got := gen.nativeError(t.Context(), signal, nativeOperationFile)

	require.ErrorIs(t, got, transcode.ErrShorterThanDeclared)
	_, enhanced := errors.AsType[*errors.EnhancedError](got)
	assert.False(t, enhanced, "the signal is not built through the errors builder")

	other := gen.nativeError(t.Context(), errors.NewStd("plain failure"), nativeOperationFile)
	_, enhanced = errors.AsType[*errors.EnhancedError](other)
	assert.True(t, enhanced, "any other error is still classified")
}

// brightestRow returns the image row with the highest palette index in column x.
func brightestRow(img *image.Paletted, x int) int {
	best, row := -1, -1
	for y := range img.Bounds().Dy() {
		if v := int(img.ColorIndexAt(x, y)); v > best {
			best, row = v, y
		}
	}
	return row
}

// toneSamples returns a mono sine clip of the given frequency.
func toneSamples(rate int, seconds, freq float64) []int16 {
	out := make([]int16, int(float64(rate)*seconds))
	for i := range out {
		out[i] = int16(0.4 * math.MaxInt16 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

func TestNativeGate_ProfileRateSetsNyquist(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	const (
		toneHz    = 3000.0
		birdNyq   = 12000.0
		rawWidth  = nativeTestWidth
		wantRow   = 96 // round((1 - 3000/12000) * (129 - 1))
		sourceNyq = 24000.0
	)
	require.NotEqual(t, wantRow, int(math.Round((1-toneHz/sourceNyq)*(nativeTestRows-1))), "the source rate must give a different row")
	require.Equal(t, wantRow, int(math.Round((1-toneHz/birdNyq)*(nativeTestRows-1))))

	// 48 kHz is analysed at its own rate, 44.1 kHz is not an exact multiple of the
	// profile rate and 12 kHz is below it, so both of those are resampled.
	tests := []struct {
		name string
		rate int
	}{
		{"48 kHz analysed without resampling", 48000},
		{"44.1 kHz resampled", 44100},
		{"12 kHz resampled", 12000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := toneSamples(tt.rate, nativeTestSeconds, toneHz)
			audio := writeNativeWAV(t, env.TempDir, "tone.wav", samples, tt.rate)

			fromFile := filepath.Join(env.TempDir, "file.png")
			require.NoError(t, gen.GenerateFromFile(t.Context(), audio, fromFile, rawWidth, true))
			fromPCM := filepath.Join(env.TempDir, "pcm.png")
			require.NoError(t, gen.GenerateFromPCM(t.Context(), samplesToPCM(samples), fromPCM, rawWidth, true, tt.rate))

			for name, path := range map[string]string{"file": fromFile, "pcm": fromPCM} {
				img := decodePNG(t, path)
				assert.InDelta(t, wantRow, brightestRow(img, img.Bounds().Dx()/2), 1, name)
			}
		})
	}
}

// analysisSource keeps a clip at an accepted multiple of the profile rate at its
// own rate and resamples every other clip to the profile rate.
func TestAnalysisSource(t *testing.T) {
	const birdHz = 24000
	tests := []struct {
		name        string
		rate        int
		profile     FrequencyProfile
		wantRate    int
		wantDisplay int
	}{
		{"bird 48 kHz is not resampled", 48000, BirdProfile(), 48000, birdHz},
		{"bird 44.1 kHz is resampled", 44100, BirdProfile(), birdHz, birdHz},
		{"bird 96 kHz is resampled", 96000, BirdProfile(), birdHz, birdHz},
		{"bat 256 kHz keeps its rate", 256000, BatProfile(), 256000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := transcode.MonoFromPCM16(samplesToPCM(toneSamples(tt.rate, 0.1, 1000)), tt.rate, 0)
			require.NoError(t, err)

			src, displayRate, err := analysisSource(decoded, tt.profile)

			require.NoError(t, err)
			assert.Equal(t, tt.wantRate, src.SampleRate())
			assert.Equal(t, tt.wantDisplay, displayRate)
		})
	}
}

func TestNativeGate_BatProfileKeepsNativeRate(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	const (
		seconds     = 0.5
		legacyNyqHz = 128000.0 // the old fixed bat axis, which must no longer apply
	)
	tests := []struct {
		name    string
		srcRate int
		toneHz  float64
	}{
		{"192 kHz clip", 192000, 80000},
		{"256 kHz clip", 256000, 50000},
		{"384 kHz clip", 384000, 150000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nyq := float64(tt.srcRate) / 2
			wantRow := int(math.Round((1 - tt.toneHz/nyq) * (nativeTestRows - 1)))
			legacyRow := int(math.Round((1 - tt.toneHz/legacyNyqHz) * (nativeTestRows - 1)))
			samples := toneSamples(tt.srcRate, seconds, tt.toneHz)
			audio := writeNativeWAV(t, env.TempDir, "bat.wav", samples, tt.srcRate)

			fromFile := filepath.Join(env.TempDir, "file.png")
			require.NoError(t, gen.GenerateFromFile(t.Context(), audio, fromFile, nativeTestWidth, true, WithFrequencyProfile(BatProfile())))
			fromPCM := filepath.Join(env.TempDir, "pcm.png")
			require.NoError(t, gen.GenerateFromPCM(t.Context(), samplesToPCM(samples), fromPCM, nativeTestWidth, true, tt.srcRate, WithFrequencyProfile(BatProfile())))

			for name, path := range map[string]string{"file": fromFile, "pcm": fromPCM} {
				img := decodePNG(t, path)
				got := brightestRow(img, img.Bounds().Dx()/2)
				assert.InDelta(t, wantRow, got, 1, name)
				if tt.srcRate != 256000 {
					assert.NotEqual(t, legacyRow, got, "%s: row must not match a 128 kHz Nyquist", name)
				}
			}
		})
	}
}

func TestNativeGate_PCMZeroRateUsesDefault(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	assert.Equal(t, conf.SampleRate, pcmSourceRate(0))
	assert.Equal(t, conf.SampleRate, pcmSourceRate(-1))
	assert.Equal(t, 24000, pcmSourceRate(24000))

	pcm := samplesToPCM(toneSamples(conf.SampleRate, nativeTestSeconds, 3000))
	zero := filepath.Join(env.TempDir, "zero.png")
	explicit := filepath.Join(env.TempDir, "explicit.png")
	require.NoError(t, gen.GenerateFromPCM(t.Context(), pcm, zero, nativeTestWidth, false, 0))
	require.NoError(t, gen.GenerateFromPCM(t.Context(), pcm, explicit, nativeTestWidth, false, conf.SampleRate))

	a, err := os.ReadFile(zero) //nolint:gosec // G304: test path
	require.NoError(t, err)
	b, err := os.ReadFile(explicit) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestNativeGate_PCMFailureNoFallback(t *testing.T) {
	skipWithoutPOSIXShell(t)
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	stub, ran := markerSoxStub(t)
	env.Settings.Realtime.Audio.SoxPath = stub

	out := filepath.Join(env.TempDir, "fail.png")
	// One byte is less than one 16-bit sample, so the native source rejects it.
	err := gen.GenerateFromPCM(t.Context(), []byte{1}, out, nativeTestWidth, false, 24000)

	require.Error(t, err)
	assert.NoFileExists(t, out)
	assertNoSpectrogramTemp(t, out)
	assert.Zero(t, stubRuns(t, ran), "sox must not run as a fallback")
}

func TestStyleForMatchesGetStyleArgs(t *testing.T) {
	for _, name := range []string{
		conf.SpectrogramStyleDefault, conf.SpectrogramStyleScientificDark,
		conf.SpectrogramStyleHighContrastDark, conf.SpectrogramStyleScientific, "", "bogus",
	} {
		args := getStyleArgs(name)
		want := render.Style{Palette: soxchrome.PaletteSpec{
			Monochrome:      slices.Contains(args, "-m"),
			HighColour:      slices.Contains(args, "-h"),
			LightBackground: slices.Contains(args, "-l"),
		}}
		if slices.Contains(args, "dolph") {
			want.Window = render.WindowDolph
		}
		assert.Equal(t, want, render.StyleFor(name), name)
	}
}

func TestNativeRangeDB(t *testing.T) {
	assert.Equal(t, nativeRangeHighContrastDB, nativeRangeDB(conf.SpectrogramDynamicRangeHighContrast))
	assert.Equal(t, nativeRangeStandardDB, nativeRangeDB(conf.SpectrogramDynamicRangeStandard))
	assert.Equal(t, nativeRangeExtendedDB, nativeRangeDB(conf.SpectrogramDynamicRangeExtended))
	assert.Zero(t, nativeRangeDB("unknown"), "an unknown preset gives 0, which render rejects")
}

// nativeTestSource is a short mono clip for the file-writing tests.
func nativeTestSource(t *testing.T) render.Source {
	t.Helper()
	src, err := transcode.MonoFromPCM16(samplesToPCM(nativeTestSamples(24000)), 24000, 0)
	require.NoError(t, err)
	return src
}

func TestRenderNative_OverwritesStaleTemp(t *testing.T) {
	gen, env := newNativeGenerator(t)
	temp := filepath.Join(env.TempDir, "stale.png")
	require.NoError(t, os.WriteFile(temp, bytes.Repeat([]byte("stale"), 1000), 0o600))

	err := gen.renderNative(t.Context(), env.Settings, nativeTestSource(t), 0, temp, nativeTestWidth, false, nativeOperationFile, 0, false, 0)

	require.NoError(t, err)
	requireNativePNG(t, temp)
}

// A symlink left at the temp name is replaced, never written through.
func TestRenderNative_DoesNotWriteThroughASymlink(t *testing.T) {
	gen, env := newNativeGenerator(t)
	victim := filepath.Join(env.TempDir, "victim.txt")
	require.NoError(t, os.WriteFile(victim, []byte("keep"), 0o600))
	temp := filepath.Join(env.TempDir, "link.png")
	if err := os.Symlink(victim, temp); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	err := gen.renderNative(t.Context(), env.Settings, nativeTestSource(t), 0, temp, nativeTestWidth, false, nativeOperationFile, 0, false, 0)

	require.NoError(t, err)
	got, readErr := os.ReadFile(victim) //nolint:gosec // G304: test path
	require.NoError(t, readErr)
	assert.Equal(t, "keep", string(got))
	requireNativePNG(t, temp)
}

// A temp name that cannot be removed (a non-empty directory) fails the render
// as a file error instead of being written over.
func TestRenderNative_RemoveFailureIsFileIO(t *testing.T) {
	gen, env := newNativeGenerator(t)
	temp := filepath.Join(env.TempDir, "dir.png")
	require.NoError(t, os.MkdirAll(temp, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(temp, "inner"), []byte("x"), 0o600))

	err := gen.renderNative(t.Context(), env.Settings, nativeTestSource(t), 0, temp, nativeTestWidth, false, nativeOperationFile, 0, false, 0)

	require.Error(t, err)
	enhanced, ok := errors.AsType[*errors.EnhancedError](err)
	require.True(t, ok)
	// The exclusive create would also fail on a directory, so the step tells the
	// removal failure apart from the open failure.
	require.Equal(t, "remove", enhanced.GetContext()["step"])
	assert.Equal(t, errors.CategoryFileIO, enhanced.Category)
}

func TestNativeGate_PCMTransitions(t *testing.T) {
	skipWithoutPOSIXShell(t)
	gen, env := newNativeGenerator(t)
	stub, ran := markerSoxStub(t)
	env.Settings.Realtime.Audio.SoxPath = stub
	pcm := samplesToPCM(nativeTestSamples(24000))

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
				require.NoError(t, gen.GenerateFromPCM(t.Context(), pcm, out, nativeTestWidth, false, 24000))
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

func TestRenderNative_OpenFailureIsFileIO(t *testing.T) {
	gen, env := newNativeGenerator(t)
	temp := filepath.Join(env.TempDir, "no-such-dir", "out.png")

	err := gen.renderNative(t.Context(), env.Settings, nativeTestSource(t), 0, temp, nativeTestWidth, false, nativeOperationFile, 0, false, 0)

	require.Error(t, err)
	enhanced, ok := errors.AsType[*errors.EnhancedError](err)
	require.True(t, ok)
	// Removing a path that does not exist is ignored, so the failure is the open.
	require.Equal(t, "open", enhanced.GetContext()["step"])
	assert.Equal(t, errors.CategoryFileIO, enhanced.Category)
	assert.Equal(t, "spectrogram", enhanced.GetComponent())
}

// failingWriter accepts n bytes and then fails every write.
type failingWriter struct{ n int }

var errWriterFull = errors.NewStd("writer full")

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, errWriterFull
	}
	k := min(len(p), w.n)
	w.n -= k
	if k < len(p) {
		return k, errWriterFull
	}
	return k, nil
}

func TestEncodePNG(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 256, 256), color.Palette{color.Black, color.White})
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 2) //nolint:gosec // G115: value is 0 or 1
	}

	var ok bytes.Buffer
	require.NoError(t, encodePNG(&ok, img))
	decoded, err := png.Decode(&ok)
	require.NoError(t, err)
	assert.Equal(t, img.Bounds(), decoded.Bounds())

	// This small image fits in the write buffer, so each limit fails at the final
	// flush. The encoder failure is covered by the larger image below.
	for _, limit := range []int{0, 10, 100} {
		require.ErrorIs(t, encodePNG(&failingWriter{n: limit}, img), errWriterFull, "limit %d", limit)
	}
}

// Encodes that run at the same time must each use their own encoder buffer, and
// a buffer reused for a later image of another size must not leak pixels from the
// earlier one. The images come from real renders at two sizes. The encoding
// goroutines are released together, and each cycles through every image, so the
// encodes overlap and the same goroutine encodes a narrow image after a wide one.
func TestEncodePNG_ConcurrentEncodesKeepTheirOwnPixels(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	gen, env := newNativeGenerator(t)
	const (
		rate       = 24000
		cyclesEach = 5
		wideWidth  = 514
		toneSecs   = 1
		goroutineN = 4
	)
	inputs := []struct {
		name   string
		width  int
		toneHz float64
	}{
		{"low narrow", nativeTestWidth, 1500},
		{"high wide", wideWidth, 6000},
		{"mid narrow", nativeTestWidth, 3000},
		{"top wide", wideWidth, 9000},
	}
	refs := make([]*image.Paletted, len(inputs))
	for i, in := range inputs {
		path := filepath.Join(env.TempDir, fmt.Sprintf("ref%d.png", i))
		pcm := samplesToPCM(toneSamples(rate, toneSecs, in.toneHz))
		require.NoError(t, gen.GenerateFromPCM(t.Context(), pcm, path, in.width, true, rate), in.name)
		refs[i] = decodePNG(t, path)
	}

	// decoded is one encode result and the index of the reference it must equal.
	type decoded struct {
		ref int
		img *image.Paletted
	}
	results := make([][]decoded, goroutineN)
	errs := make([]error, goroutineN)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := range goroutineN {
		wg.Go(func() {
			<-start
			for range cyclesEach {
				for j := range refs {
					ref := (g + j) % len(refs)
					var out bytes.Buffer
					if err := encodePNG(&out, refs[ref]); err != nil {
						errs[g] = err
						return
					}
					img, err := png.Decode(&out)
					if err != nil {
						errs[g] = err
						return
					}
					got, ok := img.(*image.Paletted)
					if !ok {
						errs[g] = errors.NewStd(fmt.Sprintf("decoded %T, want *image.Paletted", img))
						return
					}
					results[g] = append(results[g], decoded{ref: ref, img: got})
				}
			}
		})
	}
	close(start)
	wg.Wait()

	for g := range goroutineN {
		require.NoError(t, errs[g], "goroutine %d", g)
		require.Len(t, results[g], cyclesEach*len(refs), "goroutine %d", g)
		for _, d := range results[g] {
			want := refs[d.ref]
			assert.Equal(t, want.Rect, d.img.Rect, inputs[d.ref].name)
			assert.Equal(t, want.Palette, d.img.Palette, inputs[d.ref].name)
			assert.Equal(t, want.Pix, d.img.Pix, inputs[d.ref].name)
		}
	}
}

// An image that does not compress below the write buffer overflows it while the
// encoder is still writing. bufio keeps its first error and returns it again
// from Flush, so this pins that the failure is returned, not which call raised it.
func TestEncodePNG_FailureWhileEncoding(t *testing.T) {
	const side = 600
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.Gray{Y: uint8(i)} //nolint:gosec // G115: i < 256
	}
	img := image.NewPaletted(image.Rect(0, 0, side, side), pal)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: deterministic test data
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.IntN(256)) //nolint:gosec // G115: value is below 256
	}

	require.ErrorIs(t, encodePNG(&failingWriter{n: 0}, img), errWriterFull)
}

func TestSelectedRendererFollowsTheGate(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "")
	name, native := selectedRenderer()
	assert.Equal(t, soxRendererName, name)
	assert.False(t, native)

	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	name, native = selectedRenderer()
	assert.Equal(t, nativeRendererName, name)
	assert.True(t, native)
}
