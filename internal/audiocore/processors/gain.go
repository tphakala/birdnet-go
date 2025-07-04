// Package processors provides audio processing implementations for the audiocore package
package processors

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// GainProcessor applies gain adjustment to audio data
type GainProcessor struct {
	id           string
	gain         atomic.Value // stores float64
	outputFormat audiocore.AudioFormat
	logger       *slog.Logger
}

// NewGainProcessor creates a new gain processor
func NewGainProcessor(id string, initialGain float64) (audiocore.AudioProcessor, error) {
	if initialGain < 0.0 || initialGain > 10.0 {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("gain", initialGain).
			Context("error", "gain must be between 0.0 and 10.0").
			Build()
	}

	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(
		"component", "gain_processor",
		"processor_id", id)

	processor := &GainProcessor{
		id:     id,
		logger: logger,
	}
	processor.gain.Store(initialGain)

	logger.Info("gain processor created",
		"initial_gain", initialGain)

	return processor, nil
}

// ID returns a unique identifier for this processor
func (gp *GainProcessor) ID() string {
	return gp.id
}

// Process transforms audio data by applying gain
func (gp *GainProcessor) Process(ctx context.Context, input *audiocore.AudioData) (*audiocore.AudioData, error) {
	if input == nil {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "input audio data is nil").
			Build()
	}

	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	gain := gp.gain.Load().(float64)

	// If gain is 1.0, return input unchanged
	if gain == 1.0 {
		if gp.logger.Enabled(context.TODO(), slog.LevelDebug) {
			gp.logger.Debug("gain is 1.0, returning input unchanged")
		}
		return input, nil
	}

	// Create output buffer
	output := &audiocore.AudioData{
		Buffer:    make([]byte, len(input.Buffer)),
		Format:    input.Format,
		Timestamp: input.Timestamp,
		Duration:  input.Duration,
		SourceID:  input.SourceID,
	}

	// Copy input to output
	copy(output.Buffer, input.Buffer)

	// Apply gain based on encoding
	switch input.Format.Encoding {
	case "pcm_s16le":
		gp.applyGainS16LE(output.Buffer, gain)
		if gp.logger.Enabled(context.TODO(), slog.LevelDebug) {
			gp.logger.Debug("applied gain to PCM S16LE audio",
				"gain", gain,
				"buffer_size", len(output.Buffer))
		}
	case "pcm_f32le":
		gp.applyGainF32LE(output.Buffer, gain)
		if gp.logger.Enabled(context.TODO(), slog.LevelDebug) {
			gp.logger.Debug("applied gain to PCM F32LE audio",
				"gain", gain,
				"buffer_size", len(output.Buffer))
		}
	default:
		gp.logger.Error("unsupported audio encoding",
			"encoding", input.Format.Encoding)
		return nil, errors.New(audiocore.ErrInvalidAudioFormat).
			Component(audiocore.ComponentAudioCore).
			Context("encoding", input.Format.Encoding).
			Context("error", "unsupported audio encoding").
			Build()
	}

	return output, nil
}

// GetRequiredFormat returns nil as gain processor can handle any format
func (gp *GainProcessor) GetRequiredFormat() *audiocore.AudioFormat {
	return nil
}

// GetOutputFormat returns the same format as input
func (gp *GainProcessor) GetOutputFormat(inputFormat audiocore.AudioFormat) audiocore.AudioFormat {
	return inputFormat
}

// SetGain updates the gain value
func (gp *GainProcessor) SetGain(gain float64) error {
	if gain < 0.0 || gain > 10.0 {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("gain", gain).
			Context("error", "gain must be between 0.0 and 10.0").
			Build()
	}

	gp.gain.Store(gain)
	gp.logger.Info("gain updated",
		"new_gain", gain)
	return nil
}

// GetGain returns the current gain value
func (gp *GainProcessor) GetGain() float64 {
	return gp.gain.Load().(float64)
}

// applyGainS16LE applies gain to 16-bit signed little-endian PCM samples
func (gp *GainProcessor) applyGainS16LE(buffer []byte, gain float64) {
	// Process in pairs of bytes (16-bit samples)
	for i := 0; i < len(buffer)-1; i += 2 {
		// Convert bytes to int16
		sample := int16(binary.LittleEndian.Uint16(buffer[i : i+2]))

		// Apply gain with clipping
		amplified := float64(sample) * gain
		if amplified > math.MaxInt16 {
			amplified = math.MaxInt16
		} else if amplified < math.MinInt16 {
			amplified = math.MinInt16
		}

		// Convert back to bytes
		binary.LittleEndian.PutUint16(buffer[i:i+2], uint16(int16(amplified)))
	}
}

// applyGainF32LE applies gain to 32-bit float little-endian PCM samples
func (gp *GainProcessor) applyGainF32LE(buffer []byte, gain float64) {
	// Process in groups of 4 bytes (32-bit float samples)
	for i := 0; i < len(buffer)-3; i += 4 {
		// Convert bytes to float32
		bits := binary.LittleEndian.Uint32(buffer[i : i+4])
		sample := math.Float32frombits(bits)

		// Apply gain with clipping to [-1.0, 1.0]
		amplified := float32(float64(sample) * gain)
		if amplified > 1.0 {
			amplified = 1.0
		} else if amplified < -1.0 {
			amplified = -1.0
		}

		// Convert back to bytes
		binary.LittleEndian.PutUint32(buffer[i:i+4], math.Float32bits(amplified))
	}
}