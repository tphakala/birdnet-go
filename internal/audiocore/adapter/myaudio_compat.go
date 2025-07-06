// Package adapter provides compatibility between audiocore and myaudio packages
package adapter

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/capture"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
	"github.com/tphakala/birdnet-go/internal/audiocore/processors"
	"github.com/tphakala/birdnet-go/internal/audiocore/sources"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// MyAudioCompatAdapter bridges audiocore with the existing myaudio interface
type MyAudioCompatAdapter struct {
	manager        audiocore.AudioManager
	captureManager capture.Manager
	exportManager  *export.Manager
	settings       *conf.Settings
	ctx            context.Context
	cancel         context.CancelFunc
	wg             *sync.WaitGroup
	outputChan     chan myaudio.UnifiedAudioData
	quitChan       chan struct{}
	restartChan    chan struct{}
}

// NewMyAudioCompatAdapter creates a new adapter that implements myaudio.CaptureAudio interface using audiocore
func NewMyAudioCompatAdapter(settings *conf.Settings) *MyAudioCompatAdapter {
	// Create audio manager configuration
	managerConfig := &audiocore.ManagerConfig{
		MaxSources:        10,
		DefaultBufferSize: 4096,
		EnableMetrics:     settings.Sentry.Enabled,
		MetricsInterval:   10 * time.Second,
		ProcessingTimeout: 5 * time.Second,
		BufferPoolConfig: audiocore.BufferPoolConfig{
			SmallBufferSize:   4 * 1024,
			MediumBufferSize:  64 * 1024,
			LargeBufferSize:   1024 * 1024,
			MaxBuffersPerSize: 100,
			EnableMetrics:     settings.Sentry.Enabled,
		},
	}

	// Create export manager with FFmpeg if available
	ffmpegPath := settings.Realtime.Audio.FfmpegPath
	exportManager := export.DefaultManager(ffmpegPath)

	// Create audio manager
	manager := audiocore.NewAudioManager(managerConfig)

	// Create capture manager
	bufferPool := audiocore.NewBufferPool(managerConfig.BufferPoolConfig)
	captureManager := capture.NewManager(bufferPool, exportManager)

	// Set capture manager in audio manager
	if err := manager.SetCaptureManager(captureManager); err != nil {
		log.Printf("Failed to set capture manager: %v", err)
	}

	return &MyAudioCompatAdapter{
		manager:        manager,
		captureManager: captureManager,
		exportManager:  exportManager,
		settings:       settings,
	}
}

// CaptureAudio starts audio capture using audiocore, compatible with myaudio.CaptureAudio
func (a *MyAudioCompatAdapter) CaptureAudio(
	settings *conf.Settings,
	wg *sync.WaitGroup,
	quitChan chan struct{},
	restartChan chan struct{},
	unifiedAudioChan chan myaudio.UnifiedAudioData,
) {
	a.settings = settings
	a.wg = wg
	a.quitChan = quitChan
	a.restartChan = restartChan
	a.outputChan = unifiedAudioChan

	wg.Add(1)
	defer wg.Done()

	// Create context for audiocore
	a.ctx, a.cancel = context.WithCancel(context.Background())
	defer a.cancel()

	// Set up audio sources
	if err := a.setupAudioSources(); err != nil {
		log.Printf("Failed to setup audio sources: %v", err)
		return
	}

	// Start the audio manager
	if err := a.manager.Start(a.ctx); err != nil {
		log.Printf("Failed to start audio manager: %v", err)
		return
	}
	defer func() {
		if err := a.manager.Stop(); err != nil {
			log.Printf("Error stopping audio manager: %v", err)
		}
	}()

	// Start processing audio data
	a.processAudioData()
}

