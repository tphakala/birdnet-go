package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestVolumeSuspendTracker_InitializeSource(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 3,
		MinResumeFrames:  2,
	}

	tracker.InitializeSource("test-source", cfg)

	assert.False(t, tracker.IsSuspended("test-source"), "source should not be suspended initially")
}

func TestVolumeSuspendTracker_SuspendOnLowVolume(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 3,
		MinResumeFrames:  2,
	}

	tracker.InitializeSource("test-source", cfg)

	// Send low volume frames (below threshold)
	for i := 0; i < 2; i++ {
		tracker.UpdateAudioLevel("test-source", 5)
		assert.False(t, tracker.IsSuspended("test-source"), "should not suspend before reaching min frames")
	}

	// Third low volume frame should trigger suspension
	tracker.UpdateAudioLevel("test-source", 5)
	assert.True(t, tracker.IsSuspended("test-source"))
}

func TestVolumeSuspendTracker_ResumeOnHighVolume(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 3,
		MinResumeFrames:  2,
	}

	tracker.InitializeSource("test-source", cfg)

	// Suspend first
	for i := 0; i < 3; i++ {
		tracker.UpdateAudioLevel("test-source", 5)
	}
	assert.True(t, tracker.IsSuspended("test-source"))

	// Send high volume frame (above resume threshold)
	tracker.UpdateAudioLevel("test-source", 25)
	assert.True(t, tracker.IsSuspended("test-source"), "should still be suspended after first high volume frame")

	// Second high volume frame should trigger resume
	tracker.UpdateAudioLevel("test-source", 25)
	assert.False(t, tracker.IsSuspended("test-source"))
}

func TestVolumeSuspendTracker_HysteresisZone(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 3,
		MinResumeFrames:  2,
	}

	tracker.InitializeSource("test-source", cfg)

	// Suspend first
	for i := 0; i < 3; i++ {
		tracker.UpdateAudioLevel("test-source", 5)
	}
	assert.True(t, tracker.IsSuspended("test-source"))

	// Send volume in hysteresis zone (between suspend and resume thresholds)
	// Should maintain suspended state without changing counters
	for i := 0; i < 5; i++ {
		tracker.UpdateAudioLevel("test-source", 15)
		assert.True(t, tracker.IsSuspended("test-source"), "should remain suspended in hysteresis zone")
	}
	assert.True(t, tracker.IsSuspended("test-source"))
}

func TestVolumeSuspendTracker_RemoveSource(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 3,
		MinResumeFrames:  2,
	}

	tracker.InitializeSource("test-source", cfg)
	tracker.RemoveSource("test-source")

	// After removal, should return false for non-existent source
	assert.False(t, tracker.IsSuspended("test-source"))
	tracker.UpdateAudioLevel("test-source", 5)
	assert.False(t, tracker.IsSuspended("test-source"), "removed source should remain absent")
}

func TestVolumeSuspendTracker_GetState(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 3,
		MinResumeFrames:  2,
	}

	tracker.InitializeSource("test-source", cfg)

	// Initially not suspended
	isSuspended, duration := tracker.GetState("test-source")
	assert.False(t, isSuspended)
	assert.Equal(t, time.Duration(0), duration)

	// Suspend
	for i := 0; i < 3; i++ {
		tracker.UpdateAudioLevel("test-source", 5)
	}

	// Check suspended state
	isSuspended, duration = tracker.GetState("test-source")
	assert.True(t, isSuspended)
	assert.GreaterOrEqual(t, duration, time.Duration(0), "suspend duration should be non-negative")
}

func TestVolumeSuspendTracker_DefaultFrameCounts(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	// Config with zero frame counts (should use defaults)
	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 0, // Should default to 3
		MinResumeFrames:  0, // Should default to 2
	}

	tracker.InitializeSource("test-source", cfg)

	// Should use default of 3 frames to suspend
	for i := 0; i < 2; i++ {
		tracker.UpdateAudioLevel("test-source", 5)
		assert.False(t, tracker.IsSuspended("test-source"))
	}
	tracker.UpdateAudioLevel("test-source", 5)
	assert.True(t, tracker.IsSuspended("test-source"), "should suspend after default 3 frames")
}

func TestVolumeSuspendTracker_ResumeDurationResetBug(t *testing.T) {
	tracker := NewVolumeSuspendTracker()

	cfg := conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 1,
		MinResumeFrames:  1,
	}

	tracker.InitializeSource("test-source", cfg)
	tracker.UpdateAudioLevel("test-source", 5)
	assert.True(t, tracker.IsSuspended("test-source"))

	_, durationWhileSuspended := tracker.GetState("test-source")
	assert.GreaterOrEqual(t, durationWhileSuspended, time.Duration(0))

	tracker.UpdateAudioLevel("test-source", 25)
	assert.False(t, tracker.IsSuspended("test-source"))

	isSuspended, duration := tracker.GetState("test-source")
	assert.False(t, isSuspended)
	assert.Equal(t, time.Duration(0), duration)
}
