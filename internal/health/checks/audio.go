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

// DropStats maps source ID to cumulative frame drop count.
type DropStats map[string]int64

// BufferDropsCheck monitors audio buffer drop statistics.
type BufferDropsCheck struct {
	getDropStats func() DropStats
}

// NewBufferDropsCheck creates a BufferDropsCheck using the given drop stats provider.
func NewBufferDropsCheck(getDropStats func() DropStats) *BufferDropsCheck {
	return &BufferDropsCheck{getDropStats: getDropStats}
}

// Name returns the check identifier.
func (c *BufferDropsCheck) Name() string { return "buffer_drops" }

// Category returns the audio category.
func (c *BufferDropsCheck) Category() health.Category { return health.CategoryAudio }

// Run evaluates audio buffer drop statistics across all sources.
func (c *BufferDropsCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getDropStats == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	stats := c.getDropStats()
	if stats == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	return evalCounterStats(c.Name(), c.Category(), stats, &counterStatsConfig{
		warnThreshold: 1,
		critThreshold: 101,
		totalKey:      "total_drops",
		healthyMsg:    "No buffer drops across %d source(s)",
		warnMsg:       "Buffer drops detected: %d total across %d source(s)",
		critMsg:       "High buffer drop rate: %d total drops across %d source(s)",
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

// OverrunStats maps source ID to cumulative write error count.
type OverrunStats map[string]int64

// BufferOverrunCheck monitors audio processing overrun events.
type BufferOverrunCheck struct {
	getOverrunStats func() OverrunStats
}

// NewBufferOverrunCheck creates a BufferOverrunCheck using the given overrun stats provider.
func NewBufferOverrunCheck(getOverrunStats func() OverrunStats) *BufferOverrunCheck {
	return &BufferOverrunCheck{getOverrunStats: getOverrunStats}
}

// Name returns the check identifier.
func (c *BufferOverrunCheck) Name() string { return "buffer_overrun" }

// Category returns the audio category.
func (c *BufferOverrunCheck) Category() health.Category { return health.CategoryAudio }

// Run evaluates audio processing overrun statistics across all sources.
func (c *BufferOverrunCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getOverrunStats == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	stats := c.getOverrunStats()
	if stats == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	return evalCounterStats(c.Name(), c.Category(), stats, &counterStatsConfig{
		warnThreshold: 1,
		critThreshold: 51,
		totalKey:      "total_errors",
		healthyMsg:    "No buffer overruns across %d source(s)",
		warnMsg:       "Buffer overruns detected: %d errors across %d source(s)",
		critMsg:       "High overrun rate: %d errors across %d source(s)",
	}, start)
}

// CaptureBufferInfo holds utilization data for a single capture buffer.
type CaptureBufferInfo struct {
	SourceID  string  `json:"source_id"`
	Capacity  int     `json:"capacity"`
	Used      int     `json:"used"`
	FillRatio float64 `json:"fill_ratio"`
}

// CaptureBufferCheck monitors the health of the audio capture buffers.
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

// Run evaluates capture buffer utilization across all sources.
func (c *CaptureBufferCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getBufferHealth == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	buffers := c.getBufferHealth()
	if len(buffers) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	const warnRatio = 0.80
	const critRatio = 0.95

	warnCount := 0
	critCount := 0
	var maxRatio float64

	for _, b := range buffers {
		if b.FillRatio > maxRatio {
			maxRatio = b.FillRatio
		}
		switch {
		case b.FillRatio >= critRatio:
			critCount++
		case b.FillRatio >= warnRatio:
			warnCount++
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("Capture buffers healthy across %d source(s) (max fill %.0f%%)", len(buffers), maxRatio*100)

	switch {
	case critCount > 0:
		status = health.StatusCritical
		msg = fmt.Sprintf("%d capture buffer(s) near capacity (>%.0f%% full)", critCount, critRatio*100)
	case warnCount > 0:
		status = health.StatusWarning
		msg = fmt.Sprintf("%d capture buffer(s) above %.0f%% utilization", warnCount, warnRatio*100)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"buffers":    len(buffers),
			"max_fill":   maxRatio,
			"warn_count": warnCount,
			"crit_count": critCount,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