// setupAudioSources configures audio sources based on settings
func (a *MyAudioCompatAdapter) setupAudioSources() error {
	// Check if we have RTSP URLs configured
	if len(a.settings.Realtime.RTSP.URLs) > 0 {
		// TODO: Implement RTSP source when available
		log.Printf("RTSP sources not yet implemented in audiocore")
		return nil
	}

	// Set up soundcard source
	sourceConfig := &audiocore.SourceConfig{
		ID:     "soundcard",
		Name:   "System Audio",
		Type:   "soundcard",
		Device: a.settings.Realtime.Audio.Source,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		BufferSize: 4096,
		Gain:       1.0,
	}

	// Create soundcard source
	source, err := sources.NewSoundcardSource(sourceConfig)
	if err != nil {
		return err
	}

	// Add source to manager
	if err := a.manager.AddSource(source); err != nil {
		return err
	}

	// Set up processor chain if needed
	if a.settings.Realtime.Audio.Equalizer.Enabled {
		// Create processor chain
		chain := audiocore.NewProcessorChain()

		// Add gain processor if needed
		// TODO: Add equalizer processor when implemented
		gainProc, err := processors.NewGainProcessor("gain", 1.0)
		if err != nil {
			return err
		}
		if err := chain.AddProcessor(gainProc); err != nil {
			return err
		}

		// Set processor chain for the source
		if err := a.manager.SetProcessorChain(source.ID(), chain); err != nil {
			return err
		}
	}

	// Configure capture for this source if export is enabled
	if a.settings.Realtime.Audio.Export.Enabled {
		captureConfig := capture.Config{
			Duration:   60 * time.Second, // 60 second buffer like myaudio
			Format:     sourceConfig.Format,
			PreBuffer:  2 * time.Second,  // 2 seconds before detection
			PostBuffer: 13 * time.Second, // 13 seconds after detection (total 15s)
			ExportConfig: &export.Config{
				Format:           export.Format(a.settings.Realtime.Audio.Export.Type),
				OutputPath:       a.settings.Realtime.Audio.Export.Path,
				FileNameTemplate: "{source}_{timestamp}",
				Bitrate:          a.settings.Realtime.Audio.Export.Bitrate,
				FFmpegPath:       a.settings.Realtime.Audio.FfmpegPath,
				EnableDebug:      a.settings.Realtime.Audio.Export.Debug,
				Timeout:          30 * time.Second,
			},
		}

		if err := a.captureManager.EnableCapture(sourceConfig.ID, captureConfig); err != nil {
			log.Printf("Failed to enable capture for source %s: %v", sourceConfig.ID, err)
		}
	}

	return nil
}

// processAudioData processes audio from audiocore and converts to myaudio format
func (a *MyAudioCompatAdapter) processAudioData() {
	// Buffer for audio level calculation - removed as not needed for simplified implementation

	// Sound level monitoring
	var soundLevelAnalyzer *SoundLevelAnalyzer
	if a.settings.Realtime.Audio.SoundLevel.Enabled {
		soundLevelAnalyzer = NewSoundLevelAnalyzer(a.settings.Realtime.Audio.SoundLevel.Interval)
	}

	for {
		select {
		case <-a.quitChan:
			return

		case <-a.restartChan:
			// Handle restart
			log.Println("Audio capture restart requested")
			return

		case audioData := <-a.manager.AudioOutput():
			// Convert audiocore.AudioData to myaudio format

			// Write to analysis buffer (myaudio compatibility)
			if err := myaudio.WriteToAnalysisBuffer(audioData.SourceID, audioData.Buffer); err != nil {
				log.Printf("Error writing to analysis buffer: %v", err)
			}

			// Write to audiocore capture buffer if capture is enabled
			if a.captureManager.IsCaptureEnabled(audioData.SourceID) {
				if err := a.captureManager.Write(audioData.SourceID, &audioData); err != nil {
					log.Printf("Error writing to audiocore capture buffer: %v", err)
				}
			} else {
				// Fall back to myaudio capture buffer for compatibility
				if err := myaudio.WriteToCaptureBuffer(audioData.SourceID, audioData.Buffer); err != nil {
					log.Printf("Error writing to capture buffer: %v", err)
				}
			}

			// Calculate audio level
			audioLevel := a.calculateAudioLevel(audioData.Buffer)

			// Create unified audio data
			unifiedData := myaudio.UnifiedAudioData{
				AudioLevel: myaudio.AudioLevelData{
					Level:    int(audioLevel * 100), // Convert to 0-100 scale
					Clipping: audioLevel > 0.95,
					Source:   audioData.SourceID,
					Name:     "AudioCore Source",
				},
				Timestamp: audioData.Timestamp,
			}

			// Add sound level data if enabled
			if soundLevelAnalyzer != nil {
				soundLevel := soundLevelAnalyzer.Process(audioData.Buffer)
				if soundLevel != nil {
					unifiedData.SoundLevel = soundLevel
				}
			}

			// Send to output channel
			select {
			case a.outputChan <- unifiedData:
			default:
				// Channel full, drop data
			}
		}
	}
}

