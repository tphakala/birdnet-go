package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// StreamHealthInfo is a snapshot of a single RTSP stream's health.
type StreamHealthInfo struct {
	// URL is the RTSP source URL.
	URL string
	// IsHealthy indicates whether the stream is considered healthy.
	IsHealthy bool
	// ProcessState is the current state of the underlying FFmpeg process (e.g. "running", "stopped").
	ProcessState string
	// RestartCount is the number of times this stream has been restarted.
	RestartCount int
	// Error holds the most recent error message, if any.
	Error string
}

// StreamConnectivityCheck verifies that all configured RTSP streams are reachable and healthy.
type StreamConnectivityCheck struct {
	getStreams func() []StreamHealthInfo
}

// NewStreamConnectivityCheck creates a StreamConnectivityCheck using the given stream provider.
func NewStreamConnectivityCheck(getStreams func() []StreamHealthInfo) *StreamConnectivityCheck {
	return &StreamConnectivityCheck{getStreams: getStreams}
}

// Name returns the check identifier.
func (c *StreamConnectivityCheck) Name() string { return "stream_connectivity" }

// Category returns the streams category.
func (c *StreamConnectivityCheck) Category() health.Category { return health.CategoryStreams }

// Run evaluates the connectivity health of all RTSP streams.
func (c *StreamConnectivityCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStreams == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	streams := c.getStreams()
	if len(streams) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	unhealthy := 0
	for _, s := range streams {
		if !s.IsHealthy {
			unhealthy++
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("All %d streams connected", len(streams))

	switch {
	case unhealthy > 1:
		status = health.StatusCritical
		msg = fmt.Sprintf("%d of %d streams are not healthy", unhealthy, len(streams))
	case unhealthy == 1:
		status = health.StatusWarning
		msg = fmt.Sprintf("1 of %d streams is not healthy", len(streams))
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"total":     len(streams),
			"unhealthy": unhealthy,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// Threshold constants for stream health checks.
const (
	streamBaseWarnThreshold = 3
	streamBaseCritThreshold = 10
)

// StreamErrorRateCheck monitors RTSP stream restart counts using time-windowed evaluation.
type StreamErrorRateCheck struct {
	store     *observability.HealthMetricsStore
	getEvents func(metric string, n int) []observability.HealthEvent
	window    time.Duration
}

// NewStreamErrorRateCheck creates a StreamErrorRateCheck using the health metrics store and event getter.
func NewStreamErrorRateCheck(store *observability.HealthMetricsStore, getEvents func(metric string, n int) []observability.HealthEvent) *StreamErrorRateCheck {
	return &StreamErrorRateCheck{
		store:     store,
		getEvents: getEvents,
		window:    DefaultWindow,
	}
}

// Name returns the check identifier.
func (c *StreamErrorRateCheck) Name() string { return "stream_error_rate" }

// Category returns the streams category.
func (c *StreamErrorRateCheck) Category() health.Category { return health.CategoryStreams }

// WithWindow returns a copy of this check configured with the given evaluation window.
// Returns the receiver unchanged when d equals the current window to avoid an allocation.
func (c *StreamErrorRateCheck) WithWindow(d time.Duration) health.Check {
	if d == c.window {
		return c
	}
	cp := *c
	cp.window = d
	return &cp
}

// Run evaluates stream restart counts within the configured time window.
func (c *StreamErrorRateCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	return evalWindowedStats(c.Name(), c.Category(), c.store, c.getEvents, &windowedStatsConfig{
		baseWarnThreshold: streamBaseWarnThreshold,
		baseCritThreshold: streamBaseCritThreshold,
		sustainedHours:    defaultSustainedHours,
		metricPrefix:      observability.MetricPrefixStreamRestarts,
		window:            c.window,
	}, start)
}

// FFmpeg health check message formats.
const (
	// ffmpegStoppedMsgFormat is used when only stopped (terminal) processes are present.
	ffmpegStoppedMsgFormat = "%d FFmpeg process(es) stopped"
	// ffmpegNotRunningMsgFormat is used when only transient not-running processes are present.
	ffmpegNotRunningMsgFormat = "%d FFmpeg process(es) are not in running state"
	// ffmpegStoppedAndNotRunningMsgFormat is used when both stopped and transient
	// not-running processes are present so neither count is masked.
	ffmpegStoppedAndNotRunningMsgFormat = "%d FFmpeg process(es) stopped, %d not in running state"
)

// FFmpegHealthCheck monitors the process state of the FFmpeg processes backing each RTSP stream.
type FFmpegHealthCheck struct {
	getStreams func() []StreamHealthInfo
}

// NewFFmpegHealthCheck creates an FFmpegHealthCheck using the given stream provider.
func NewFFmpegHealthCheck(getStreams func() []StreamHealthInfo) *FFmpegHealthCheck {
	return &FFmpegHealthCheck{getStreams: getStreams}
}

// Name returns the check identifier.
func (c *FFmpegHealthCheck) Name() string { return "ffmpeg_health" }

// Category returns the streams category.
func (c *FFmpegHealthCheck) Category() health.Category { return health.CategoryStreams }

// Run evaluates the process state of each stream's FFmpeg instance.
func (c *FFmpegHealthCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStreams == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	streams := c.getStreams()
	if len(streams) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	stoppedCount := 0
	notRunningCount := 0

	for _, s := range streams {
		switch s.ProcessState {
		case ffmpeg.ProcessStateRunning:
			// healthy
		case ffmpeg.ProcessStateStopped:
			stoppedCount++
		default:
			notRunningCount++
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("All %d FFmpeg processes running", len(streams))

	switch {
	case stoppedCount > 0 && notRunningCount > 0:
		// Both stopped (terminal) and transient not-running processes exist.
		// Stopped processes keep the status critical, but the message must
		// surface both counts so the not-running processes are not masked.
		status = health.StatusCritical
		msg = fmt.Sprintf(ffmpegStoppedAndNotRunningMsgFormat, stoppedCount, notRunningCount)
	case stoppedCount > 0:
		status = health.StatusCritical
		msg = fmt.Sprintf(ffmpegStoppedMsgFormat, stoppedCount)
	case notRunningCount > 0:
		status = health.StatusWarning
		msg = fmt.Sprintf(ffmpegNotRunningMsgFormat, notRunningCount)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"total":       len(streams),
			"stopped":     stoppedCount,
			"not_running": notRunningCount,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
