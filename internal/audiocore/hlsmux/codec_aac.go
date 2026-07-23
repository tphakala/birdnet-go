package hlsmux

import (
	"fmt"

	aacpcm "github.com/tphakala/go-aac/pcm"
	m4a "github.com/tphakala/go-m4a"
)

const (
	// aacCodecName identifies AAC-LC in configuration and in logs.
	aacCodecName = "aac-lc"

	// aacFrameSamples is the per-channel sample count of one AAC-LC access
	// unit. It is what New checks a segment target against, so a segment too
	// short to hold a single access unit is rejected at construction rather
	// than by every frame the encoder emits.
	aacFrameSamples = 1024

	// bitsPerKilobit converts the codec-independent BitrateKbps carried by
	// EncoderConfig to the bits per second go-aac's Config takes.
	bitsPerKilobit = 1000
)

// AACLC returns the AAC-LC codec, backed by go-aac for encoding and go-m4a's
// mp4a sample entry for the container.
//
// It is the only codec this package exports today. AAC-LC is chosen for HLS
// compatibility rather than on technical merit: Opus is cheaper to encode and
// sounds better at these bitrates, but it does not play through the audio
// element on Apple devices and is not a codec in the HLS Authoring
// Specification, so shipping it would silently break live audio on every
// iPhone. The codec remains a parameter throughout this package precisely so
// that judgement can be revisited without a rewrite.
func AACLC() Codec {
	return Codec{
		Name:            aacCodecName,
		MaxFrameSamples: aacFrameSamples,
		newEncoder:      newAACEncoder,
		writerConfig:    aacWriterConfig,
	}
}

// aacEncoder adapts go-aac's pcm.FrameEncoder to the FrameEncoder interface.
//
// The adaptation is thin but not omittable. go-aac's EncodeInterleaved and
// Flush take an unnamed func literal type, while this package declares EmitFunc
// as a named type, and Go treats those as different method signatures for
// interface satisfaction, so *pcm.FrameEncoder does not satisfy FrameEncoder
// directly however identical the underlying signatures look.
type aacEncoder struct {
	enc *aacpcm.FrameEncoder
}

// newAACEncoder builds the per-stream AAC-LC encoder.
func newAACEncoder(cfg EncoderConfig) (FrameEncoder, error) {
	// Cutoff is deliberately left at zero, selecting go-aac's tuned
	// rate-dependent default. Raising the coded bandwidth for bird song, which
	// carries energy well above the default cutoff, is a real tuning question
	// but a separate one: it changes what listeners hear and wants measurement,
	// not a value picked while wiring the codec up.
	enc, err := aacpcm.NewFrameEncoder(aacpcm.Config{
		SampleRate: cfg.SampleRate,
		BitDepth:   bitDepth16,
		Channels:   cfg.Channels,
		Bitrate:    cfg.BitrateKbps * bitsPerKilobit,
	})
	if err != nil {
		return nil, fmt.Errorf("hlsmux: build AAC-LC encoder: %w", err)
	}
	return &aacEncoder{enc: enc}, nil
}

// aacWriterConfig describes the AAC-LC track to go-m4a.
func aacWriterConfig(cfg EncoderConfig, enc FrameEncoder) (m4a.WriterConfig, error) {
	asc := enc.DecoderConfig()
	if len(asc) == 0 {
		return m4a.WriterConfig{}, fmt.Errorf("hlsmux: AAC-LC encoder produced an empty AudioSpecificConfig")
	}

	// Map the encoder's reported priming onto go-m4a's EncoderDelay, whose zero
	// value means "use the codec default" (1024 for AAC-LC) rather than "no
	// priming". Passing a reported zero straight through would therefore trim a
	// frame that was never added, shifting the whole timeline by 1024 samples.
	// NoEdit is the sentinel that suppresses the edit list, which is what an
	// encoder reporting no priming actually wants.
	encoderDelay := enc.Delay()
	if encoderDelay == 0 {
		encoderDelay = m4a.NoEdit
	}

	// MediaLength is deliberately left unset. It pins the edit list to an exact
	// source length, which a live stream does not have, and go-m4a's fragmented
	// constructors reject any non-zero value rather than ignore it.
	return m4a.WriterConfig{
		Codec:        m4a.CodecAACLC,
		SampleRate:   cfg.SampleRate,
		Channels:     cfg.Channels,
		ASC:          asc,
		EncoderDelay: encoderDelay,
	}, nil
}

// EncodeInterleaved consumes pcm and reports each complete access unit.
func (a *aacEncoder) EncodeInterleaved(pcm []byte, emit EmitFunc) error {
	return a.enc.EncodeInterleaved(pcm, func(au []byte, samples int) error {
		return emit(au, samples)
	})
}

// Flush drains the encoder lookahead so the final frame is not lost.
func (a *aacEncoder) Flush(emit EmitFunc) error {
	return a.enc.Flush(func(au []byte, samples int) error {
		return emit(au, samples)
	})
}

// DecoderConfig returns the MPEG-4 AudioSpecificConfig for the esds box.
func (a *aacEncoder) DecoderConfig() []byte {
	return a.enc.AudioSpecificConfig()
}

// Delay is the encoder priming in samples, trimmed by the container edit list.
func (a *aacEncoder) Delay() int {
	return a.enc.Delay()
}

// Close releases the encoder. go-aac holds nothing that needs releasing, so
// this is a no-op; the method exists so a pooled or cgo-backed encoder can be
// substituted later without changing the interface.
func (a *aacEncoder) Close() error {
	return nil
}
