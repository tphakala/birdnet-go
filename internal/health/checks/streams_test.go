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
	assert.Equal(t, 0, result.Details["dead"])
	assert.Equal(t, 0, result.Details["not_running"])
	assert.Equal(t, 2, result.Details["total"])
}

func TestFFmpegHealthCheck_DeadOnly(t *testing.T) {
	t.Parallel()
	streams := []StreamHealthInfo{
		{URL: "rtsp://a", ProcessState: "dead"},
		{URL: "rtsp://b", ProcessState: "running"},
	}
	check := NewFFmpegHealthCheck(streamProvider(streams))
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusCritical, result.Status)
	assert.Contains(t, result.Message, "1 FFmpeg process(es) are dead")
	// Single-condition dead message must not mention not-running.
	assert.NotContains(t, result.Message, "not in running state")
	assert.Equal(t, 1, result.Details["dead"])
	assert.Equal(t, 0, result.Details["not_running"])
}

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
	assert.Equal(t, 0, result.Details["dead"])
	assert.Equal(t, 1, result.Details["not_running"])
}

// TestFFmpegHealthCheck_DeadAndNotRunning verifies the combined message when
// both dead and not-running processes exist. Status must stay Critical and the
// message must mention BOTH counts.
func TestFFmpegHealthCheck_DeadAndNotRunning(t *testing.T) {
	t.Parallel()
	streams := []StreamHealthInfo{
		{URL: "rtsp://a", ProcessState: "dead"},
		{URL: "rtsp://b", ProcessState: "dead"},
		{URL: "rtsp://c", ProcessState: "starting"},
		{URL: "rtsp://d", ProcessState: "running"},
	}
	check := NewFFmpegHealthCheck(streamProvider(streams))
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusCritical, result.Status, "dead processes must keep status critical")
	assert.Contains(t, result.Message, "2 FFmpeg process(es) are dead", "message must mention dead count")
	assert.Contains(t, result.Message, "1 not in running state", "message must also mention not-running count")
	assert.Equal(t, 2, result.Details["dead"])
	assert.Equal(t, 1, result.Details["not_running"])
	assert.Equal(t, 4, result.Details["total"])
}
