package transcode

import (
	"bytes"
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// decodeBytes writes data to a file named for ext and decodes it back.
func decodeBytes(t *testing.T, data []byte, ext string) *pcmBuffer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out"+ext)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	pcm, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)
	return pcm
}

func TestEncode_PerFormatMagicAndRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format string
		ext    string
		magic  func(b []byte) bool
	}{
		{audioformat.WAV, ".wav", func(b []byte) bool { return bytes.HasPrefix(b, []byte("RIFF")) }},
		{audioformat.FLAC, ".flac", func(b []byte) bool { return bytes.HasPrefix(b, []byte("fLaC")) }},
		{audioformat.MP3, ".mp3", func(b []byte) bool {
			return bytes.HasPrefix(b, []byte("ID3")) || (len(b) > 1 && b[0] == 0xFF && b[1]&0xE0 == 0xE0)
		}},
		{audioformat.AAC, ".m4a", func(b []byte) bool { return len(b) > 8 && bytes.Equal(b[4:8], []byte("ftyp")) }},
		{audioformat.Opus, ".opus", func(b []byte) bool { return bytes.HasPrefix(b, []byte("OggS")) }},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()
			src := tone(testRate, 1, 1, testToneHz, testAmp)
			data, err := encode(t.Context(), newPCM(src, testRate, 1), tt.format, t.TempDir())
			require.NoError(t, err)
			assert.Truef(t, tt.magic(data), "unexpected file header % x", data[:min(len(data), 12)])

			back := decodeBytes(t, data, tt.ext)
			assert.Equal(t, testRate, back.SampleRate)
			assert.Equal(t, 1, back.Channels)
			assert.InDelta(t, len(src), back.Frames(), lossyFrameSlack)
			assert.True(t, dominantIs(channelOf(bytesInt16(back.Data), 1, 0), testRate, testToneHz, testOffToneH))
			if tt.format == audioformat.WAV || tt.format == audioformat.FLAC {
				assert.Equal(t, int16Bytes(src), back.Data, "lossless formats round-trip exactly")
			}
		})
	}
}

func TestEncode_UnsupportedRateResamplesTo48k(t *testing.T) {
	t.Parallel()

	const lossyRateSlack = 4200
	tests := []struct {
		name     string
		rate     int
		format   string
		wantRate int
	}{
		{"44.1k to opus", 44100, audioformat.Opus, 48000},
		{"256k to mp3", 256000, audioformat.MP3, 48000},
		{"256k to aac", 256000, audioformat.AAC, 48000},
		{"256k to opus", 256000, audioformat.Opus, 48000},
		{"44.1k to mp3 stays", 44100, audioformat.MP3, 44100},
		{"44.1k to aac stays", 44100, audioformat.AAC, 44100},
		{"256k to wav stays", 256000, audioformat.WAV, 256000},
		{"256k to flac stays", 256000, audioformat.FLAC, 256000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			const seconds = 0.5
			src := tone(tt.rate, 1, seconds, testToneHz, testAmp)
			data, err := encode(t.Context(), newPCM(src, tt.rate, 1), tt.format, t.TempDir())
			require.NoError(t, err)

			ext := map[string]string{"wav": ".wav", "flac": ".flac", "mp3": ".mp3", "aac": ".m4a", "opus": ".opus"}[tt.format]
			back := decodeBytes(t, data, ext)
			assert.Equal(t, tt.wantRate, back.SampleRate)
			assert.InDelta(t, seconds*float64(tt.wantRate), back.Frames(), lossyRateSlack)
		})
	}
}

func TestEncode_FLACAboveMaxRateResamples(t *testing.T) {
	t.Parallel()

	const flacLimit = 655350 // pins the library's flacpcm.MaxSampleRate, the highest rate the FLAC encoder accepts
	assert.True(t, EncoderAcceptsRate(audioformat.FLAC, flacLimit, outputBitDepth, 1), "the FLAC limit itself is carried")
	assert.False(t, EncoderAcceptsRate(audioformat.FLAC, flacLimit+1, outputBitDepth, 1), "a rate above the FLAC limit is not carried")

	const (
		rate    = 768000 // above the FLAC limit, below the source rate bound
		seconds = 0.25
	)
	src := tone(rate, 1, seconds, testToneHz, testAmp)
	data, err := encode(t.Context(), newPCM(src, rate, 1), audioformat.FLAC, t.TempDir())
	require.NoError(t, err, "the FLAC encoder rejects this rate, so it must be resampled first")

	back := decodeBytes(t, data, ".flac")
	assert.Equal(t, conf.SampleRate, back.SampleRate)
}

func TestEncode_TooShortForResamplerIsEmptyRange(t *testing.T) {
	t.Parallel()

	// A few frames at a rate the Opus encoder does not carry: the resampler's
	// latency swallows all of them, so nothing is left to encode.
	const frames = 8
	p := &pcmBuffer{Data: make([]byte, frames*2), SampleRate: 44100, Channels: 1}
	_, err := encode(t.Context(), p, audioformat.Opus, t.TempDir())
	require.ErrorIs(t, err, ErrEmptyRange)
}

