package stream

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	aacpcm "github.com/tphakala/go-aac/pcm"
	audiostream "github.com/tphakala/go-audio-stream"
	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-opus/opus"
)

// ascAACLC48kMono is a valid AAC-LC AudioSpecificConfig: object type 2, sample
// rate index 3 (48000 Hz), channel config 1 (mono).
var ascAACLC48kMono = []byte{0x11, 0x88}

// ascAACMain48kMono is object type 1 (Main), which the LC-only decoder rejects.
var ascAACMain48kMono = []byte{0x09, 0x88}

func TestNewFrameDecoder_dispatch(t *testing.T) {
	pcmFormat := func(rate, ch int) audiostream.AudioFormat {
		return audiostream.AudioFormat{Kind: audiostream.KindPCMS16LE, SampleRate: rate, Channels: ch}
	}
	tests := []struct {
		name    string
		codec   audiostream.Codec
		format  audiostream.AudioFormat
		wantErr error
		verify  func(t *testing.T, d frameDecoder)
	}{
		{
			name:   "g711 mu-law passthrough",
			codec:  audiostream.CodecG711{Law: audiostream.MuLaw},
			format: pcmFormat(8000, 1),
			verify: func(t *testing.T, d frameDecoder) {
				t.Helper()
				pt, ok := d.(*passthroughDecoder)
				require.True(t, ok, "want passthroughDecoder")
				assert.Equal(t, 8000, pt.sampleRate)
				assert.Equal(t, 1, pt.channels)
			},
		},
		{
			name:   "l16 stereo passthrough",
			codec:  audiostream.CodecL16{ClockRate: 48000, Channels: 2},
			format: pcmFormat(48000, 2),
			verify: func(t *testing.T, d frameDecoder) {
				t.Helper()
				pt, ok := d.(*passthroughDecoder)
				require.True(t, ok, "want passthroughDecoder")
				assert.Equal(t, 48000, pt.sampleRate)
				assert.Equal(t, 2, pt.channels)
			},
		},
		{
			name:  "opus decoder",
			codec: audiostream.CodecOpus{},
			verify: func(t *testing.T, d frameDecoder) {
				t.Helper()
				_, ok := d.(*opusFrameDecoder)
				assert.True(t, ok, "want opusFrameDecoder")
			},
		},
		{
			name:  "mp3 decoder",
			codec: audiostream.CodecMP3{},
			verify: func(t *testing.T, d frameDecoder) {
				t.Helper()
				_, ok := d.(*mp3FrameDecoder)
				assert.True(t, ok, "want mp3FrameDecoder")
			},
		},
		{
			name:  "aac-lc decoder learns geometry from asc",
			codec: audiostream.CodecAAC{AudioSpecificConfig: ascAACLC48kMono},
			verify: func(t *testing.T, d frameDecoder) {
				t.Helper()
				ad, ok := d.(*aacFrameDecoder)
				require.True(t, ok, "want aacFrameDecoder")
				assert.Equal(t, 48000, ad.dec.SampleRate())
				assert.Equal(t, 1, ad.dec.Channels())
			},
		},
		{
			name:    "aac main profile is unsupported",
			codec:   audiostream.CodecAAC{AudioSpecificConfig: ascAACMain48kMono},
			wantErr: ErrUnsupportedCodec,
		},
		{
			name:    "flac is unsupported",
			codec:   audiostream.CodecFLAC{},
			wantErr: ErrUnsupportedCodec,
		},
		{
			name:    "unknown codec is unsupported",
			codec:   audiostream.CodecUnknown{},
			wantErr: ErrUnsupportedCodec,
		},
		{
			name:    "latm without resolved config is pending, not terminal",
			codec:   audiostream.CodecMP4ALATM{},
			wantErr: errCodecConfigPending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := newFrameDecoder(tt.codec, tt.format)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, d)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, d)
			if tt.verify != nil {
				tt.verify(t, d)
			}
		})
	}
}

