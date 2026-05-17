package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// StreamHealthInfo is a snapshot of a single RTSP stream's health.
type StreamHealthInfo struct {
	// URL is the RTSP source URL.
	URL string
	// IsHealthy indicates whether the stream is considered healthy.
	IsHealthy bool
	// ProcessState is the current state of the underlying FFmpeg process (e.g. "running", "dead").
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

// StreamErrorRateCheck monitors RTSP stream restart counts to detect error loops.
type StreamErrorRateCheck struct {
	getStreams func() []StreamHealthInfo
}

// NewStreamErrorRateCheck creates a StreamErrorRateCheck using the given stream provider.
func NewStreamErrorRateCheck(getStreams func() []StreamHealthInfo) *StreamErrorRateCheck {
	return &StreamErrorRateCheck{getStreams: getStreams}
}

// Name returns the check identifier.
func (c *StreamErrorRateCheck) Name() string { return "stream_error_rate" }

// Category returns the streams category.
func (c *StreamErrorRateCheck) Category() health.Category { return health.CategoryStreams }

// Run evaluates the restart count of each RTSP stream.
func (c *StreamErrorRateCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStreams == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	streams := c.getStreams()
	if len(streams) == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	const warnRestarts = 3
	const critRestarts = 10

	status := health.StatusHealthy
	var msg string
	warnCount := 0
	critCount := 0

	for _, s := range streams {
		switch {
		case s.RestartCount > critRestarts:
			critCount++
		case s.RestartCount > warnRestarts:
			warnCount++
		}
	}

	switch {
	case critCount > 0:
		status = health.StatusCritical
		msg = fmt.Sprintf("%d stream(s) have restarted more than %d times", critCount, critRestarts)
	case warnCount > 0:
		status = health.StatusWarning
		msg = fmt.Sprintf("%d stream(s) have restarted more than %d times", warnCount, warnRestarts)
	default:
		msg = "Stream restart counts within normal range"
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"streams_above_warn_threshold": warnCount,
			"streams_above_crit_threshold": critCount,
			"warn_threshold":               warnRestarts,
			"crit_threshold":               critRestarts,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

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

	deadCount := 0
	notRunningCount := 0

	for _, s := range streams {
		switch s.ProcessState {
		case "dead":
			deadCount++
		case "running":
			// healthy
		default:
			notRunningCount++
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("All %d FFmpeg processes running", len(streams))

	switch {
	case deadCount > 0:
		status = health.StatusCritical
		msg = fmt.Sprintf("%d FFmpeg process(es) are dead", deadCount)
	case notRunningCount > 0:
		status = health.StatusWarning
		msg = fmt.Sprintf("%d FFmpeg process(es) are not in running state", notRunningCount)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"total":       len(streams),
			"dead":        deadCount,
			"not_running": notRunningCount,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
