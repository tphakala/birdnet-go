package transcode

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
)

func TestExtractClip_EndToEnd(t *testing.T) {
	t.Parallel()

	const (
		srcSeconds = noiseOnlySeconds + noiseToneSeconds
		start      = 1.0
		end        = 2.5
	)
	path := filepath.Join(t.TempDir(), "src.flac")
	writeFLACFile(t, path, noisyTone(testRate)[:int(srcSeconds*testRate)], testRate, 1, 16)

	ext := map[string]string{"wav": ".wav", "flac": ".flac", "mp3": ".mp3", "aac": ".m4a", "opus": ".opus"}
	filterSets := map[string]Filters{
		"plain":    {},
		"filtered": {Denoise: "medium", Normalize: true, GainDB: -3},
	}
	for _, format := range []string{audioformat.WAV, audioformat.FLAC, audioformat.MP3, audioformat.AAC, audioformat.Opus} {
		for name, filters := range filterSets {
			t.Run(format+"_"+name, func(t *testing.T) {
				t.Parallel()
				res, err := ExtractClip(t.Context(), &ClipOptions{
					InputPath: path, Start: start, End: end, Format: format, Filters: filters, TempDir: t.TempDir(),
				})
				require.NoError(t, err)
				assert.Equal(t, EncoderTag(format), res.Encoder)

				back := decodeBytes(t, res.Data, ext[format])
				assert.InDelta(t, (end-start)*testRate, back.Frames(), lossyFrameSlack)
				assert.True(t, dominantIs(channelOf(bytesInt16(back.Data), 1, 0), testRate, testToneHz, testOffToneH))
			})
		}
	}
}

func TestExtractClip_Validation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.wav")
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)
	base := ClipOptions{InputPath: path, Start: 0, End: 0.5, Format: audioformat.WAV, TempDir: t.TempDir()}

	mutations := map[string]func(o *ClipOptions){
		"negative start":   func(o *ClipOptions) { o.Start = -1 },
		"nan start":        func(o *ClipOptions) { o.Start = math.NaN() },
		"end before start": func(o *ClipOptions) { o.Start, o.End = 1, 0.5 },
		"infinite end":     func(o *ClipOptions) { o.End = math.Inf(1) },
		"too long":         func(o *ClipOptions) { o.End = MaxClipDurationSec + 1 },
		"alac":             func(o *ClipOptions) { o.Format = "alac" },
		"bad preset":       func(o *ClipOptions) { o.Filters.Denoise = "x" },
		"gain too high":    func(o *ClipOptions) { o.Filters.GainDB = MaxGainDB + 1 },
		"nan gain":         func(o *ClipOptions) { o.Filters.GainDB = math.NaN() },
	}
	for name, mutate := range mutations {
		opts := base
		mutate(&opts)
		_, err := ExtractClip(t.Context(), &opts)
		require.ErrorIsf(t, err, ErrInvalidRequest, name)
	}
	_, err := ExtractClip(t.Context(), nil)
	require.ErrorIs(t, err, ErrInvalidRequest)

	// The longest allowed window is accepted by validation.
	opts := base
	opts.End = MaxClipDurationSec
	_, err = ExtractClip(t.Context(), &opts)
	assert.NotErrorIs(t, err, ErrInvalidRequest)
}

func TestExtractClip_StartPastEnd(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.wav")
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)
	_, err := ExtractClip(t.Context(), &ClipOptions{InputPath: path, Start: 30, End: 31, Format: audioformat.WAV})
	require.ErrorIs(t, err, ErrEmptyRange)
}

func TestClipTimeout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, clipMinTimeout, clipTimeout(1))
	assert.Equal(t, 100*time.Second, clipTimeout(50))
	assert.Equal(t, clipMaxTimeout, clipTimeout(MaxClipDurationSec))
}

func TestProcessToWAV(t *testing.T) {
	t.Parallel()

	const rate = 44100
	path := filepath.Join(t.TempDir(), "x.flac")
	writeFLACFile(t, path, noisyTone(rate), rate, 1, 16)

	data, err := ProcessToWAV(t.Context(), path, Filters{Denoise: "light", Normalize: true, GainDB: 1})
	require.NoError(t, err)

	back := decodeBytes(t, data, ".wav")
	assert.Equal(t, rate, back.SampleRate, "the source rate is kept")
	assert.Equal(t, 1, back.Channels)
	assert.Equal(t, len(noisyTone(rate)), back.Frames())

	_, err = ProcessToWAV(t.Context(), path, Filters{Denoise: "nope"})
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = ProcessToWAV(t.Context(), filepath.Join(t.TempDir(), "missing.wav"), Filters{})
	require.Error(t, err)
}

func TestAudibleBats(t *testing.T) {
	t.Parallel()

	const (
		rate   = 256000
		factor = 10
	)
	path := filepath.Join(t.TempDir(), "bat.wav")
	writeWAVFile(t, path, tone(rate, 1, batSeconds, batToneHz, 0.05), rate, 1)

	data, err := AudibleBats(t.Context(), path, factor, Filters{Normalize: true})
	require.NoError(t, err)

	back := decodeBytes(t, data, ".wav")
	assert.Equal(t, AudibleBatsOutputSampleRate, back.SampleRate)
	assert.InDelta(t, batSeconds*float64(factor)*AudibleBatsOutputSampleRate, back.Frames(), expandLengthSlack)
	assert.True(t, dominantIs(channelOf(bytesInt16(back.Data), 1, 0), AudibleBatsOutputSampleRate, batToneHz/factor, batToneHz/factor*1.7))
	assert.InDelta(t, normalizeTargetLUFS, lufsOf(t, back).IntegratedLUFS, normalizeToleranceLU+0.5, "normalize runs after expansion")

	_, err = AudibleBats(t.Context(), path, 7, Filters{})
	require.ErrorIs(t, err, ErrInvalidRequest)
	_, err = AudibleBats(t.Context(), path, factor, Filters{Denoise: "light"})
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestProcessingTempDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dir, err := ProcessingTempDir(base)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, ".tmp-processing"), dir)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	again, err := ProcessingTempDir(base)
	require.NoError(t, err)
	assert.Equal(t, dir, again, "idempotent")
}
