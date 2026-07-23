package hlsmux

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	m4a "github.com/tphakala/go-m4a"
)

// The rest of this package's tests deliberately run against a fake codec, to
// prove the muxer is codec neutral rather than merely claiming to be. These
// tests are the complement: they pin the one concrete codec that actually
// ships, so a change in go-aac's reported priming or AudioSpecificConfig
// surfaces here rather than as silently misaligned audio in a browser.

// ascMono48k is the two-byte MPEG-4 AudioSpecificConfig for AAC-LC, 48 kHz,
// one channel: object type 2 (AAC-LC) in the top 5 bits, sampling frequency
// index 3 (48000) in the next 4, channel configuration 1 in the next 4.
var ascMono48k = []byte{0x11, 0x88}

// tone generates n samples of a sine at hz, as interleaved little-endian
// signed 16-bit PCM. Real signal rather than silence: an encoder handed pure
// zeros can produce degenerate frames that hide framing bugs.
func tone(n, channels, rate int, hz float64) []byte {
	buf := make([]byte, n*channels*bytesPerSample)
	for i := range n {
		v := int16(math.Round(8000 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))))
		for ch := range channels {
			off := (i*channels + ch) * bytesPerSample
			buf[off] = byte(v)
			buf[off+1] = byte(v >> 8)
		}
	}
	return buf
}

func TestAACLCCodecShape(t *testing.T) {
	t.Parallel()

	codec := AACLC()
	assert.Equal(t, "aac-lc", codec.Name)
	assert.Equal(t, aacFrame, codec.MaxFrameSamples,
		"MaxFrameSamples must be the AAC-LC access unit size, or New cannot reject a too-short segment")
	assert.True(t, codec.valid(), "AACLC must be fully constructed")
}

func TestAACLCEncoderReportsASCAndPriming(t *testing.T) {
	t.Parallel()

	enc, err := newAACEncoder(EncoderConfig{
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = enc.Close() })

	assert.Equal(t, ascMono48k, enc.DecoderConfig(),
		"the esds AudioSpecificConfig must describe AAC-LC 48 kHz mono")
	assert.Equal(t, aacFrame, enc.Delay(),
		"AAC-LC primes by exactly one frame; the container trims it with an edit list")
}

// TestAACLCWriterConfigCarriesPriming pins the mapping from the encoder's
// reported delay onto go-m4a's EncoderDelay.
func TestAACLCWriterConfigCarriesPriming(t *testing.T) {
	t.Parallel()

	encCfg := EncoderConfig{SampleRate: testRate, Channels: testChannels, BitrateKbps: testBitrate}
	enc, err := newAACEncoder(encCfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = enc.Close() })

	cfg, err := aacWriterConfig(encCfg, enc)
	require.NoError(t, err)

	assert.Equal(t, m4a.CodecAACLC, cfg.Codec)
	assert.Equal(t, testRate, cfg.SampleRate)
	assert.Equal(t, testChannels, cfg.Channels)
	assert.Equal(t, ascMono48k, cfg.ASC)
	assert.Equal(t, aacFrame, cfg.EncoderDelay)
	assert.Zero(t, cfg.MediaLength,
		"a live stream has no known length, and go-m4a's fragmented constructors reject a non-zero one")
}

// TestAACLCZeroPrimingSuppressesEditList covers the one mapping that is easy to
// get wrong and impossible to notice. go-m4a reads EncoderDelay zero as "use the
// codec default", which for AAC-LC is 1024, so forwarding a reported zero would
// trim a frame of real audio that was never primed and shift the whole timeline.
// NoEdit is the value that actually means "no priming".
func TestAACLCZeroPrimingSuppressesEditList(t *testing.T) {
	t.Parallel()

	encCfg := EncoderConfig{SampleRate: testRate, Channels: testChannels, BitrateKbps: testBitrate}
	cfg, err := aacWriterConfig(encCfg, stubEncoder{asc: ascMono48k, delay: 0})
	require.NoError(t, err)
	assert.Equal(t, m4a.NoEdit, cfg.EncoderDelay,
		"zero reported priming must suppress the edit list, not select the codec default")
}

