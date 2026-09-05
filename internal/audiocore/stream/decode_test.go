package stream

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	audiostream "github.com/tphakala/go-audio-stream"
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
