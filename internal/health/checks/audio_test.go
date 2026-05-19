package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestBufferDropsCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewBufferDropsCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "buffer_drops", result.Name)
}

func TestBufferDropsCheck_NilStats(t *testing.T) {
	t.Parallel()
	check := NewBufferDropsCheck(func() DropStats { return nil })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestBufferDropsCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewBufferDropsCheck(func() DropStats {
		return DropStats{"src1": 0, "src2": 0}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "No buffer drops")
}

func TestBufferDropsCheck_Warning(t *testing.T) {
	t.Parallel()
	check := NewBufferDropsCheck(func() DropStats {
		return DropStats{"src1": 5, "src2": 0}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "5 total")
}

func TestBufferDropsCheck_Critical(t *testing.T) {
	t.Parallel()
	check := NewBufferDropsCheck(func() DropStats {
		return DropStats{"src1": 101}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestAudioLevelCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "audio_level", result.Name)
}

func TestAudioLevelCheck_Empty(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo { return nil })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestAudioLevelCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 42, Clipping: false},
			{Source: "src2", Level: 15, Clipping: false},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "normal")
}

func TestAudioLevelCheck_Silence(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 0},
			{Source: "src2", Level: 0},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "silence")
}

func TestAudioLevelCheck_Clipping(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 99, Clipping: true},
			{Source: "src2", Level: 50, Clipping: false},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "Clipping")
}

func TestAudioLevelCheck_PartialSilence(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 0},
			{Source: "src2", Level: 50},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "Silence detected on 1")
}

func TestBufferOverrunCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewBufferOverrunCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "buffer_overrun", result.Name)
}

func TestBufferOverrunCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewBufferOverrunCheck(func() OverrunStats {
		return OverrunStats{"src1": 0}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
}

func TestBufferOverrunCheck_Warning(t *testing.T) {
	t.Parallel()
	check := NewBufferOverrunCheck(func() OverrunStats {
		return OverrunStats{"src1": 10}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
}

func TestBufferOverrunCheck_Critical(t *testing.T) {
	t.Parallel()
	check := NewBufferOverrunCheck(func() OverrunStats {
		return OverrunStats{"src1": 51}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestCaptureBufferCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "capture_buffer", result.Name)
}

func TestCaptureBufferCheck_Empty(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo { return nil })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestCaptureBufferCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 1000, Used: 500, FillRatio: 0.50},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "healthy")
}

func TestCaptureBufferCheck_Warning(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 1000, Used: 850, FillRatio: 0.85},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "80%")
}

func TestCaptureBufferCheck_Critical(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 1000, Used: 960, FillRatio: 0.96},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestCaptureBufferCheck_Details(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 1000, Used: 300, FillRatio: 0.30},
			{SourceID: "src2", Capacity: 2000, Used: 1800, FillRatio: 0.90},
		}
	})
	result := check.Run(t.Context())
	require.NotNil(t, result.Details)
	assert.Equal(t, 2, result.Details["buffers"])
	assert.Equal(t, 1, result.Details["warn_count"])
	assert.Equal(t, 0, result.Details["crit_count"])
}
