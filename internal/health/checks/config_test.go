package checks

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestToolAvailabilityCheck_NilClosure(t *testing.T) {
	t.Parallel()
	check := NewToolAvailabilityCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestToolAvailabilityCheck_EmptyTools(t *testing.T) {
	t.Parallel()
	check := NewToolAvailabilityCheck(func() []ToolInfo { return nil })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestToolAvailabilityCheck_AllAvailable(t *testing.T) {
	t.Parallel()
	check := NewToolAvailabilityCheck(func() []ToolInfo {
		return []ToolInfo{
			{Name: "FFmpeg", Path: "/usr/bin/ffmpeg", Version: "7.1"},
			{Name: "Sox", Path: "/usr/bin/sox"},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "2 tools available")
	assert.Equal(t, "tool_availability", result.Name)
	assert.Equal(t, health.CategoryConfig, result.Category)
}

func TestToolAvailabilityCheck_OneMissing(t *testing.T) {
	t.Parallel()
	check := NewToolAvailabilityCheck(func() []ToolInfo {
		return []ToolInfo{
			{Name: "FFmpeg", Path: "/usr/bin/ffmpeg", Version: "7.1"},
			{Name: "Sox", Path: ""},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "Sox")
}

func TestToolAvailabilityCheck_AllMissing(t *testing.T) {
	t.Parallel()
	check := NewToolAvailabilityCheck(func() []ToolInfo {
		return []ToolInfo{
			{Name: "FFmpeg", Path: ""},
			{Name: "Sox", Path: ""},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "FFmpeg")
	assert.Contains(t, result.Message, "Sox")
}

func TestToolAvailabilityCheck_DetailsContent(t *testing.T) {
	t.Parallel()
	check := NewToolAvailabilityCheck(func() []ToolInfo {
		return []ToolInfo{
			{Name: "FFmpeg", Path: "/usr/bin/ffmpeg", Version: "7.1"},
			{Name: "Sox", Path: ""},
		}
	})
	result := check.Run(t.Context())
	require.Contains(t, result.Details, "tools")

	// Round-trip through JSON to inspect the anonymous struct slice.
	raw, err := json.Marshal(result.Details["tools"])
	require.NoError(t, err)

	var tools []map[string]string
	require.NoError(t, json.Unmarshal(raw, &tools))
	require.Len(t, tools, 2)

	assert.Equal(t, "FFmpeg", tools[0]["name"])
	assert.Equal(t, "/usr/bin/ffmpeg", tools[0]["path"])
	assert.Equal(t, "7.1", tools[0]["version"])
	assert.Equal(t, "available", tools[0]["status"])

	assert.Equal(t, "Sox", tools[1]["name"])
	assert.Empty(t, tools[1]["path"])
	assert.Equal(t, "missing", tools[1]["status"])
}
