package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
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

// DefaultWindow is the default evaluation window for windowed checks.
const DefaultWindow = time.Hour

// Threshold constants for audio health checks.
const (
	dropsBaseWarnThreshold   = 10
	dropsBaseCritThreshold   = 50
	overrunBaseWarnThreshold = 5
	overrunBaseCritThreshold = 25
)

// BufferDropsCheck monitors audio buffer drop statistics using time-windowed evaluation.
type BufferDropsCheck struct {
	store     *observability.HealthMetricsStore
	getEvents func(metric string, n int) []observability.HealthEvent
	window    time.Duration
}

// NewBufferDropsCheck creates a BufferDropsCheck using the health metrics store and event getter.
func NewBufferDropsCheck(store *observability.HealthMetricsStore, getEvents func(metric string, n int) []observability.HealthEvent) *BufferDropsCheck {
	return &BufferDropsCheck{
		store:     store,
		getEvents: getEvents,
		window:    DefaultWindow,
	}
}

// Name returns the check identifier.
func (c *BufferDropsCheck) Name() string { return "buffer_drops" }

// Category returns the audio category.
func (c *BufferDropsCheck) Category() health.Category { return health.CategoryAudio }

// WithWindow returns a copy of this check configured with the given evaluation window.
// Returns the receiver unchanged when d equals the current window to avoid an allocation.
func (c *BufferDropsCheck) WithWindow(d time.Duration) health.Check {
	if d == c.window {
		return c
	}
	cp := *c
	cp.window = d
	return &cp
}

// Run evaluates audio buffer drop statistics within the configured time window.
func (c *BufferDropsCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	return evalWindowedStats(c.Name(), c.Category(), c.store, c.getEvents, &windowedStatsConfig{
		baseWarnThreshold: dropsBaseWarnThreshold,
		baseCritThreshold: dropsBaseCritThreshold,
		sustainedHours:    defaultSustainedHours,
		metricPrefix:      observability.MetricPrefixAudioDrops,
		window:            c.window,
	}, start)
}

// AudioLevelInfo holds audio level data for a single source.
type AudioLevelInfo struct {
	Source   string `json:"source"`
	Level    int    `json:"level"`
	Clipping bool   `json:"clipping"`
}

// AudioLevelCheck monitors audio input levels for silence or clipping.
type AudioLevelCheck struct {
	getAudioLevels func() []AudioLevelInfo
}

// NewAudioLevelCheck creates an AudioLevelCheck using the given level provider.
func NewAudioLevelCheck(getAudioLevels func() []AudioLevelInfo) *AudioLevelCheck {
	return &AudioLevelCheck{getAudioLevels: getAudioLevels}
}

// Name returns the check identifier.
func (c *AudioLevelCheck) Name() string { return "audio_level" }

// Category returns the audio category.
func (c *AudioLevelCheck) Category() health.Category { return health.CategoryAudio }

// Run evaluates audio input levels for silence or clipping.
func (c *AudioLevelCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getAudioLevels == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	levels := c.getAudioLevels()
	if len(levels) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	silentCount := 0
	clippingCount := 0
	for _, l := range levels {
		if l.Level == 0 {
			silentCount++
		}
		if l.Clipping {
			clippingCount++
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("Audio levels normal across %d source(s)", len(levels))

	switch {
	case silentCount == len(levels):
		status = health.StatusWarning
		msg = fmt.Sprintf("All %d source(s) reporting silence", len(levels))
	case clippingCount > 0:
		status = health.StatusWarning
		msg = fmt.Sprintf("Clipping detected on %d of %d source(s)", clippingCount, len(levels))
	case silentCount > 0:
		status = health.StatusWarning
		msg = fmt.Sprintf("Silence detected on %d of %d source(s)", silentCount, len(levels))
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"sources":  len(levels),
			"silent":   silentCount,
			"clipping": clippingCount,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// BufferOverrunCheck monitors audio processing overrun events using time-windowed evaluation.
type BufferOverrunCheck struct {
	store     *observability.HealthMetricsStore
	getEvents func(metric string, n int) []observability.HealthEvent
	window    time.Duration
}

// NewBufferOverrunCheck creates a BufferOverrunCheck using the health metrics store and event getter.
func NewBufferOverrunCheck(store *observability.HealthMetricsStore, getEvents func(metric string, n int) []observability.HealthEvent) *BufferOverrunCheck {
	return &BufferOverrunCheck{
		store:     store,
		getEvents: getEvents,
		window:    DefaultWindow,
	}
}

// Name returns the check identifier.
func (c *BufferOverrunCheck) Name() string { return "buffer_overrun" }

// Category returns the audio category.
func (c *BufferOverrunCheck) Category() health.Category { return health.CategoryAudio }

// WithWindow returns a copy of this check configured with the given evaluation window.
// Returns the receiver unchanged when d equals the current window to avoid an allocation.
func (c *BufferOverrunCheck) WithWindow(d time.Duration) health.Check {
	if d == c.window {
		return c
	}
	cp := *c
	cp.window = d
	return &cp
}

// Run evaluates audio processing overrun statistics within the configured time window.
func (c *BufferOverrunCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	return evalWindowedStats(c.Name(), c.Category(), c.store, c.getEvents, &windowedStatsConfig{
		baseWarnThreshold: overrunBaseWarnThreshold,
		baseCritThreshold: overrunBaseCritThreshold,
		sustainedHours:    defaultSustainedHours,
		metricPrefix:      observability.MetricPrefixAudioOverruns,
		window:            c.window,
	}, start)
}

// CaptureBufferInfo holds status data for a single capture buffer.
type CaptureBufferInfo struct {
	SourceID    string `json:"source_id"`
	Capacity    int    `json:"capacity"`
	Initialized bool   `json:"initialized"`
}

// CaptureBufferCheck monitors the health of the audio capture buffers.
// Capture buffers are circular (ring) buffers that are always "full" after
// warmup, so fill ratio is not a meaningful health metric. Instead, this
// check verifies that buffers exist and are allocated for all active sources.
type CaptureBufferCheck struct {
	getBufferHealth func() []CaptureBufferInfo
}

// NewCaptureBufferCheck creates a CaptureBufferCheck using the given health provider.
func NewCaptureBufferCheck(getBufferHealth func() []CaptureBufferInfo) *CaptureBufferCheck {
	return &CaptureBufferCheck{getBufferHealth: getBufferHealth}
}

// Name returns the check identifier.
func (c *CaptureBufferCheck) Name() string { return "capture_buffer" }

// Category returns the audio category.
func (c *CaptureBufferCheck) Category() health.Category { return health.CategoryAudio }

// Run verifies that capture buffers are allocated for all active sources.
func (c *CaptureBufferCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getBufferHealth == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	buffers := c.getBufferHealth()
	if len(buffers) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	uninitCount := 0
	var totalCapacity int
	for _, b := range buffers {
		totalCapacity += b.Capacity
		if !b.Initialized {
			uninitCount++
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("%d capture buffer(s) allocated (%d KB total)", len(buffers), totalCapacity/1024)

	if uninitCount > 0 {
		status = health.StatusWarning
		msg = fmt.Sprintf("%d of %d capture buffer(s) not yet initialized", uninitCount, len(buffers))
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"buffers":        len(buffers),
			"total_capacity": totalCapacity,
			"uninitialized":  uninitCount,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
