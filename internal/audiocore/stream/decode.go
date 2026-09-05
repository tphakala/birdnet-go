package stream

import (
	"encoding/binary"
	"fmt"
	"math"

	aacpcm "github.com/tphakala/go-aac/pcm"
	audiostream "github.com/tphakala/go-audio-stream"
	mp3 "github.com/tphakala/go-mp3"
	"github.com/tphakala/go-opus/opus"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// errCodecConfigPending signals that a track's codec configuration is not yet
// resolved: an in-band MP4A-LATM stream (cpresent=1) carries its
// AudioSpecificConfig in the first packets, not in the SDP, so there is nothing
// to decode until OnCodecUpdate delivers it. It is deliberately NOT terminal
// (unlike ErrUnsupportedCodec): the caller proceeds to SETUP/PLAY and lets the
// in-band update install the decoder, and the pipeline drops frames while the
// decoder is nil.
var errCodecConfigPending = errors.Newf("codec configuration not yet resolved").
	Component("native-stream").Category(errors.CategoryAudioSource).Build()

// Decoder scratch sizing. Each bounds one decoded frame so the reused output
// buffers never reallocate on the steady-state path.
const (
	// opusMaxSamples is the largest Opus frame at 48 kHz: 120 ms, mono.
	opusMaxSamples = 5760
	// mp3MaxInterleaved is the largest MPEG Layer III frame interleaved: 1152
	// samples per channel, stereo.
	mp3MaxInterleaved = 1152 * 2
	// aacMaxBytes is one AAC-LC access unit as s16 PCM: 1024 samples, stereo.
	aacMaxBytes = 1024 * 2 * 2

	// opusOutputRate and opusOutputChannels are the geometry the native path
	// decodes Opus straight into, skipping the downmix and resample stages.
	opusOutputRate     = 48000
	opusOutputChannels = 1

	// s16FullScale scales a normalized [-1, 1] float to the 16-bit range.
	s16FullScale = 32767
)

// frameDecoder converts one depacketized frame into interleaved little-endian
// s16 PCM. Implementations own reusable scratch buffers, so decodeFrame is not
// safe for concurrent use and must be called only from the supervisor's reader
// goroutine. Geometry is reported per frame because a compressed codec's true
// sample rate and channel count come from its bitstream, not the transport.
type frameDecoder interface {
	// decodeFrame decodes in into interleaved s16le PCM and reports the PCM
	// geometry. The returned slice is owned by the decoder and is valid only
	// until the next decodeFrame call; the caller must consume or copy it before
	// decoding the next frame.
	decodeFrame(in []byte) (pcm []byte, sampleRate, channels int, err error)
	// reset clears codec state at a session boundary (a supervisor reconnect).
	reset() error
}

// newFrameDecoder builds the decoder for a track's codec. PCM kinds (G.711,
// G.726, L16) the library already expanded to s16le pass through with the
// geometry from format; compressed kinds decode through the codec library. An
// unsupported codec (FLAC, HE-AAC/SBR, opaque, or an unresolved LATM config) is
// a terminal ErrUnsupportedCodec.
func newFrameDecoder(codec audiostream.Codec, format audiostream.AudioFormat) (frameDecoder, error) {
	switch c := codec.(type) {
	case audiostream.CodecG711, audiostream.CodecG726, audiostream.CodecL16:
		return &passthroughDecoder{sampleRate: format.SampleRate, channels: format.Channels}, nil
	case audiostream.CodecOpus:
		return newOpusFrameDecoder()
	case audiostream.CodecMP3:
		return newMP3FrameDecoder(), nil
	case audiostream.CodecAAC:
		return newAACFrameDecoder(c.AudioSpecificConfig)
	case audiostream.CodecMP4ALATM:
		if len(c.AudioSpecificConfig) == 0 {
			// The ASC has not been learned yet (in-band cpresent=1). This is not
			// terminal: the stream proceeds and OnCodecUpdate installs the
			// decoder once the config arrives in-band.
			return nil, errCodecConfigPending
		}
		return newAACFrameDecoder(c.AudioSpecificConfig)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedCodec, codecName(codec))
	}
}

// passthroughDecoder returns the library-expanded PCM unchanged. The library
// delivers G.711, G.726, and L16 as interleaved s16le PCM, so decode is a no-op
// and the geometry comes from the reported AudioFormat.
type passthroughDecoder struct {
	sampleRate, channels int
}

func (d *passthroughDecoder) decodeFrame(in []byte) (pcm []byte, sampleRate, channels int, err error) {
	return in, d.sampleRate, d.channels, nil
}

func (d *passthroughDecoder) reset() error { return nil }

// opusFrameDecoder decodes one Opus packet per frame straight to 48 kHz mono.
// Downmix is always skipped (the output is already mono); resample is skipped
// too whenever the analysis target rate is 48 kHz, the common case.
type opusFrameDecoder struct {
	dec *opus.Decoder
	i16 []int16
	out []byte
}