func TestCodecName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		codec audiostream.Codec
		want  string
	}{
		{"opus", audiostream.CodecOpus{}, "opus"},
		{"mp3", audiostream.CodecMP3{}, "mp3"},
		{"aac-lc", audiostream.CodecAAC{}, "aac-lc"},
		{"mp4a-latm", audiostream.CodecMP4ALATM{}, "aac-lc"},
		{"g711 mu-law", audiostream.CodecG711{Law: audiostream.MuLaw}, "pcmu"},
		{"g711 a-law", audiostream.CodecG711{Law: audiostream.ALaw}, "pcma"},
		{"g726", audiostream.CodecG726{}, "g726"},
		{"l16", audiostream.CodecL16{}, "l16"},
		{"flac", audiostream.CodecFLAC{}, "flac"},
		{"unknown", audiostream.CodecUnknown{}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, codecName(tt.codec))
		})
	}
}

func TestPassthroughDecoder_returnsInputAndGeometry(t *testing.T) {
	d := &passthroughDecoder{sampleRate: 48000, channels: 1}
	in := []byte{1, 2, 3, 4}
	pcm, rate, ch, err := d.decodeFrame(in)
	require.NoError(t, err)
	assert.Equal(t, in, pcm)
	assert.Equal(t, 48000, rate)
	assert.Equal(t, 1, ch)
}

func TestFloat32ToS16LE_scalesRoundsAndClamps(t *testing.T) {
	dst := make([]byte, 4*2)
	float32ToS16LE(dst, []float32{0, 1.0, -1.0, 2.0})
	got := []int16{
		int16(binary.LittleEndian.Uint16(dst[0:])),
		int16(binary.LittleEndian.Uint16(dst[2:])),
		int16(binary.LittleEndian.Uint16(dst[4:])),
		int16(binary.LittleEndian.Uint16(dst[6:])),
	}
	assert.Equal(t, int16(0), got[0], "silence")
	assert.Equal(t, int16(32767), got[1], "full-scale positive")
	assert.Equal(t, int16(-32767), got[2], "full-scale negative")
	assert.Equal(t, int16(32767), got[3], "clamps above +1.0")
}

// TestOpusDecodeRoundTrip encodes a mono 48 kHz tone with go-opus and decodes it
// back through the stream's opusFrameDecoder, asserting geometry and that the
// tone survives. Standalone (no MediaMTX); complements the integration-tagged
// PCMFidelity contract case.
func TestOpusDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	const rate, ch, frame = 48000, 1, 960 // 20 ms at 48 kHz

	enc, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: rate, Channels: ch, Bitrate: 64000})
	require.NoError(t, err)
	dec, err := newOpusFrameDecoder()
	require.NoError(t, err)

	var energy int64
	var gotRate, gotCh int
	buf := make([]byte, 4000)
	for f := range 6 {
		n, encErr := enc.Encode(toneInt16(frame, rate, 440, f*frame), buf)
		require.NoError(t, encErr)
		require.Positive(t, n)
		out, r, c, decErr := dec.decodeFrame(buf[:n])
		require.NoError(t, decErr)
		gotRate, gotCh = r, c
		assert.Len(t, out, frame*2, "20 ms mono frame decodes to 960 s16le samples")
		energy += pcmEnergy(out)
	}
	assert.Equal(t, rate, gotRate)
	assert.Equal(t, ch, gotCh)
	assert.Positive(t, energy, "decoded opus must carry the tone")
}

// TestMP3DecodeRoundTrip encodes a mono 48 kHz tone with go-mp3 and decodes it
// back through the stream's mp3FrameDecoder. MP3 carries an encoder/decoder delay,
// so the tone is asserted over the accumulated output rather than the first frame.
func TestMP3DecodeRoundTrip(t *testing.T) {
	t.Parallel()
	const rate, ch, frame = 48000, 1, 1152 // MPEG-1 samples per frame

	enc, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: rate, Channels: ch, Bitrate: 128000})
	require.NoError(t, err)
	dec := newMP3FrameDecoder()

	var energy int64
	var gotRate, gotCh int
	for f := range 12 {
		mp3Frame, encErr := enc.EncodeFrame(nil, [][]float32{toneFloat32(frame, rate, 440, f*frame)})
		require.NoError(t, encErr)
		if len(mp3Frame) == 0 {
			continue
		}
		out, r, c, decErr := dec.decodeFrame(mp3Frame)
		require.NoError(t, decErr)
		if r != 0 {
			gotRate, gotCh = r, c
		}
		energy += pcmEnergy(out)
	}
	assert.Equal(t, rate, gotRate)
	assert.Equal(t, ch, gotCh)
	assert.Positive(t, energy, "decoded mp3 must carry the tone after priming")
}