func TestEncode_UpsampleOverCap(t *testing.T) {
	t.Parallel()

	// 11.025 kHz is not an MP3 encoder rate, so the clip would be upsampled to
	// 48 kHz; enough of it exceeds maxDecodedPCMBytes once resampled.
	const rate = 11025
	frames := maxDecodedPCMBytes/2*rate/48000 + rate
	p := &pcmBuffer{Data: make([]byte, frames*2), SampleRate: rate, Channels: 1}
	_, err := encode(t.Context(), p, audioformat.MP3, t.TempDir())
	require.ErrorIs(t, err, ErrAudioTooLong)
}

func TestEncode_LeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	entries := func(dir string) int {
		list, err := os.ReadDir(dir)
		require.NoError(t, err)
		return len(list)
	}
	src := tone(testRate, 1, 0.5, testToneHz, testAmp)

	for _, format := range []string{audioformat.FLAC, audioformat.MP3, audioformat.AAC, audioformat.Opus} {
		dir := t.TempDir()
		_, err := encode(t.Context(), newPCM(src, testRate, 1), format, dir)
		require.NoError(t, err)
		assert.Zerof(t, entries(dir), "%s success", format)

		// A failing encode: the lossy encoders reject three channels, and the
		// FLAC encoder rejects a trailing partial frame.
		bad := &pcmBuffer{Data: make([]byte, 6000), SampleRate: testRate, Channels: 3}
		if format == audioformat.FLAC {
			bad = &pcmBuffer{Data: make([]byte, 6001), SampleRate: testRate, Channels: 1}
		}
		failDir := t.TempDir()
		_, err = encode(t.Context(), bad, format, failDir)
		require.Errorf(t, err, "%s with malformed input must fail", format)
		assert.Zerof(t, entries(failDir), "%s failure", format)
	}
}

func TestEncode_Rejections(t *testing.T) {
	t.Parallel()

	src := newPCM(tone(testRate, 1, 0.1, testToneHz, testAmp), testRate, 1)
	for _, format := range []string{"alac", "ALAC", "", "m4a", "ogg"} {
		_, err := encode(t.Context(), src, format, t.TempDir())
		require.ErrorIsf(t, err, ErrInvalidRequest, "format %q", format)
	}
	_, err := encode(t.Context(), &pcmBuffer{SampleRate: testRate, Channels: 1}, audioformat.WAV, "")
	require.ErrorIs(t, err, ErrEmptyRange)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = encode(ctx, src, audioformat.FLAC, t.TempDir())
	require.ErrorIs(t, err, context.Canceled)
}

func TestEncoderTag(t *testing.T) {
	t.Parallel()

	assert.Equal(t, clipenc.NativeWAV, EncoderTag(audioformat.WAV))
	assert.Equal(t, clipenc.NativeFLAC, EncoderTag(audioformat.FLAC))
	assert.Equal(t, clipenc.NativeMP3, EncoderTag(audioformat.MP3))
	assert.Equal(t, clipenc.NativeAAC, EncoderTag(audioformat.AAC))
	assert.Equal(t, clipenc.NativeOpus, EncoderTag(audioformat.Opus))
	assert.Equal(t, clipenc.NativeWAV, EncoderTag("unknown"))
}

// A WAV built in place in front of the samples must equal the one the encoder
// writes by copying them.
func TestEncodeWAV_InPlaceMatchesTheCopyingPath(t *testing.T) {
	t.Parallel()

	for _, channels := range []int{1, 2} {
		src := newPCM(tone(testRate, channels, 0.3, testToneHz, testAmp), testRate, channels)
		want, err := encodeWAV(src)
		require.NoError(t, err)

		room := make([]byte, wavHeaderBytes+len(src.Data))
		copy(room[wavHeaderBytes:], src.Data)
		inPlace := &pcmBuffer{Data: room[wavHeaderBytes:], SampleRate: testRate, Channels: channels, wavRoom: room}
		got, err := encodeWAV(inPlace)
		require.NoError(t, err)

		assert.Equal(t, want, got, "%d channels", channels)
		assert.Same(t, &room[0], &got[0], "the WAV is built in the room buffer")
	}
}

func TestEncode_AppliesTheClipBitrates(t *testing.T) {
	t.Parallel()

	// Noise keeps the encoders at their target rate instead of letting them idle.
	const (
		seconds   = 4.0
		noiseSD   = 6000
		tolerance = 0.15 // encoder framing and container overhead
	)
	rng := rand.New(rand.NewPCG(noiseSeed, noiseSeed))
	noise := make([]int16, int(seconds*testRate))
	for i := range noise {
		noise[i] = int16(rng.NormFloat64() * noiseSD)
	}

	// MP3 is not pinned by its output size: the encoder's own default equals the
	// configured 128 kbps, so only the configured value is asserted.
	assert.Equal(t, 128, clipBitrateKbps[audioformat.MP3])

	for _, format := range []string{audioformat.AAC, audioformat.Opus} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			data, err := encode(t.Context(), newPCM(noise, testRate, 1), format, t.TempDir())
			require.NoError(t, err)
			wantBytes := float64(clipBitrateKbps[format]) * 1000 / 8 * seconds
			assert.InEpsilon(t, wantBytes, float64(len(data)), tolerance, "the configured %s bitrate reaches the encoder", format)
		})
	}
}
