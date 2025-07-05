// Package adapter provides adapters between audiocore and legacy myaudio system
package adapter

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// BufferBridge bridges audiocore sources to myaudio buffers
type BufferBridge struct {
	source   audiocore.AudioSource
	sourceID string
	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
	logger   *slog.Logger
}

// NewBufferBridge creates a new buffer bridge
func NewBufferBridge(source audiocore.AudioSource, sourceID string) *BufferBridge {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(
		"component", "buffer_bridge",
		"source_id", sourceID)

	return &BufferBridge{
		source:   source,
		sourceID: sourceID,
		stopChan: make(chan struct{}),
		logger:   logger,
	}
}

// Start begins forwarding audio data from the source to myaudio buffers
func (b *BufferBridge) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryState).
			Context("source_id", b.sourceID).
			Context("error", "bridge already running").
			Build()
	}

	// Start the source
	if err := b.source.Start(ctx); err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("source_id", b.sourceID).
			Context("operation", "start_source").
			Build()
	}

	b.running = true
	b.wg.Add(2) // One for audio processing, one for error handling
	go b.processAudio()
	go b.handleErrors()

	return nil
}

// Stop halts the bridge and stops the source
func (b *BufferBridge) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return nil
	}

	// Stop the source
	if err := b.source.Stop(); err != nil {
		// Log error but continue with cleanup to ensure bridge stops properly
		b.logger.Warn("failed to stop audio source during bridge shutdown",
			"error", err)
	}

	// Signal stop
	close(b.stopChan)
	b.running = false

	// Wait for goroutines to finish
	b.wg.Wait()

	return nil
}

// processAudio reads from the audiocore source and writes to myaudio buffers
func (b *BufferBridge) processAudio() {
	defer b.wg.Done()

	frameCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case data, ok := <-b.source.AudioOutput():
			if !ok {
				// Channel closed, source stopped
				return
			}

			// Write to analysis buffer
			if err := myaudio.WriteToAnalysisBuffer(b.sourceID, data.Buffer); err != nil {
				// Log error but continue processing to avoid blocking
				if time.Since(lastLogTime) > 5*time.Minute {
					b.logger.Error("failed to write to analysis buffer",
						"error", err,
						"buffer_size", len(data.Buffer),
						"frames_processed", frameCount)
					lastLogTime = time.Now()
				}
			}

			// Write to capture buffer
			_ = myaudio.WriteToCaptureBuffer(b.sourceID, data.Buffer) // Ignore error and continue

			// Broadcast audio data for any listeners
			myaudio.BroadcastAudioData(b.sourceID, data.Buffer)

			frameCount++
			// Progress logging could be added here if needed

		case <-b.stopChan:
			return
		}
	}
}

// handleErrors processes errors from the audio source
func (b *BufferBridge) handleErrors() {
	defer b.wg.Done()

	for {
		select {
		case err, ok := <-b.source.Errors():
			if !ok {
				// Channel closed
				return
			}

			// Log the error for visibility and debugging
			b.logger.Error("audio source reported error",
				"error", err,
				"source_type", b.source.Name())

		case <-b.stopChan:
			return
		}
	}
}

// GetSource returns the underlying audio source
func (b *BufferBridge) GetSource() audiocore.AudioSource {
	return b.source
}

// IsRunning returns whether the bridge is active
func (b *BufferBridge) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}