// TestAACDecodeRoundTrip encodes a mono 48 kHz tone to an AAC-LC ADTS stream with
// go-aac, strips the ADTS headers to raw access units, and decodes them through
// the stream's aacFrameDecoder built from the matching LC/48k/mono ASC.
func TestAACDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	const rate, ch, samples = 48000, 1, 8192

	var adts bytes.Buffer
	require.NoError(t, aacpcm.EncodeInterleaved(&adts, aacpcm.Config{
		SampleRate: rate,
		BitDepth:   16,
		Channels:   ch,
		Bitrate:    128000,
	}, toneInterleavedS16LE(samples, rate, 440)))

	aus := splitADTS(t, adts.Bytes())
	require.NotEmpty(t, aus, "encoder should emit at least one ADTS frame")

	dec, err := newAACFrameDecoder(ascAACLC48kMono)
	require.NoError(t, err)

	var energy int64
	var gotRate, gotCh int
	for _, au := range aus {
		out, r, c, decErr := dec.decodeFrame(au)
		require.NoError(t, decErr)
		if r != 0 {
			gotRate, gotCh = r, c
		}
		energy += pcmEnergy(out)
	}
	assert.Equal(t, rate, gotRate)
	assert.Equal(t, ch, gotCh)
	assert.Positive(t, energy, "decoded aac must carry the tone after priming")
}

// splitADTS parses an ADTS stream into its raw access units so they can be fed to
// the raw AAC decoder.
func splitADTS(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	var aus [][]byte
	for i := 0; i+7 <= len(stream); {
		require.Equal(t, byte(0xFF), stream[i], "ADTS syncword high")
		require.Equal(t, byte(0xF0), stream[i+1]&0xF0, "ADTS syncword low")
		protectionAbsent := stream[i+1]&0x01 == 1
		frameLen := int(stream[i+3]&0x03)<<11 | int(stream[i+4])<<3 | int(stream[i+5])>>5
		require.GreaterOrEqual(t, frameLen, 7, "ADTS frame length")
		require.LessOrEqual(t, i+frameLen, len(stream), "ADTS frame within stream")
		header := 7
		if !protectionAbsent {
			header = 9
		}
		aus = append(aus, stream[i+header:i+frameLen])
		i += frameLen
	}
	return aus
}

// toneInt16 returns n mono int16 samples of a sine at freqHz, phase-continued
// from the given sample offset.
func toneInt16(n, rate, freqHz, phase int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(20000 * math.Sin(2*math.Pi*float64(freqHz)*float64(phase+i)/float64(rate)))
	}
	return out
}

// toneFloat32 returns n mono float32 samples of a sine in [-1, 1].
func toneFloat32(n, rate, freqHz, phase int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(0.6 * math.Sin(2*math.Pi*float64(freqHz)*float64(phase+i)/float64(rate)))
	}
	return out
}

// toneInterleavedS16LE returns n mono s16le PCM bytes of a sine at freqHz.
func toneInterleavedS16LE(n, rate, freqHz int) []byte {
	out := make([]byte, n*2)
	for i := range n {
		v := int16(20000 * math.Sin(2*math.Pi*float64(freqHz)*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}

// pcmEnergy sums the absolute s16le sample amplitudes, a cheap non-silence check.
func pcmEnergy(b []byte) int64 {
	var e int64
	for i := 0; i+1 < len(b); i += 2 {
		v := int64(int16(binary.LittleEndian.Uint16(b[i:])))
		if v < 0 {
			v = -v
		}
		e += v
	}
	return e
}