// calculateAudioLevel calculates the audio level from PCM data
func (a *MyAudioCompatAdapter) calculateAudioLevel(buffer []byte) float32 {
	if len(buffer) < 2 {
		return 0
	}

	var sum float64
	samples := len(buffer) / 2

	for i := 0; i < len(buffer)-1; i += 2 {
		// Convert bytes to int16 (assuming little-endian)
		sample := int16(buffer[i]) | (int16(buffer[i+1]) << 8)
		sum += float64(sample) * float64(sample)
	}

	// Calculate RMS
	rms := math.Sqrt(sum / float64(samples))
	return float32(rms / 32768.0) // Normalize to 0-1 range
}

// TimeProvider interface allows for time mocking in tests
type TimeProvider interface {
	Now() time.Time
}

// RealTimeProvider implements TimeProvider using actual time
type RealTimeProvider struct{}

// Now returns the current time
func (RealTimeProvider) Now() time.Time {
	return time.Now()
}

// SoundLevelAnalyzer provides compatibility for sound level monitoring
type SoundLevelAnalyzer struct {
	interval     int
	buffer       []float32
	lastUpdate   time.Time
	timeProvider TimeProvider
}

// NewSoundLevelAnalyzer creates a new sound level analyzer
func NewSoundLevelAnalyzer(interval int) *SoundLevelAnalyzer {
	return NewSoundLevelAnalyzerWithTimeProvider(interval, RealTimeProvider{})
}

// NewSoundLevelAnalyzerWithTimeProvider creates a new sound level analyzer with custom time provider
func NewSoundLevelAnalyzerWithTimeProvider(interval int, timeProvider TimeProvider) *SoundLevelAnalyzer {
	return &SoundLevelAnalyzer{
		interval:     interval,
		buffer:       make([]float32, 0),
		lastUpdate:   timeProvider.Now(),
		timeProvider: timeProvider,
	}
}

// Process analyzes audio data for sound levels
func (s *SoundLevelAnalyzer) Process(audioData []byte) *myaudio.SoundLevelData {
	// This is a simplified implementation
	// In production, you would implement proper 1/3 octave band analysis

	now := s.timeProvider.Now()
	if now.Sub(s.lastUpdate) < time.Duration(s.interval)*time.Second {
		return nil
	}

	s.lastUpdate = now

	// Create mock sound level data for compatibility
	octaveBands := make(map[string]myaudio.OctaveBandData)
	// Add some mock octave band data
	freqs := []float64{125, 250, 500, 1000, 2000, 4000}
	for _, freq := range freqs {
		freqStr := fmt.Sprintf("%.0f", freq)
		octaveBands[freqStr] = myaudio.OctaveBandData{
			CenterFreq:  freq,
			Min:         55.0, // Mock dB level
			Max:         65.0,
			Mean:        60.0,
			SampleCount: 100,
		}
	}

	return &myaudio.SoundLevelData{
		Timestamp:   now,
		Source:      "audiocore",
		Name:        "AudioCore Source",
		Duration:    s.interval,
		OctaveBands: octaveBands,
	}
}

// StartAudioCoreCapture is the entry point that replaces myaudio.CaptureAudio when UseAudioCore is enabled
func StartAudioCoreCapture(
	settings *conf.Settings,
	wg *sync.WaitGroup,
	quitChan chan struct{},
	restartChan chan struct{},
	unifiedAudioChan chan myaudio.UnifiedAudioData,
) {
	adapter := NewMyAudioCompatAdapter(settings)
	adapter.CaptureAudio(settings, wg, quitChan, restartChan, unifiedAudioChan)
}
