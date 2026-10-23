package transcode

import (
	"context"
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
	writeEncoded(t, audioformat.FLAC, 0, path, noisyTone(testRate)[:int(srcSeconds*testRate)], testRate)

	ext := map[string]string{"wav": ".wav", "flac": ".flac", "mp3": ".mp3", "aac": ".m4a", "opus": ".opus"}
	filterSets := map[string]Filters{
		"plain":    {},
		"filtered": {Denoise: "medium", Normalize: true, GainDB: -3},
	}
	for _, format := range []string{audioformat.WAV, audioformat.FLAC, audioformat.MP3, audioformat.AAC, audioformat.Opus} {
		for name, filters := range filterSets {
			t.Run(format+"_"+name, func(t *testing.T) {
				t.Parallel()
				data, err := ExtractClip(t.Context(), &ClipOptions{
					InputPath: path, Start: start, End: end, Format: format, Filters: filters, TempDir: t.TempDir(),
				})
				require.NoError(t, err)

				back := decodeBytes(t, data, ext[format])
				assert.InDelta(t, (end-start)*testRate, back.Frames(), lossyFrameSlack)
				assert.True(t, dominantIs(channelOf(bytesInt16(back.Data), 1, 0), testRate, testToneHz, testOffToneH))
			})
		}
	}
}

func TestExtractClip_Validation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.wav")
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)
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
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)
	_, err := ExtractClip(t.Context(), &ClipOptions{InputPath: path, Start: 30, End: 31, Format: audioformat.WAV})
	require.ErrorIs(t, err, ErrEmptyRange)
}

func TestClipTimeout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, clipMinTimeout, clipTimeout(1))
	assert.Equal(t, 100*time.Second, clipTimeout(50))
	assert.Equal(t, clipMaxTimeout, clipTimeout(MaxClipDurationSec))
}

func TestRun_AppliesThePerJobTimeout(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "src.wav")
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)

	// A timeout that has already elapsed cancels the job context at once, with no
	// timer race on a coarse clock.
	_, err := run(t.Context(), &job{path: path, timeout: -time.Second, format: audioformat.WAV})
	require.ErrorIs(t, err, context.DeadlineExceeded, "a job past its own timeout stops even though the caller's context is live")
}

func TestPipelineTimeouts(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 60*time.Second, processingTimeout)
	assert.Equal(t, 120*time.Second, audibleBatsTimeout)
}

func TestProcessToWAV(t *testing.T) {
	t.Parallel()

	const rate = 44100
	path := filepath.Join(t.TempDir(), "x.flac")
	writeEncoded(t, audioformat.FLAC, 0, path, noisyTone(rate), rate)

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
	writeWAVFile(t, path, tone(rate, 1, batSeconds, batToneHz, 0.05), rate)

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

func TestExtractClip_AppliesWindowStartAndFilters(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "src.wav")
	src := noisyTone(testRate)
	writeWAVFile(t, path, src, testRate)

	clip := func(start, end float64, f Filters) *pcmBuffer {
		data, err := ExtractClip(t.Context(), &ClipOptions{InputPath: path, Start: start, End: end, Format: audioformat.WAV, Filters: f})
		require.NoError(t, err)
		return decodeBytes(t, data, ".wav")
	}

	t.Run("the window starts at Start", func(t *testing.T) {
		t.Parallel()
		got := clip(1, 2, Filters{})
		want := int16Bytes(src[testRate : 2*testRate])
		assert.Equal(t, want, got.Data)
	})

	t.Run("the filters reach the encoded clip", func(t *testing.T) {
		t.Parallel()
		const halfGainDB = -6.0206
		plain := clip(1, 2, Filters{})
		quieter := clip(1, 2, Filters{GainDB: halfGainDB})
		assert.InDelta(t, peakAbs(bytesInt16(plain.Data))/2, peakAbs(bytesInt16(quieter.Data)), 2)
	})
}

func TestProcessToWAV_AppliesFilters(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "src.wav")
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)

	plain, err := ProcessToWAV(t.Context(), path, Filters{})
	require.NoError(t, err)
	quieter, err := ProcessToWAV(t.Context(), path, Filters{GainDB: -6.0206})
	require.NoError(t, err)

	plainPeak := peakAbs(bytesInt16(decodeBytes(t, plain, ".wav").Data))
	quieterPeak := peakAbs(bytesInt16(decodeBytes(t, quieter, ".wav").Data))
	assert.InDelta(t, plainPeak/2, quieterPeak, 2)
}
