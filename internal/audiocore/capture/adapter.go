package capture

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// AudioCoreCaptureAdapter adapts our enhanced Manager to audiocore.CaptureManager interface
type AudioCoreCaptureAdapter struct {
	manager Manager
}

// NewAudioCoreCaptureAdapter creates an adapter that implements audiocore.CaptureManager
func NewAudioCoreCaptureAdapter(manager Manager) audiocore.CaptureManager {
	return &AudioCoreCaptureAdapter{
		manager: manager,
	}
}

// Write writes audio data to capture buffer
func (a *AudioCoreCaptureAdapter) Write(sourceID string, data *audiocore.AudioData) error {
	return a.manager.Write(sourceID, data)
}

// SaveClip saves an audio clip from the buffer (returns raw bytes as expected by audiocore)
func (a *AudioCoreCaptureAdapter) SaveClip(sourceID string, timestamp time.Time, duration time.Duration) ([]byte, error) {
	audioData, err := a.manager.SaveClip(sourceID, timestamp, duration)
	if err != nil {
		return nil, err
	}
	return audioData.Buffer, nil
}