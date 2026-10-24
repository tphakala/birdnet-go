package analysis

import (
	"sync"
	"sync/atomic"

	dsppcm "github.com/tphakala/go-audio-dsp/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// soundLevelChanSize is the default capacity for the sound level output channel.
const soundLevelChanSize = 100

const (
	// soundLevelDropLogInterval is how many dropped measurements pass between
	// warnings about a full channel, starting with the first drop.
	soundLevelDropLogInterval = 100

	// soundLevelSustainedDropThreshold is the drop count at which sustained
	// output drops are reported.
	soundLevelSustainedDropThreshold = 1000
)

const (
	// soundLevelDecodeChunk is the number of samples decoded per step. It bounds
	// the float32 scratch and matches the octave meter's own chunk size.
	soundLevelDecodeChunk = 4096

	// bytesPerSample is the size of one 16-bit PCM sample.
	bytesPerSample = 2
)

// SoundLevelConsumer implements audiocore.AudioConsumer. On each Write call it
// decodes the PCM byte data to float32 samples and feeds them to a
// soundlevel.Processor for 1/3 octave band analysis. Each completed
// aggregation interval is published as SoundLevelData on the output channel.
type SoundLevelConsumer struct {
	id        string
	rate      int
	depth     int
	channels  int
	processor *soundlevel.Processor
	outCh     chan soundlevel.SoundLevelData
	closed    atomic.Bool
	closeOnce sync.Once
	sampleBuf []float32 // fixed decode scratch of soundLevelDecodeChunk samples (Write is sequential per consumer)
	outDrops  atomic.Int64
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
		sampleBuf: make([]float32, soundLevelDecodeChunk),
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

// Write decodes the frame's PCM bytes to float32 samples in fixed-size chunks and
// feeds them to the octave band processor, which reports every interval the
// frame completes on the output channel. A frame with an odd byte length is
// dropped with a warning, because 16-bit PCM frames are aligned upstream and an
// odd length signals a bug. Returns audiocore.ErrConsumerClosed after Close has
// been called.
func (c *SoundLevelConsumer) Write(frame audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if c.closed.Load() {
		return audiocore.ErrConsumerClosed
	}

	if len(frame.Data)%bytesPerSample != 0 {
		GetLogger().Warn("sound level frame has odd byte length, dropped",
			logger.String("source_id", frame.SourceID),
			logger.String("consumer_id", c.id),
			logger.Int("bytes", len(frame.Data)),
			logger.String("operation", "sound_level_consumer_write"))
		return nil
	}

	data := frame.Data
	for len(data) > 0 {
		step := min(len(data), bytesPerSample*soundLevelDecodeChunk)
		n, err := dsppcm.BytesToFloat32(c.sampleBuf, data[:step])
		if err != nil {
			// Unreachable: the length is even. Kept so a library change cannot
			// turn into silently wrong levels.
			GetLogger().Warn("sound level PCM decode failed",
				logger.String("source_id", frame.SourceID),
				logger.Error(err),
				logger.String("operation", "sound_level_consumer_write"))
			return nil
		}
		c.processor.Process(c.sampleBuf[:n], c.publish)
		data = data[step:]
	}

	return nil
}

// publish sends a completed interval on the output channel without blocking. A
// full channel drops the measurement and counts the drop.
func (c *SoundLevelConsumer) publish(data soundlevel.SoundLevelData) {
	select {
	case c.outCh <- data:
	default:
		drops := c.outDrops.Add(1)
		if drops%soundLevelDropLogInterval == 1 {
			GetLogger().Warn("sound level output channel full, dropping measurement",
				logger.String("consumer_id", c.id),
				logger.Int64("total_out_drops", drops))
		}
		if drops == soundLevelSustainedDropThreshold {
			_ = errors.Newf("sound level consumer dropped %d output measurements", drops).
				Component("analysis.sound_level_consumer").
				Category(errors.CategoryAudio).
				Context("operation", "sustained_output_drops").
				Context("consumer_id", c.id).
				Context("sample_rate", c.rate).
				Build()
		}
	}
}

// Close marks the consumer as closed. Subsequent Write calls return
// audiocore.ErrConsumerClosed. Close also closes the output channel, so the
// caller's receive loop ends once it has drained it.
func (c *SoundLevelConsumer) Close() error {
	c.closed.Store(true)
	c.closeOnce.Do(func() { close(c.outCh) })
	return nil
}

// Compile-time interface check.
var _ audiocore.AudioConsumer = (*SoundLevelConsumer)(nil)
