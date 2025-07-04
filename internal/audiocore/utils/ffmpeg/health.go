package ffmpeg

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// healthChecker implements the HealthChecker interface
type healthChecker struct {
	silenceThreshold float32
	silenceDuration  time.Duration
	audioLevels      map[string]*audioLevelTracker
	mu               sync.RWMutex
}

// audioLevelTracker tracks audio levels for silence detection
type audioLevelTracker struct {
	lastAudioTime   time.Time
	lastAudioLevel  float32
	silenceStart    time.Time
	sampleCount     int64
	totalSamples    int64
	avgLevel        float32
}

// NewHealthChecker creates a new health checker
func NewHealthChecker() HealthChecker {
	return &healthChecker{
		silenceThreshold: -60.0, // -60 dB default threshold
		silenceDuration:  60 * time.Second,
		audioLevels:      make(map[string]*audioLevelTracker),
	}
}

// Check performs a health check on the process
func (h *healthChecker) Check(process Process) error {
	if !process.IsRunning() {
		return errors.New(fmt.Errorf("process not running")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("process_id", process.ID()).
			Build()
	}

	// Check if process is producing audio data
	if err := h.checkSilence(process); err != nil {
		return err
	}

	// Check process metrics
	metrics := process.Metrics()
	
	// Check if process has been restarting too frequently
	if metrics.RestartCount > 10 && 
		time.Since(metrics.LastRestart) < 5*time.Minute {
		return errors.New(fmt.Errorf("process restarting too frequently")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("process_id", process.ID()).
			Context("restart_count", fmt.Sprintf("%d", metrics.RestartCount)).
			Build()
	}

	// Check for recent errors
	if metrics.LastError != nil && 
		time.Since(metrics.LastRestart) < 30*time.Second {
		return errors.New(fmt.Errorf("recent error detected")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("process_id", process.ID()).
			Context("error", metrics.LastError.Error()).
			Build()
	}

	return nil
}

// SetSilenceThreshold sets the silence detection threshold
func (h *healthChecker) SetSilenceThreshold(threshold float32, duration time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	h.silenceThreshold = threshold
	h.silenceDuration = duration
}

// checkSilence checks if the process has been silent for too long
func (h *healthChecker) checkSilence(process Process) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	processID := process.ID()
	tracker, exists := h.audioLevels[processID]
	if !exists {
		// Initialize tracker for new process
		tracker = &audioLevelTracker{
			lastAudioTime: time.Now(),
		}
		h.audioLevels[processID] = tracker
		
		// Start monitoring audio levels for this process
		go h.monitorAudioLevels(process, tracker)
		return nil
	}

	// Check if we've been silent for too long
	if !tracker.silenceStart.IsZero() {
		silenceDuration := time.Since(tracker.silenceStart)
		if silenceDuration > h.silenceDuration {
			return errors.New(fmt.Errorf("silence detected for %v", silenceDuration)).
				Component("audiocore").
				Category(errors.CategoryAudio).
				Context("process_id", processID).
				Context("silence_duration", silenceDuration.String()).
				Context("threshold_db", fmt.Sprintf("%.1f", h.silenceThreshold)).
				Build()
		}
	}

	// Check if we haven't received any audio data recently
	if time.Since(tracker.lastAudioTime) > 30*time.Second {
		return errors.New(fmt.Errorf("no audio data received")).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("process_id", processID).
			Context("last_audio", tracker.lastAudioTime.Format(time.RFC3339)).
			Build()
	}

	return nil
}

// monitorAudioLevels monitors audio levels from a process
func (h *healthChecker) monitorAudioLevels(process Process, tracker *audioLevelTracker) {
	defer func() {
		// Clean up tracker when process stops
		h.mu.Lock()
		delete(h.audioLevels, process.ID())
		h.mu.Unlock()
	}()

	audioOutput := process.AudioOutput()
	for audioData := range audioOutput {
		if len(audioData) == 0 {
			continue
		}

		// Calculate audio level (RMS)
		level := h.calculateAudioLevel(audioData)
		
		h.mu.Lock()
		tracker.lastAudioTime = time.Now()
		tracker.lastAudioLevel = level
		tracker.sampleCount++
		tracker.totalSamples += int64(len(audioData) / 2) // Assuming 16-bit samples
		
		// Update running average
		alpha := float32(0.1) // Smoothing factor
		if tracker.sampleCount == 1 {
			tracker.avgLevel = level
		} else {
			tracker.avgLevel = alpha*level + (1-alpha)*tracker.avgLevel
		}

		// Check for silence
		levelDB := h.amplitudeToDecibels(level)
		if levelDB < h.silenceThreshold {
			if tracker.silenceStart.IsZero() {
				tracker.silenceStart = time.Now()
			}
		} else {
			tracker.silenceStart = time.Time{} // Reset silence timer
		}
		h.mu.Unlock()
	}
}

// calculateAudioLevel calculates RMS level from audio data
func (h *healthChecker) calculateAudioLevel(data []byte) float32 {
	if len(data) < 2 {
		return 0
	}

	// Assume 16-bit little-endian samples
	var sum float64
	sampleCount := len(data) / 2

	for i := 0; i < len(data)-1; i += 2 {
		// Convert to 16-bit signed integer
		sample := int16(data[i]) | int16(data[i+1])<<8
		normalized := float64(sample) / 32768.0
		sum += normalized * normalized
	}

	if sampleCount == 0 {
		return 0
	}

	rms := math.Sqrt(sum / float64(sampleCount))
	return float32(rms)
}

// amplitudeToDecibels converts amplitude to decibels
func (h *healthChecker) amplitudeToDecibels(amplitude float32) float32 {
	if amplitude <= 0 {
		return -100.0 // Very low level
	}
	return float32(20.0 * math.Log10(float64(amplitude)))
}

// GetAudioLevelStats returns audio level statistics for a process
func (h *healthChecker) GetAudioLevelStats(processID string) (avgLevel, lastLevel float32, sampleCount int64, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	tracker, exists := h.audioLevels[processID]
	if !exists {
		return 0, 0, 0, false
	}

	return tracker.avgLevel, tracker.lastAudioLevel, tracker.sampleCount, true
}