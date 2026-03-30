package analysis

import (
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ModelTarget describes a model that should receive audio from this consumer.
// Each target specifies the model identifier and the sample rate at which the
// model expects to receive audio data.
type ModelTarget struct {
	ModelID    string // unique model identifier (e.g. "BirdNET_V2.4")
	SampleRate int    // target sample rate for this model in Hz
}

// BufferConsumer implements audiocore.AudioConsumer and writes each incoming
// AudioFrame's PCM data to both the analysis buffers (one per model target) and
// the capture buffer for the frame's source. It acts as the bridge between the
// audiocore routing layer and the buffer.Manager that feeds analysis pipelines.
//
// Audio is resampled on the fly when a model target's sample rate differs from
// the source rate. A single Resampler is created per unique target rate to
// avoid redundant conversion when multiple models share the same rate.
//
// PCM constraint: The BirdNET model expects 16-bit signed little-endian PCM.
// Callers must ensure the router delivers frames with BitDepth=16. Higher
// bit depths will be written to the buffers without conversion, which will
// produce incorrect analysis results.
type BufferConsumer struct {
	id             string
	bufferMgr      *buffer.Manager
	rate           int // source sample rate
	depth          int
	channels       int
	closed         atomic.Bool
	targets        []ModelTarget
	resamplers     map[int]*resample.Resampler // keyed by target rate
	groupedTargets map[int][]ModelTarget       // targets grouped by rate, pre-computed
	bufWarnOnce    sync.Map                    // modelID → struct{}, logs missing buffer once per model
}

// NewBufferConsumer creates a BufferConsumer that writes audio frames to the
// analysis and capture buffers managed by bufferMgr. The consumer expects
// frames with the given sample rate, bit depth, and channel count.
//
// targets specifies the models that should receive audio. One Resampler is
// created per unique target sample rate that differs from sampleRate. Targets
// are pre-grouped by rate so the Write hot path only iterates rate groups.
//
// Returns an error if bufferMgr is nil, if the audio parameters are invalid,
// or if a required resampler cannot be created.
func NewBufferConsumer(id string, bufferMgr *buffer.Manager, sampleRate, bitDepth, channels int, targets []ModelTarget) (*BufferConsumer, error) {
	if bufferMgr == nil {
		return nil, errors.Newf("buffer manager must not be nil").
			Component("analysis.buffer_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_consumer").
			Build()
	}
	if sampleRate <= 0 {
		return nil, errors.Newf("invalid sample rate: %d", sampleRate).
			Component("analysis.buffer_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_consumer").
			Build()
	}
	if bitDepth <= 0 {
		return nil, errors.Newf("invalid bit depth: %d", bitDepth).
			Component("analysis.buffer_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_consumer").
			Build()
	}
	if channels <= 0 {
		return nil, errors.Newf("invalid channel count: %d", channels).
			Component("analysis.buffer_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_consumer").
			Build()
	}

	// Filter out targets with invalid sample rates before grouping.
	// A zero or negative rate would cause resampler creation to fail and
	// indicates a misconfigured model (e.g., unrecognized custom model).
	validTargets := make([]ModelTarget, 0, len(targets))
	for _, t := range targets {
		if t.SampleRate <= 0 {
			GetLogger().Error("skipping model target with invalid sample rate",
				logger.String("model_id", t.ModelID),
				logger.Int("sample_rate", t.SampleRate),
				logger.String("consumer_id", id),
				logger.String("operation", "new_buffer_consumer"))
			continue
		}
		validTargets = append(validTargets, t)
	}

	// Pre-compute grouped targets and create one resampler per unique
	// non-native rate.
	grouped := make(map[int][]ModelTarget, len(validTargets))
	for _, t := range validTargets {
		grouped[t.SampleRate] = append(grouped[t.SampleRate], t)
	}

	resamplers := make(map[int]*resample.Resampler, len(grouped))
	for rate := range grouped {
		if rate == sampleRate {
			continue // native rate, no resampler needed
		}
		r, rErr := resample.NewResampler(sampleRate, rate)
		if rErr != nil {
			// Clean up any resamplers already created.
			for _, prev := range resamplers {
				_ = prev.Close()
			}
			return nil, errors.Newf("failed to create resampler for target rate %d: %w", rate, rErr).
				Component("analysis.buffer_consumer").
				Category(errors.CategoryAudio).
				Context("operation", "new_buffer_consumer").
				Context("source_rate", sampleRate).
				Context("target_rate", rate).
				Build()
		}
		resamplers[rate] = r
	}

	return &BufferConsumer{
		id:             id,
		bufferMgr:      bufferMgr,
		rate:           sampleRate,
		depth:          bitDepth,
		channels:       channels,
		targets:        validTargets,
		resamplers:     resamplers,
		groupedTargets: grouped,
	}, nil
}

// ID returns the unique identifier for this consumer.
func (c *BufferConsumer) ID() string { return c.id }

// SampleRate returns the expected sample rate in Hz.
func (c *BufferConsumer) SampleRate() int { return c.rate }

// BitDepth returns the expected bit depth.
func (c *BufferConsumer) BitDepth() int { return c.depth }

// Channels returns the expected channel count.
func (c *BufferConsumer) Channels() int { return c.channels }

// Write delivers an audio frame to both the analysis buffers (one per model
// target, with resampling as needed) and the capture buffer for the frame's
// source. If a buffer is not found the missing write is logged and skipped.
// Returns an error only when the consumer has been closed.
func (c *BufferConsumer) Write(frame audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if c.closed.Load() {
		return audiocore.ErrConsumerClosed
	}

	sourceID := frame.SourceID

	// Write to capture buffer (always at source rate).
	cb, err := c.bufferMgr.CaptureBuffer(sourceID)
	if err != nil {
		GetLogger().Warn("capture buffer not found for source",
			logger.String("source_id", sourceID),
			logger.String("consumer_id", c.id),
			logger.String("operation", "buffer_consumer_write"))
	} else if writeErr := cb.Write(frame.Data); writeErr != nil {
		GetLogger().Warn("failed to write to capture buffer",
			logger.String("source_id", sourceID),
			logger.Error(writeErr),
			logger.String("operation", "buffer_consumer_write"))
	}

	// Fan out to model analysis buffers, grouped by target rate.
	// All targets sharing the same rate are written before the next
	// ResampleInto call, so the resampler's internal buffer is safe to reuse.
	for rate, targets := range c.groupedTargets {
		var data []byte
		if rate == c.rate {
			data = frame.Data // native rate, no resampling
		} else {
			resampled, resampleErr := c.resamplers[rate].ResampleInto(frame.Data)
			if resampleErr != nil {
				GetLogger().Error("resampling failed for target rate",
					logger.Int("target_rate", rate),
					logger.String("source_id", sourceID),
					logger.Error(resampleErr),
					logger.String("operation", "buffer_consumer_write"))
				continue
			}
			data = resampled
		}
		for _, t := range targets {
			ab, abErr := c.bufferMgr.AnalysisBuffer(sourceID, t.ModelID)
			if abErr != nil {
				if _, loaded := c.bufWarnOnce.LoadOrStore(t.ModelID, struct{}{}); !loaded {
					GetLogger().Warn("analysis buffer not found for model target",
						logger.String("source_id", sourceID),
						logger.String("model_id", t.ModelID),
						logger.String("consumer_id", c.id),
						logger.String("operation", "buffer_consumer_write"))
				}
				continue
			}
			if writeErr := ab.Write(data); writeErr != nil {
				GetLogger().Warn("failed to write to analysis buffer",
					logger.String("source_id", sourceID),
					logger.String("model_id", t.ModelID),
					logger.Error(writeErr),
					logger.String("operation", "buffer_consumer_write"))
			}
		}
	}

	// Design: Write intentionally returns nil even when individual buffer
	// writes fail. A missing or errored buffer should not crash the audio
	// pipeline — the AudioConsumer contract expects Write to be resilient.
	// Failures are logged above so operators can diagnose issues.
	return nil
}

// Close marks the consumer as closed and releases all owned resamplers.
// Subsequent Write calls return audiocore.ErrConsumerClosed.
func (c *BufferConsumer) Close() error {
	c.closed.Store(true)
	for _, r := range c.resamplers {
		_ = r.Close()
	}
	return nil
}

// AudioLevelData holds audio level data computed from PCM frames.
type AudioLevelData struct {
	Level    int    `json:"level"`    // 0-100
	Clipping bool   `json:"clipping"` // true if clipping is detected
	Source   string `json:"source"`   // Source identifier
	Name     string `json:"name"`     // Human-readable name of the source
}

// Audio level scaling constants.
const (
	// audioLevelDBFloor is the noise floor in dB used for scaling.
	audioLevelDBFloor = -60.0

	// audioLevelDBRange is the dynamic range mapped to 0-100.
	audioLevelDBRange = 50.0

	// audioLevelClippingMin is the minimum level assigned when clipping is detected.
	audioLevelClippingMin = 95.0

	// pcm16Max is the maximum value for signed 16-bit PCM.
	pcm16Max = 32768.0

	// pcm16ClipPositive is the positive clipping threshold for 16-bit PCM.
	pcm16ClipPositive int16 = 32767

	// pcm16ClipNegative is the negative clipping threshold for 16-bit PCM.
	pcm16ClipNegative int16 = -32768
)

// calculateAudioLevel computes the RMS audio level (0-100) from 16-bit PCM
// samples and detects clipping. This mirrors the legacy implementation in
// internal/audiocore/capture.go.
func calculateAudioLevel(samples []byte, source, name string) AudioLevelData {
	if len(samples) == 0 {
		return AudioLevelData{Level: 0, Clipping: false, Source: source, Name: name}
	}

	// Truncate to an even number of bytes for 16-bit samples.
	// An odd byte count means the last byte has no pair and cannot form
	// a valid 16-bit sample, so it is silently dropped.
	if len(samples)%2 != 0 {
		GetLogger().Debug("odd byte count in PCM data, truncating trailing byte",
			logger.Int("original_len", len(samples)),
			logger.String("source", source))
		samples = samples[:len(samples)-1]
	}

	sampleCount := len(samples) / 2
	if sampleCount == 0 {
		return AudioLevelData{Level: 0, Clipping: false, Source: source, Name: name}
	}

	var sum float64
	isClipping := false

	for i := 0; i < len(samples); i += 2 {
		if i+1 >= len(samples) {
			break
		}
		sample := int16(binary.LittleEndian.Uint16(samples[i : i+2])) //nolint:gosec // G115: intentional uint16→int16 bit reinterpretation for PCM audio
		sampleAbs := math.Abs(float64(sample))
		sum += sampleAbs * sampleAbs

		if sample == pcm16ClipPositive || sample == pcm16ClipNegative {
			isClipping = true
		}
	}

	// RMS → dB → scaled 0-100.
	rms := math.Sqrt(sum / float64(sampleCount))
	db := 20 * math.Log10(rms/pcm16Max)
	scaledLevel := (db - audioLevelDBFloor) * (100.0 / audioLevelDBRange)

	if isClipping {
		scaledLevel = math.Max(scaledLevel, audioLevelClippingMin)
	}

	// Clamp to [0, 100].
	if scaledLevel < 0 {
		scaledLevel = 0
	} else if scaledLevel > 100 {
		scaledLevel = 100
	}

	return AudioLevelData{
		Level:    int(scaledLevel),
		Clipping: isClipping,
		Source:   source,
		Name:     name,
	}
}

// Compile-time interface check.
var _ audiocore.AudioConsumer = (*BufferConsumer)(nil)
