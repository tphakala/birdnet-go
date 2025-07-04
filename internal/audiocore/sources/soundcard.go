// Package sources provides audio source implementations for the audiocore package
package sources

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// SoundcardSource represents an audio source from a system soundcard
type SoundcardSource struct {
	config      audiocore.SourceConfig
	format      audiocore.AudioFormat
	audioOutput chan audiocore.AudioData
	errorOutput chan error
	isActive    atomic.Bool
	gain        atomic.Value // stores float64
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	closeOnce   sync.Once
	closed      bool
	logger      *slog.Logger

	// Device-specific fields (to be implemented with actual audio library)
	deviceID   string
	bufferSize int
}

// NewSoundcardSource creates a new soundcard audio source
func NewSoundcardSource(config *audiocore.SourceConfig) (audiocore.AudioSource, error) {
	// Validate configuration
	if config.Device == "" {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "device ID cannot be empty").
			Build()
	}

	// Set default format if not specified
	if config.Format.SampleRate == 0 {
		config.Format = audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		}
	}

	// Set default buffer size if not specified
	bufferSize := config.BufferSize
	if bufferSize == 0 {
		bufferSize = 4096
	}

	// Set default gain if not specified
	if config.Gain == 0 {
		config.Gain = 1.0
	}

	// Create logger
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(
		"component", "soundcard_source",
		"source_id", config.ID,
		"device", config.Device)

	source := &SoundcardSource{
		config:      *config,
		format:      config.Format,
		audioOutput: make(chan audiocore.AudioData, 10),
		errorOutput: make(chan error, 10),
		deviceID:    config.Device,
		bufferSize:  bufferSize,
		logger:      logger,
	}

	// Store initial gain
	source.gain.Store(config.Gain)

	logger.Info("soundcard source created",
		"format", config.Format,
		"buffer_size", bufferSize,
		"gain", config.Gain)

	return source, nil
}

// ID returns a unique identifier for this source
func (s *SoundcardSource) ID() string {
	return s.config.ID
}

// Name returns a human-readable name for this source
func (s *SoundcardSource) Name() string {
	return s.config.Name
}

// Start begins audio capture from this source
func (s *SoundcardSource) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isActive.Load() {
		s.logger.Warn("attempted to start already active source")
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryState).
			Context("error", "source already active").
			Context("source_id", s.ID()).
			Build()
	}

	// Create cancellable context
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Mark as active
	s.isActive.Store(true)

	s.logger.Info("starting audio capture")

	// Start capture goroutine
	s.wg.Add(1)
	go s.captureAudio()

	return nil
}

// Stop halts audio capture
func (s *SoundcardSource) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isActive.Load() {
		s.logger.Warn("attempted to stop inactive source")
		return errors.New(audiocore.ErrSourceNotActive).
			Component(audiocore.ComponentAudioCore).
			Context("source_id", s.ID()).
			Build()
	}

	// Cancel context to stop capture
	s.logger.Info("stopping audio capture")
	s.cancel()

	// Wait for capture to stop
	s.wg.Wait()

	// Mark as inactive
	s.isActive.Store(false)

	// Close channels only once
	s.closeOnce.Do(func() {
		close(s.audioOutput)
		close(s.errorOutput)
		s.closed = true
		s.logger.Debug("channels closed")
	})

	s.logger.Info("audio capture stopped")
	return nil
}

// AudioOutput returns a channel that emits audio data
func (s *SoundcardSource) AudioOutput() <-chan audiocore.AudioData {
	return s.audioOutput
}

// Errors returns a channel for error reporting
func (s *SoundcardSource) Errors() <-chan error {
	return s.errorOutput
}

// IsActive returns true if the source is currently capturing
func (s *SoundcardSource) IsActive() bool {
	return s.isActive.Load()
}

// GetFormat returns the audio format of this source
func (s *SoundcardSource) GetFormat() audiocore.AudioFormat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.format
}

// SetGain sets the audio gain level (0.0 to 1.0)
func (s *SoundcardSource) SetGain(gain float64) error {
	if gain < 0.0 || gain > 2.0 {
		s.logger.Error("invalid gain value",
			"gain", gain,
			"valid_range", "0.0-2.0")
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("gain", gain).
			Context("error", "gain must be between 0.0 and 2.0").
			Build()
	}

	s.gain.Store(gain)
	s.logger.Debug("gain updated",
		"new_gain", gain)
	return nil
}

// captureAudio is the main capture loop
func (s *SoundcardSource) captureAudio() {
	defer s.wg.Done()

	s.logger.Debug("audio capture goroutine started")

	// Calculate frame duration based on buffer size and sample rate
	samplesPerBuffer := s.bufferSize / (s.format.BitDepth / 8) / s.format.Channels
	frameDuration := time.Duration(float64(samplesPerBuffer) / float64(s.format.SampleRate) * float64(time.Second))

	// TODO: Initialize actual audio device here
	// For now, this is a placeholder that generates silence

	// Simulate audio capture
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	s.logger.Debug("audio capture initialized",
		"frame_duration", frameDuration,
		"samples_per_buffer", samplesPerBuffer)

	frameCount := 0
	for {
		select {
		case <-s.ctx.Done():
			s.logger.Debug("audio capture goroutine stopping",
				"frames_captured", frameCount)
			return

		case <-ticker.C:
			// Create audio buffer (silence for now)
			buffer := make([]byte, s.bufferSize)

			// Apply gain
			gain := s.gain.Load().(float64)
			if gain != 1.0 {
				s.applyGain(buffer, gain)
			}

			// Create audio data
			audioData := audiocore.AudioData{
				Buffer:    buffer,
				Format:    s.format,
				Timestamp: time.Now(),
				Duration:  frameDuration,
				SourceID:  s.ID(),
			}

			// Send audio data
			select {
			case s.audioOutput <- audioData:
				frameCount++
				if frameCount%1000 == 0 {
					s.logger.Debug("audio capture progress",
						"frames_captured", frameCount)
				}
			case <-s.ctx.Done():
				return
			default:
				// Channel full, report error
				s.logger.Warn("audio output channel full, dropping frame",
					"frame", frameCount)
				err := errors.New(nil).
					Component(audiocore.ComponentAudioCore).
					Category(errors.CategoryResource).
					Context("operation", "audio_output").
					Context("frame", frameCount).
					Context("error", "audio output channel full, dropping frame").
					Build()
				select {
				case s.errorOutput <- err:
				default:
					s.logger.Debug("error channel also full")
				}
			}
		}
	}
}

// applyGain applies gain to audio samples
func (s *SoundcardSource) applyGain(buffer []byte, gain float64) {
	// This is a simplified implementation for 16-bit samples
	// In a real implementation, this would handle different bit depths
	if s.format.BitDepth == 16 {
		for i := 0; i < len(buffer)-1; i += 2 {
			// Convert bytes to int16
			sample := int16(buffer[i]) | (int16(buffer[i+1]) << 8)

			// Apply gain
			amplified := float64(sample) * gain

			// Clamp to prevent overflow
			if amplified > 32767 {
				amplified = 32767
			} else if amplified < -32768 {
				amplified = -32768
			}

			// Convert back to int16
			sample = int16(amplified)

			// Convert back to bytes
			buffer[i] = byte(sample)
			buffer[i+1] = byte(sample >> 8)
		}
	}
}
