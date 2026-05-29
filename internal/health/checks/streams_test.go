package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/health"
)

// streamProvider returns a getStreams function yielding the given streams.
func streamProvider(streams []StreamHealthInfo) func() []StreamHealthInfo {
	return func() []StreamHealthInfo { return streams }
}

func TestFFmpegHealthCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewFFmpegHealthCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "ffmpeg_health", result.Name)
	assert.Equal(t, health.CategoryStreams, result.Category)
}

func TestFFmpegHealthCheck_NoStreams(t *testing.T) {
	t.Parallel()
	check := NewFFmpegHealthCheck(streamProvider(nil))
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestFFmpegHealthCheck_AllRunning(t *testing.T) {
	t.Parallel()
	streams := []StreamHealthInfo{
		{URL: "rtsp://a", ProcessState: "running"},
		{URL: "rtsp://b", ProcessState: "running"},
	}
	check := NewFFmpegHealthCheck(streamProvider(streams))
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "All 2 FFmpeg processes running")
	assert.Equal(t, 0, result.Details["stopped"])
	assert.Equal(t, 0, result.Details["not_running"])
	assert.Equal(t, 2, result.Details["total"])
}

// TestFFmpegHealthCheck_StoppedOnly covers a permanently stopped (terminal)
// process, the most severe state. "stopped" is the string ProcessState.String()
// actually returns for StateStopped.
func TestFFmpegHealthCheck_StoppedOnly(t *testing.T) {
	t.Parallel()
	streams := []StreamHealthInfo{
		{URL: "rtsp://a", ProcessState: "stopped"},
		{URL: "rtsp://b", ProcessState: "running"},
	}
	check := NewFFmpegHealthCheck(streamProvider(streams))
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusCritical, result.Status)
	assert.Contains(t, result.Message, "1 FFmpeg process(es) stopped")
	// Single-condition stopped message must not mention not-running.
	assert.NotContains(t, result.Message, "not in running state")
	assert.Equal(t, 1, result.Details["stopped"])
	assert.Equal(t, 0, result.Details["not_running"])
}

// TestFFmpegHealthCheck_NotRunningOnly covers a transient non-running state
// (starting), which is a warning rather than critical.
func TestFFmpegHealthCheck_NotRunningOnly(t *testing.T) {
	t.Parallel()
	streams := []StreamHealthInfo{
		{URL: "rtsp://a", ProcessState: "starting"},
		{URL: "rtsp://b", ProcessState: "running"},
	}
	check := NewFFmpegHealthCheck(streamProvider(streams))
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "1 FFmpeg process(es) are not in running state")
	assert.Equal(t, 0, result.Details["stopped"])
	assert.Equal(t, 1, result.Details["not_running"])
}

// TestFFmpegHealthCheck_StoppedAndNotRunning verifies the combined message when
// both stopped (terminal) and transient not-running processes exist. Status must
// stay Critical and the message must mention BOTH counts.
func TestFFmpegHealthCheck_StoppedAndNotRunning(t *testing.T) {
	t.Parallel()
	streams := []StreamHealthInfo{
		{URL: "rtsp://a", ProcessState: "stopped"},
		{URL: "rtsp://b", ProcessState: "stopped"},
		{URL: "rtsp://c", ProcessState: "starting"},
		{URL: "rtsp://d", ProcessState: "running"},
	}
	check := NewFFmpegHealthCheck(streamProvider(streams))
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusCritical, result.Status, "stopped processes must keep status critical")
	assert.Contains(t, result.Message, "2 FFmpeg process(es) stopped", "message must mention stopped count")
	assert.Contains(t, result.Message, "1 not in running state", "message must also mention not-running count")
	assert.Equal(t, 2, result.Details["stopped"])
	assert.Equal(t, 1, result.Details["not_running"])
	assert.Equal(t, 4, result.Details["total"])
}
