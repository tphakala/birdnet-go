package analysis

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// soundLevelChanSize is the default capacity for the sound level output channel.
const soundLevelChanSize = 100

// SoundLevelConsumer implements audiocore.AudioConsumer. On each Write call it
// converts the PCM byte data to float64 samples and feeds them to a
// soundlevel.Processor for 1/3 octave band analysis. When the processor's
// aggregation interval is complete, the resulting SoundLevelData is published
// on the output channel.
type SoundLevelConsumer struct {
	id        string
	rate      int
	depth     int
	channels  int
	processor *soundlevel.Processor
	outCh     chan soundlevel.SoundLevelData
	closed    atomic.Bool
	closeOnce sync.Once
}

// NewSoundLevelConsumer creates a SoundLevelConsumer that wraps the given
// soundlevel.Processor. Completed interval measurements are published to
// the returned channel.
//
// Returns an error if the processor is nil.
func NewSoundLevelConsumer(id string, proc *soundlevel.Processor, sampleRate, bitDepth, channels int) (*SoundLevelConsumer, chan soundlevel.SoundLevelData, error) {
	if proc == nil {
		return nil, nil, errors.Newf("sound level processor must not be nil").
			Component("analysis.sound_level_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_sound_level_consumer").
			Build()
	}

	if sampleRate <= 0 {
		return nil, nil, errors.Newf("invalid sample rate: %d, must be greater than 0", sampleRate).
			Component("analysis.sound_level_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_sound_level_consumer").
			Context("sample_rate", sampleRate).
			Build()
	}

	if channels <= 0 {
		return nil, nil, errors.Newf("invalid channel count: %d, must be greater than 0", channels).
			Component("analysis.sound_level_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_sound_level_consumer").
			Context("channels", channels).
			Build()
	}

	if bitDepth != 16 {
		return nil, nil, errors.Newf("unsupported bit depth %d: SoundLevelConsumer requires 16-bit PCM", bitDepth).
			Component("analysis.sound_level_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_sound_level_consumer").
			Context("bit_depth", bitDepth).
			Build()
	}

	ch := make(chan soundlevel.SoundLevelData, soundLevelChanSize)
	return &SoundLevelConsumer{
		id:        id,
		rate:      sampleRate,
		depth:     bitDepth,
		channels:  channels,
		processor: proc,
		outCh:     ch,
	}, ch, nil
}

// ID returns the unique identifier for this consumer.
func (c *SoundLevelConsumer) ID() string { return c.id }

// SampleRate returns the expected sample rate in Hz.
func (c *SoundLevelConsumer) SampleRate() int { return c.rate }

// BitDepth returns the expected bit depth.
func (c *SoundLevelConsumer) BitDepth() int { return c.depth }

// Channels returns the expected channel count.
func (c *SoundLevelConsumer) Channels() int { return c.channels }

// Write converts the frame's PCM bytes to float64 samples and feeds them to
// the octave band processor. If the processor's aggregation interval is
// complete, the result is published on the output channel. Returns
// audiocore.ErrConsumerClosed after Close has been called.
func (c *SoundLevelConsumer) Write(frame audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if c.closed.Load() {
		return audiocore.ErrConsumerClosed
	}

	// Convert PCM bytes to float64 samples normalized to [-1, 1].
	samples := convert.BytesToFloat64PCM16(frame.Data)
	if len(samples) == 0 {
		return nil
	}

	data, err := c.processor.ProcessSamples(samples)
	if err != nil {
		// ErrIntervalIncomplete is normal — the window is not yet full.
		if errors.Is(err, soundlevel.ErrIntervalIncomplete) {
			return nil
		}
		GetLogger().Warn("sound level processing failed",
			logger.String("source_id", frame.SourceID),
			logger.Error(err),
			logger.String("operation", "sound_level_consumer_write"))
		return nil
	}

	if data != nil {
		// Non-blocking send: drop if channel is full.
		select {
		case c.outCh <- *data:
		default:
		}
	}

	return nil
}

// Close marks the consumer as closed. Subsequent Write calls return
// audiocore.ErrConsumerClosed. The output channel is not closed — the caller
// is responsible for draining it.
func (c *SoundLevelConsumer) Close() error {
	c.closed.Store(true)
	c.closeOnce.Do(func() { close(c.outCh) })
	return nil
}

// Compile-time interface check.
var _ audiocore.AudioConsumer = (*SoundLevelConsumer)(nil)