func newOpusFrameDecoder() (*opusFrameDecoder, error) {
	dec, err := opus.NewDecoder(opusOutputRate, opusOutputChannels)
	if err != nil {
		return nil, fmt.Errorf("native opus decoder: %w", err)
	}
	return &opusFrameDecoder{
		dec: dec,
		i16: make([]int16, opusMaxSamples),
		out: make([]byte, opusMaxSamples*2),
	}, nil
}

func (d *opusFrameDecoder) decodeFrame(in []byte) (pcm []byte, sampleRate, channels int, err error) {
	n, err := d.dec.Decode(in, d.i16)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("opus decode: %w", err)
	}
	// Slice both to n so the compiler can eliminate the per-sample bounds checks
	// in this tight loop.
	samples := d.i16[:n]
	out := d.out[:n*2]
	for i := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(samples[i]))
	}
	return out, opusOutputRate, opusOutputChannels, nil
}

func (d *opusFrameDecoder) reset() error {
	d.dec.Reset()
	return nil
}

// mp3FrameDecoder decodes one MPEG frame per call. Each frame's header names its
// own geometry, so sample rate and channel count are reported per frame.
type mp3FrameDecoder struct {
	dec *mp3.Decoder
	f32 []float32
	out []byte
}

func newMP3FrameDecoder() *mp3FrameDecoder {
	return &mp3FrameDecoder{
		dec: mp3.NewDecoder(),
		f32: make([]float32, mp3MaxInterleaved),
		out: make([]byte, mp3MaxInterleaved*2),
	}
}

func (d *mp3FrameDecoder) decodeFrame(in []byte) (pcm []byte, sampleRate, channels int, err error) {
	n, info, err := d.dec.DecodeFrame(in, d.f32)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("mp3 decode: %w", err)
	}
	if n == 0 {
		return d.out[:0], info.SampleRate, info.Channels, nil
	}
	out := d.out[:n*2]
	float32ToS16LE(out, d.f32[:n])
	return out, info.SampleRate, info.Channels, nil
}

func (d *mp3FrameDecoder) reset() error {
	d.dec.Reset()
	return nil
}

// aacFrameDecoder decodes AAC-LC access units through go-aac's push-style frame
// decoder, built from the out-of-band AudioSpecificConfig. NewRawDecoder parses
// the ASC up front and rejects a non-LC, multichannel, or HE-AAC config, so an
// unsupported stream fails at construction rather than at the first frame.
type aacFrameDecoder struct {
	dec *aacpcm.FrameDecoder
	out []byte
}

func newAACFrameDecoder(asc []byte) (*aacFrameDecoder, error) {
	dec, err := aacpcm.NewRawDecoder(asc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedCodec, err)
	}
	return &aacFrameDecoder{dec: dec, out: make([]byte, 0, aacMaxBytes)}, nil
}

func (d *aacFrameDecoder) decodeFrame(in []byte) (pcm []byte, sampleRate, channels int, err error) {
	out, _, err := d.dec.DecodeFrame(d.out[:0], in)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("aac decode: %w", err)
	}
	d.out = out
	return out, d.dec.SampleRate(), d.dec.Channels(), nil
}

func (d *aacFrameDecoder) reset() error {
	return d.dec.Reset()
}

// float32ToS16LE writes len(src) normalized float samples into dst as
// interleaved little-endian s16, rounding to nearest. dst must have room for
// len(src)*2 bytes. The float is clamped to [-1, 1] BEFORE the integer cast, so
// an out-of-range input can never overflow the cast and bypass the clamp; the
// reslice is also a bounds-check hint for this per-sample loop.
func float32ToS16LE(dst []byte, src []float32) {
	dst = dst[:len(src)*2]
	for i, s := range src {
		switch {
		case s > 1:
			s = 1
		case s < -1:
			s = -1
		}
		v := int16(math.Round(float64(s) * s16FullScale))
		binary.LittleEndian.PutUint16(dst[i*2:], uint16(v))
	}
}

// codecName is a short, stable label for a codec. It feeds the
// StreamHealth.Codec observability field (e.g. "aac-lc", "opus", "pcmu") and the
// terminal unsupported-codec error message. An unrecognized codec falls back to
// its Go type name.
func codecName(codec audiostream.Codec) string {
	switch c := codec.(type) {
	case audiostream.CodecOpus:
		return "opus"
	case audiostream.CodecMP3:
		return "mp3"
	case audiostream.CodecAAC, audiostream.CodecMP4ALATM:
		return "aac-lc"
	case audiostream.CodecG711:
		if c.Law == audiostream.ALaw {
			return "pcma"
		}
		return "pcmu"
	case audiostream.CodecG726:
		return "g726"
	case audiostream.CodecL16:
		return "l16"
	case audiostream.CodecFLAC:
		return "flac"
	case audiostream.CodecUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("%T", codec)
	}
}
