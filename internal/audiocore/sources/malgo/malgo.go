// Package malgo provides a malgo-based soundcard audio source implementation
package malgo

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/malgo"
)

// MalgoSource implements audiocore.AudioSource using malgo for cross-platform audio capture
type MalgoSource struct {
	// Core fields
	id     string
	name   string
	config MalgoConfig

	// Malgo specific
	ctx    *malgo.AllocatedContext
	device *malgo.Device

	// Audio pipeline
	outputChan chan audiocore.AudioData
	errorChan  chan error

	// Buffer management
	bufferPool  audiocore.BufferPool
	convertPool *sync.Pool

	// State management
	mu      sync.RWMutex
	running atomic.Bool
	cancel  context.CancelFunc

	// Format info
	formatType malgo.FormatType
	actualRate uint32
	gain       atomic.Value // stores float64
}

// MalgoConfig contains configuration for the malgo audio source
type MalgoConfig struct {
	DeviceID     string
	DeviceName   string
	SampleRate   uint32
	Channels     uint8
	BufferFrames uint32
	Gain         float64
}

// NewMalgoSource creates a new malgo-based audio source
func NewMalgoSource(id string, config MalgoConfig, bufferPool audiocore.BufferPool) (*MalgoSource, error) {
	// Validate configuration
	if config.SampleRate == 0 {
		config.SampleRate = 48000
	}
	if config.Channels == 0 {
		config.Channels = 1
	}
	if config.BufferFrames == 0 {
		config.BufferFrames = 512
	}
	if config.Gain == 0 {
		config.Gain = 1.0
	}

	// Create source
	source := &MalgoSource{
		id:         id,
		name:       config.DeviceName,
		config:     config,
		outputChan: make(chan audiocore.AudioData, 10),
		errorChan:  make(chan error, 10),
		bufferPool: bufferPool,
		convertPool: &sync.Pool{
			New: func() any {
				// Pre-allocate conversion buffers based on typical frame size
				// For 512 frames * 2 bytes/sample = 1024 bytes
				buffer := make([]byte, config.BufferFrames*2)
				return &buffer
			},
		},
	}

	// Store initial gain
	source.gain.Store(config.Gain)

	return source, nil
}

// ID returns a unique identifier for this source
func (s *MalgoSource) ID() string {
	return s.id
}

// Name returns a human-readable name for this source
func (s *MalgoSource) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

// Start begins audio capture from this source
func (s *MalgoSource) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running.Load() {
		return errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryState).
			Context("source_id", s.id).
			Context("error", "source already running").
			Build()
	}

	// Initialize malgo context
	backend := s.getBackend()
	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("source_id", s.id).
			Context("backend", runtime.GOOS).
			Context("operation", "init_context").
			Build()
	}
	s.ctx = malgoCtx

	// Try to find and initialize the device
	deviceInfo, err := s.findDevice()
	if err != nil {
		_ = malgoCtx.Uninit()
		return err
	}

	// Configure device
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Channels = uint32(s.config.Channels)
	deviceConfig.Capture.DeviceID = deviceInfo.ID.Pointer()
	deviceConfig.SampleRate = s.config.SampleRate
	deviceConfig.Alsa.NoMMap = 1

	// Create context for cancellation
	captureCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Set up callbacks
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: s.onAudioData,
		Stop: s.onDeviceStop,
	}

	// Initialize device
	device, err := malgo.InitDevice(s.ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		s.cancel()
		_ = malgoCtx.Uninit()
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("source_id", s.id).
			Context("device_name", s.config.DeviceName).
			Context("operation", "init_device").
			Build()
	}
	s.device = device

	// Get actual format
	s.formatType = device.CaptureFormat()
	s.actualRate = device.SampleRate()

	// Start device
	if err := device.Start(); err != nil {
		device.Uninit()
		s.cancel()
		_ = malgoCtx.Uninit()
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("source_id", s.id).
			Context("operation", "start_device").
			Build()
	}

	s.running.Store(true)

	// Start monitoring goroutine
	go s.monitor(captureCtx)

	return nil
}

// Stop halts audio capture
func (s *MalgoSource) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running.Load() {
		return errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryState).
			Context("source_id", s.id).
			Context("error", "source not running").
			Build()
	}

	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}

	// Stop and cleanup device
	if s.device != nil {
		_ = s.device.Stop()
		s.device.Uninit()
		s.device = nil
	}

	// Cleanup context
	if s.ctx != nil {
		_ = s.ctx.Uninit()
		s.ctx = nil
	}

	s.running.Store(false)

	// Close channels
	close(s.outputChan)
	close(s.errorChan)

	return nil
}

// AudioOutput returns a channel that emits audio data
func (s *MalgoSource) AudioOutput() <-chan audiocore.AudioData {
	return s.outputChan
}

// Errors returns a channel for error reporting
func (s *MalgoSource) Errors() <-chan error {
	return s.errorChan
}