func TestAACLCRejectsEmptyDecoderConfig(t *testing.T) {
	t.Parallel()

	encCfg := EncoderConfig{SampleRate: testRate, Channels: testChannels, BitrateKbps: testBitrate}
	_, err := aacWriterConfig(encCfg, stubEncoder{asc: nil, delay: aacFrame})
	require.Error(t, err, "an empty ASC would produce an esds no decoder could initialise from")
	assert.Contains(t, err.Error(), "AudioSpecificConfig")
}

func TestAACLCRejectsUnsupportedSampleRate(t *testing.T) {
	t.Parallel()

	// go-aac supports 44100 and 48000 only. The failure must arrive from New,
	// not from the first Write on the audio goroutine.
	_, err := New(&Config{
		Codec:       AACLC(),
		SampleRate:  22050,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	})
	require.Error(t, err)
}

// TestAACLCStreamProducesPlayableSegments runs real audio through the real
// encoder and the real container, which no other test in this package does.
func TestAACLCStreamProducesPlayableSegments(t *testing.T) {
	t.Parallel()

	s, err := New(&Config{
		Codec:       AACLC(),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// The init segment is built during New, so a valid one here proves the ASC
	// and the sample entry agree before any audio is written.
	init := s.InitSegment()
	require.NotEmpty(t, init)
	assert.Equal(t, "ftyp", string(init[4:8]), "an init segment starts with ftyp")

	// Six seconds at the default two-second segment length. Fed in 1200-sample
	// chunks, which deliberately do not divide the 1024-sample access unit, so
	// the encoder's partial-frame buffering is exercised on every write rather
	// than by luck.
	const (
		chunkSamples = 1200
		totalSamples = 6 * testRate
	)
	written := 0
	for written < totalSamples {
		n := min(chunkSamples, totalSamples-written)
		require.NoError(t, s.Write(tone(n, testChannels, testRate, 3000), sampleTime(written)))
		written += n
	}

	stats := s.Stats()
	require.False(t, stats.Failed, "encoding must not have latched a failure")
	require.GreaterOrEqual(t, stats.Segments, uint64(2), "six seconds must yield at least two segments")

	seg, ok := s.Segment(0)
	require.True(t, ok, "the first segment must still be in the window")
	assert.Equal(t, "styp", string(seg.Data[4:8]), "a media segment starts with styp")
	assert.Positive(t, seg.Samples)
	assert.Positive(t, seg.Duration)
	assert.False(t, seg.Discontinuity, "a steadily fed source must not declare a break")

	// Every segment is cut at or below the target, never above, so the playlist
	// can advertise the target duration honestly.
	assert.LessOrEqual(t, seg.Duration, DefaultSegmentDuration)

	playlist := s.Playlist()
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-MAP:URI=\""+InitSegmentName+"\"")
	assert.Contains(t, playlist, SegmentName(0))
	assert.Contains(t, playlist, "#EXT-X-PROGRAM-DATE-TIME")
	assert.NotContains(t, playlist, "CODECS",
		"CODECS belongs to a master playlist; this is a media playlist")
	assert.Equal(t, strings.Count(playlist, "#EXTINF"), s.segments.len())
}

// stubEncoder reports a fixed decoder config and priming without encoding
// anything, so the writer-config mapping can be tested at values the real
// encoder never produces.
type stubEncoder struct {
	asc   []byte
	delay int
}

func (s stubEncoder) EncodeInterleaved(_ []byte, _ EmitFunc) error { return nil }
func (s stubEncoder) Flush(_ EmitFunc) error                       { return nil }
func (s stubEncoder) DecoderConfig() []byte                        { return s.asc }
func (s stubEncoder) Delay() int                                   { return s.delay }
func (s stubEncoder) Close() error                                 { return nil }
