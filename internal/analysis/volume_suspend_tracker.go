package analysis

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// VolumeSuspendState tracks the suspension state for a single audio source.
type VolumeSuspendState struct {
	isSuspended      bool
	lowVolumeCount   int
	highVolumeCount  int
	lastStateChange  time.Time
	lastLogTime      time.Time
	suspendThreshold int
	resumeThreshold  int
	minSuspendFrames int
	minResumeFrames  int
}

// VolumeSuspendTracker manages volume-based analysis suspension for all audio sources.
type VolumeSuspendTracker struct {
	mu     sync.RWMutex
	states map[string]*VolumeSuspendState // keyed by source ID
}

// NewVolumeSuspendTracker creates a new volume suspend tracker.
func NewVolumeSuspendTracker() *VolumeSuspendTracker {
	return &VolumeSuspendTracker{
		states: make(map[string]*VolumeSuspendState),
	}
}

// InitializeSource initializes tracking for a source with the given configuration.
func (t *VolumeSuspendTracker) InitializeSource(sourceID string, cfg conf.LowNoiseAutoSuspendSettings) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Set defaults if not configured
	minSuspendFrames := cfg.MinSuspendFrames
	if minSuspendFrames <= 0 {
		minSuspendFrames = 3
	}

	minResumeFrames := cfg.MinResumeFrames
	if minResumeFrames <= 0 {
		minResumeFrames = 2
	}

	t.states[sourceID] = &VolumeSuspendState{
		isSuspended:      false,
		lowVolumeCount:   0,
		highVolumeCount:  0,
		lastStateChange:  time.Now(),
		lastLogTime:      time.Time{},
		suspendThreshold: cfg.SuspendThreshold,
		resumeThreshold:  cfg.ResumeThreshold,
		minSuspendFrames: minSuspendFrames,
		minResumeFrames:  minResumeFrames,
	}
}

// RemoveSource removes tracking for a source.
func (t *VolumeSuspendTracker) RemoveSource(sourceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, sourceID)
}

// UpdateAudioLevel updates suspend/resume state for a source based on audio level.
// This is the write path and is intended to be called from the audio pipeline layer.
func (t *VolumeSuspendTracker) UpdateAudioLevel(sourceID string, audioLevel int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.states[sourceID]
	if !exists {
		return
	}

	log := GetLogger()
	now := time.Now()

	// Update counters based on current audio level
	if audioLevel <= state.suspendThreshold {
		state.lowVolumeCount++
		state.highVolumeCount = 0
	} else if audioLevel >= state.resumeThreshold {
		state.highVolumeCount++
		state.lowVolumeCount = 0
	} else {
		// In the hysteresis zone - maintain current state without changing counters
	}

	if !state.isSuspended && state.lowVolumeCount >= state.minSuspendFrames {
		// Transition to suspended
		state.isSuspended = true
		state.lastStateChange = now
		state.lowVolumeCount = 0
		state.highVolumeCount = 0

		log.Info("analysis suspended due to low audio level",
			logger.String("source", sourceID),
			logger.Int("audio_level", audioLevel),
			logger.Int("suspend_threshold", state.suspendThreshold))
	} else if state.isSuspended && state.highVolumeCount >= state.minResumeFrames {
		// Transition to active
		state.isSuspended = false
		suspendDuration := now.Sub(state.lastStateChange)
		state.lastStateChange = now
		state.lowVolumeCount = 0
		state.highVolumeCount = 0

		log.Info("analysis resumed due to high audio level",
			logger.String("source", sourceID),
			logger.Int("audio_level", audioLevel),
			logger.Int("resume_threshold", state.resumeThreshold),
			logger.Duration("suspended_duration", suspendDuration))
	}

	// Periodic logging when suspended (every 5 minutes)
	if state.isSuspended && now.Sub(state.lastLogTime) >= 5*time.Minute {
		state.lastLogTime = now
		suspendDuration := now.Sub(state.lastStateChange)
		log.Debug("analysis still suspended",
			logger.String("source", sourceID),
			logger.Int("audio_level", audioLevel),
			logger.Duration("suspended_duration", suspendDuration))
	}

}

// IsSuspended returns whether analysis is currently suspended for the given source.
func (t *VolumeSuspendTracker) IsSuspended(sourceID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.states[sourceID]
	if !exists {
		return false
	}
	return state.isSuspended
}

// GetState returns a copy of the current state for a source (for metrics/debugging).
func (t *VolumeSuspendTracker) GetState(sourceID string) (isSuspended bool, suspendDuration time.Duration) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.states[sourceID]
	if !exists {
		return false, 0
	}

	duration := time.Duration(0)
	if state.isSuspended {
		duration = time.Since(state.lastStateChange)
	}

	return state.isSuspended, duration
}