// IsActive returns true if the source is currently capturing
func (s *MalgoSource) IsActive() bool {
	return s.running.Load()
}

// GetFormat returns the audio format of this source
func (s *MalgoSource) GetFormat() audiocore.AudioFormat {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return the target format (always S16LE mono at configured sample rate)
	return audiocore.AudioFormat{
		SampleRate: int(s.config.SampleRate),
		Channels:   int(s.config.Channels),
		BitDepth:   16,
		Encoding:   "pcm_s16le",
	}
}

// SetGain sets the audio gain level (0.0 to 2.0)
func (s *MalgoSource) SetGain(gain float64) error {
	if gain < 0.0 || gain > 2.0 {
		return errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("gain", gain).
			Context("error", "gain must be between 0.0 and 2.0").
			Build()
	}

	s.gain.Store(gain)
	return nil
}

// onAudioData is called by malgo when audio data is available
func (s *MalgoSource) onAudioData(pSample2, pSamples []byte, framecount uint32) {
	// Convert if needed
	converted, err := s.convertAudio(pSamples)
	if err != nil {
		select {
		case s.errorChan <- err:
		default:
			// Error channel full, drop error
		}
		return
	}

	// Apply gain if needed
	gain := s.gain.Load().(float64)
	if gain != 1.0 {
		s.applyGain(converted, gain)
	}

	// Calculate duration based on frame count
	duration := time.Duration(float64(framecount) / float64(s.actualRate) * float64(time.Second))

	// Create AudioData
	data := audiocore.AudioData{
		Buffer:    converted,
		Format:    s.GetFormat(),
		Timestamp: time.Now(),
		Duration:  duration,
		SourceID:  s.id,
	}

	// Send to output channel (non-blocking)
	select {
	case s.outputChan <- data:
	default:
		// Channel full, drop frame
		err := errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryResource).
			Context("source_id", s.id).
			Context("error", "audio output channel full, dropping frame").
			Build()
		select {
		case s.errorChan <- err:
		default:
			// Error channel also full
		}
	}
}

// onDeviceStop is called when the device stops unexpectedly
func (s *MalgoSource) onDeviceStop() {
	err := errors.New(nil).
		Component("audiocore").
		Category(errors.CategoryAudio).
		Context("source_id", s.id).
		Context("error", "audio device stopped unexpectedly").
		Build()

	select {
	case s.errorChan <- err:
	default:
		// Error channel full
	}

	// Attempt to restart
	go func() {
		time.Sleep(1 * time.Second)
		if s.running.Load() && s.device != nil {
			if err := s.device.Start(); err != nil {
				restartErr := errors.New(err).
					Component("audiocore").
					Category(errors.CategoryAudio).
					Context("source_id", s.id).
					Context("operation", "restart_device").
					Build()
				select {
				case s.errorChan <- restartErr:
				default:
				}
			}
		}
	}()
}

// monitor monitors the context for cancellation
func (s *MalgoSource) monitor(ctx context.Context) {
	<-ctx.Done()
	// Context cancelled, ensure device is stopped
	_ = s.Stop()
}

// getBackend returns the appropriate backend for the current platform
func (s *MalgoSource) getBackend() malgo.Backend {
	switch runtime.GOOS {
	case "linux":
		return malgo.BackendAlsa
	case "windows":
		return malgo.BackendWasapi
	case "darwin":
		return malgo.BackendCoreaudio
	default:
		return malgo.BackendNull
	}
}

// applyGain applies gain to 16-bit audio samples
func (s *MalgoSource) applyGain(buffer []byte, gain float64) {
	// Process 16-bit samples
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

// findDevice finds the requested audio device
func (s *MalgoSource) findDevice() (*malgo.DeviceInfo, error) {
	devices, err := s.ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("source_id", s.id).
			Context("operation", "enumerate_devices").
			Build()
	}

	// If device name is empty, use default
	if s.config.DeviceName == "" || s.config.DeviceName == "default" {
		for i := range devices {
			if devices[i].IsDefault == 1 {
				return &devices[i], nil
			}
		}
		// No default found, use first device
		if len(devices) > 0 {
			return &devices[0], nil
		}
	}

	// Find device by name
	deviceInfo, err := SelectDevice(devices, s.config.DeviceName)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("source_id", s.id).
			Context("device_name", s.config.DeviceName).
			Build()
	}

	return deviceInfo, nil
}

// convertAudio handles format conversion if needed
func (s *MalgoSource) convertAudio(input []byte) ([]byte, error) {
	// If already S16, return as-is
	if s.formatType == malgo.FormatS16 {
		// Make a copy to avoid modifying the original buffer
		output := make([]byte, len(input))
		copy(output, input)
		return output, nil
	}

	// Use ConvertToS16 for other formats
	return ConvertToS16(input, s.formatType, nil)
}
