package hlsmux

import (
	"errors"

	m4a "github.com/tphakala/go-m4a"
)

// The fakes below deliberately avoid the real codec. Two reasons: the AAC
// encoder this package will ship with does not exist yet, and testing against
// a codec that is not the production one is the only way to prove the muxer is
// actually codec neutral rather than merely claiming to be.
//
// go-m4a's FLAC path is used for the container side because it is the least
// constrained: it accepts any positive sample rate, only length-checks its
// STREAMINFO, and has no priming of its own, so the test can vary frame size
// and encoder delay independently of what any real codec would do.

const (
	// flacStreamInfoLen is the fixed size of a FLAC STREAMINFO metadata block,
	// which is all go-m4a validates about it.
	flacStreamInfoLen = 34

	// fakeAUBytesPerSample sizes a fake access unit so segments have a
	// plausible byte size without encoding anything.
	fakeAUBytesPerSample = 2
)

// errFakeEncoder is returned by a fake encoder configured to fail.
var errFakeEncoder = errors.New("fake encoder failure")

// fakeEncoder emits deterministic access units without coding any audio.
//
// It reuses one scratch buffer across every emit, which is what makes it a
// real test of the EmitFunc borrowing contract: if the muxer retained the
// slice instead of letting go-m4a copy it, every access unit in a segment
// would end up with the last unit's contents.
type fakeEncoder struct {
	channels int

	// frameSizes is cycled to determine each access unit's sample count. A
	// single-entry slice models a fixed-frame codec such as AAC-LC.
	frameSizes []int
	frameIdx   int

	// delay is the priming sample count reported to the container.
	delay int

	// pending is the sample count buffered but not yet large enough to emit.
	pending int

	// tailSamples is emitted by Flush, modelling an encoder that holds a
	// lookahead frame back until the stream ends.
	tailSamples int

	// failAfter, when positive, makes the encoder fail once it has emitted
	// that many access units.
	failAfter int

	emitted int
	closed  bool
	scratch []byte
}

// emitOne hands one access unit of the given sample count to emit, reusing the
// scratch buffer and filling it with a value derived from the emit index so a
// test can tell units apart.
func (f *fakeEncoder) emitOne(samples int, emit EmitFunc) error {
	if f.failAfter > 0 && f.emitted >= f.failAfter {
		return errFakeEncoder
	}
	size := samples * fakeAUBytesPerSample
	if cap(f.scratch) < size {
		f.scratch = make([]byte, size)
	}
	f.scratch = f.scratch[:size]
	for i := range f.scratch {
		f.scratch[i] = byte(f.emitted)
	}
	f.emitted++
	return emit(f.scratch, samples)
}

func (f *fakeEncoder) EncodeInterleaved(pcm []byte, emit EmitFunc) error {
	f.pending += len(pcm) / (f.channels * bytesPerSample)
	for {
		size := f.frameSizes[f.frameIdx%len(f.frameSizes)]
		if f.pending < size {
			return nil
		}
		f.pending -= size
		f.frameIdx++
		if err := f.emitOne(size, emit); err != nil {
			return err
		}
	}
}

func (f *fakeEncoder) Flush(emit EmitFunc) error {
	if f.tailSamples <= 0 {
		return nil
	}
	samples := f.tailSamples
	f.tailSamples = 0
	return f.emitOne(samples, emit)
}

func (f *fakeEncoder) DecoderConfig() []byte { return make([]byte, flacStreamInfoLen) }
func (f *fakeEncoder) Delay() int            { return f.delay }

func (f *fakeEncoder) Close() error {
	f.closed = true
	return nil
}

// fakeCodecOptions configures the codec returned by newFakeCodec.
type fakeCodecOptions struct {
	name        string
	frameSizes  []int
	delay       int
	tailSamples int
	failAfter   int

	// declaredFrameSamples is what the Codec advertises. Leaving it zero
	// models a variable-frame codec.
	declaredFrameSamples int

	// encoderErr makes construction of the encoder fail.
	encoderErr error

	// captured receives the encoder once built, so a test can inspect it.
	captured **fakeEncoder
}

// newFakeCodec builds a Codec backed by fakeEncoder.
func newFakeCodec(opts *fakeCodecOptions) Codec {
	if len(opts.frameSizes) == 0 {
		opts.frameSizes = []int{1024}
	}
	name := opts.name
	if name == "" {
		name = "fake"
	}
	return Codec{
		Name:         name,
		FrameSamples: opts.declaredFrameSamples,
		newEncoder: func(cfg EncoderConfig) (FrameEncoder, error) {
			if opts.encoderErr != nil {
				return nil, opts.encoderErr
			}
			enc := &fakeEncoder{
				channels:    cfg.Channels,
				frameSizes:  opts.frameSizes,
				delay:       opts.delay,
				tailSamples: opts.tailSamples,
				failAfter:   opts.failAfter,
			}
			if opts.captured != nil {
				*opts.captured = enc
			}
			return enc, nil
		},
		writerConfig: func(cfg EncoderConfig, enc FrameEncoder) (m4a.WriterConfig, error) {
			return m4a.WriterConfig{
				Codec:        m4a.CodecFLAC,
				SampleRate:   cfg.SampleRate,
				Channels:     cfg.Channels,
				STREAMINFO:   enc.DecoderConfig(),
				EncoderDelay: enc.Delay(),
			}, nil
		},
	}
}

// silence returns samples worth of interleaved 16-bit PCM for channels.
func silence(samples, channels int) []byte {
	return make([]byte, samples*channels*bytesPerSample)
}
