package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/health"
)

// SourceStatusCheck monitors the health state of audio sources via the liveness watchdog.
type SourceStatusCheck struct {
	getSnapshots func() []audiocore.SourceHealthSnapshot
}

// NewSourceStatusCheck creates a SourceStatusCheck using the given snapshot provider.
func NewSourceStatusCheck(getSnapshots func() []audiocore.SourceHealthSnapshot) *SourceStatusCheck {
	return &SourceStatusCheck{getSnapshots: getSnapshots}
}

// Name returns the check identifier.
func (c *SourceStatusCheck) Name() string { return "source_status" }

// Category returns the audio category.
func (c *SourceStatusCheck) Category() health.Category { return health.CategoryAudio }

// Run evaluates the health state of all audio sources.
func (c *SourceStatusCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getSnapshots == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	snaps := c.getSnapshots()
	if len(snaps) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	type sourceEntry struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}

	entries := make([]sourceEntry, 0, len(snaps))
	status := health.StatusHealthy
	var msg string

	for _, s := range snaps {
		entries = append(entries, sourceEntry{ID: s.SourceID, State: s.State})
		switch s.State {
		case "failed", "escalated":
			status = health.StatusCritical
		case "alarmed":
			if status != health.StatusCritical {
				status = health.StatusWarning
			}
		}
	}

	switch status {
	case health.StatusCritical:
		msg = "One or more audio sources have failed or escalated"
	case health.StatusWarning:
		msg = "One or more audio sources are alarmed"
	default:
		msg = "All audio sources healthy"
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     status,
		Message:    msg,
		Details:    map[string]any{"sources": entries},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// PipelineLivenessCheck monitors how recently each audio source dispatched frames.
type PipelineLivenessCheck struct {
	getSnapshots func() []audiocore.SourceHealthSnapshot
}

// NewPipelineLivenessCheck creates a PipelineLivenessCheck using the given snapshot provider.
func NewPipelineLivenessCheck(getSnapshots func() []audiocore.SourceHealthSnapshot) *PipelineLivenessCheck {
	return &PipelineLivenessCheck{getSnapshots: getSnapshots}
}

// Name returns the check identifier.
func (c *PipelineLivenessCheck) Name() string { return "pipeline_liveness" }

// Category returns the audio category.
func (c *PipelineLivenessCheck) Category() health.Category { return health.CategoryAudio }

// Run evaluates whether audio sources are dispatching frames in a timely manner.
func (c *PipelineLivenessCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getSnapshots == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	snaps := c.getSnapshots()
	if len(snaps) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	const warnAge = 30 * time.Second
	const critAge = 2 * time.Minute

	type dispatchEntry struct {
		ID           string `json:"id"`
		LastDispatch string `json:"last_dispatch"`
	}

	now := time.Now()
	entries := make([]dispatchEntry, 0, len(snaps))
	status := health.StatusHealthy
	var msg string

	for _, s := range snaps {
		lastDispatch := "never"
		if !s.LastDispatch.IsZero() {
			lastDispatch = s.LastDispatch.Format(time.RFC3339)
		}
		entries = append(entries, dispatchEntry{ID: s.SourceID, LastDispatch: lastDispatch})

		if s.LastDispatch.IsZero() {
			continue
		}
		age := now.Sub(s.LastDispatch)
		switch {
		case age > critAge:
			status = health.StatusCritical
		case age > warnAge:
			if status != health.StatusCritical {
				status = health.StatusWarning
			}
		}
	}

	switch status {
	case health.StatusCritical:
		msg = fmt.Sprintf("One or more sources have not dispatched frames for over %s", critAge)
	case health.StatusWarning:
		msg = fmt.Sprintf("One or more sources have not dispatched frames for over %s", warnAge)
	default:
		msg = "All audio sources dispatching frames normally"
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     status,
		Message:    msg,
		Details:    map[string]any{"sources": entries},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// BufferDropsCheck monitors audio buffer drop statistics.
// Metrics are not yet collected; this check always returns StatusSkipped.
type BufferDropsCheck struct{}

// NewBufferDropsCheck creates a BufferDropsCheck.
func NewBufferDropsCheck() *BufferDropsCheck { return &BufferDropsCheck{} }

// Name returns the check identifier.
func (c *BufferDropsCheck) Name() string { return "buffer_drops" }

// Category returns the audio category.
func (c *BufferDropsCheck) Category() health.Category { return health.CategoryAudio }

// Run returns StatusSkipped because buffer drop metrics are not yet available.
func (c *BufferDropsCheck) Run(_ context.Context) health.Result {
	return skippedResult(c.Name(), c.Category(), time.Now())
}

// AudioLevelCheck monitors audio input levels for silence or clipping.
// Metrics are not yet collected; this check always returns StatusSkipped.
type AudioLevelCheck struct{}

// NewAudioLevelCheck creates an AudioLevelCheck.
func NewAudioLevelCheck() *AudioLevelCheck { return &AudioLevelCheck{} }

// Name returns the check identifier.
func (c *AudioLevelCheck) Name() string { return "audio_level" }

// Category returns the audio category.
func (c *AudioLevelCheck) Category() health.Category { return health.CategoryAudio }

// Run returns StatusSkipped because audio level metrics are not yet available.
func (c *AudioLevelCheck) Run(_ context.Context) health.Result {
	return skippedResult(c.Name(), c.Category(), time.Now())
}

// BufferOverrunCheck monitors the audio capture ring buffer for overrun events.
// Metrics are not yet collected; this check always returns StatusSkipped.
type BufferOverrunCheck struct{}

// NewBufferOverrunCheck creates a BufferOverrunCheck.
func NewBufferOverrunCheck() *BufferOverrunCheck { return &BufferOverrunCheck{} }

// Name returns the check identifier.
func (c *BufferOverrunCheck) Name() string { return "buffer_overrun" }

// Category returns the audio category.
func (c *BufferOverrunCheck) Category() health.Category { return health.CategoryAudio }

// Run returns StatusSkipped because buffer overrun metrics are not yet available.
func (c *BufferOverrunCheck) Run(_ context.Context) health.Result {
	return skippedResult(c.Name(), c.Category(), time.Now())
}

// CaptureBufferCheck monitors the health of the audio capture buffer.
// Metrics are not yet collected; this check always returns StatusSkipped.
type CaptureBufferCheck struct{}

// NewCaptureBufferCheck creates a CaptureBufferCheck.
func NewCaptureBufferCheck() *CaptureBufferCheck { return &CaptureBufferCheck{} }

// Name returns the check identifier.
func (c *CaptureBufferCheck) Name() string { return "capture_buffer" }

// Category returns the audio category.
func (c *CaptureBufferCheck) Category() health.Category { return health.CategoryAudio }

// Run returns StatusSkipped because capture buffer metrics are not yet available.
func (c *CaptureBufferCheck) Run(_ context.Context) health.Result {
	return skippedResult(c.Name(), c.Category(), time.Now())
}
