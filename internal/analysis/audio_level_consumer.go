package analysis

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// audioLevelChanSize is the default capacity for the output channel.
const audioLevelChanSize = 100

// AudioLevelConsumer implements audiocore.AudioConsumer. On each Write call it
// computes the RMS audio level (0-100) from the PCM data, detects clipping,
// and publishes an AudioLevelData value on its output channel for SSE
// subscribers.
type AudioLevelConsumer struct {
	id        string
	rate      int
	depth     int
	channels  int
	outCh     chan AudioLevelData
	closed    atomic.Bool
	closeOnce sync.Once
}

// NewAudioLevelConsumer creates an AudioLevelConsumer that publishes computed
// audio level data to the returned channel. The channel is buffered to
// decouple the producer from slow SSE consumers; if the channel is full the
// newest value is dropped to avoid blocking the audio pipeline.
//
// The caller owns the returned channel and may range over it. Close() does
// not close the channel — the caller should stop reading when Close is called.
func NewAudioLevelConsumer(id string, sampleRate, bitDepth, channels int) (consumer *AudioLevelConsumer, outCh chan AudioLevelData) {
	ch := make(chan AudioLevelData, audioLevelChanSize)
	return &AudioLevelConsumer{
		id:       id,
		rate:     sampleRate,
		depth:    bitDepth,
		channels: channels,
		outCh:    ch,
	}, ch
}

// ID returns the unique identifier for this consumer.
func (c *AudioLevelConsumer) ID() string { return c.id }

// SampleRate returns the expected sample rate in Hz.
func (c *AudioLevelConsumer) SampleRate() int { return c.rate }

// BitDepth returns the expected bit depth.
func (c *AudioLevelConsumer) BitDepth() int { return c.depth }

// Channels returns the expected channel count.
func (c *AudioLevelConsumer) Channels() int { return c.channels }

// Write computes the RMS audio level from the frame's PCM data and publishes
// the result on the output channel. If the channel is full the value is
// dropped silently. Returns audiocore.ErrConsumerClosed after Close has been
// called.
func (c *AudioLevelConsumer) Write(frame audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if c.closed.Load() {
		return audiocore.ErrConsumerClosed
	}

	level := calculateAudioLevel(frame.Data, frame.SourceID, frame.SourceName)

	// Non-blocking send: drop if channel is full.
	select {
	case c.outCh <- level:
	default:
	}

	return nil
}

// Close marks the consumer as closed. Subsequent Write calls return
// audiocore.ErrConsumerClosed. The output channel is not closed — the caller
// is responsible for draining it.
func (c *AudioLevelConsumer) Close() error {
	c.closed.Store(true)
	c.closeOnce.Do(func() { close(c.outCh) })
	return nil
}

// Compile-time interface check.
var _ audiocore.AudioConsumer = (*AudioLevelConsumer)(nil)
