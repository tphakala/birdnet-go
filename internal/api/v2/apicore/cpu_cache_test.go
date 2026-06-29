package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCachedCPUUsage tests the GetCachedCPUUsage function
func TestGetCachedCPUUsage(t *testing.T) {
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "cpu-cache")

	// The cache is initialized with [0], so we should get at least that
	result := GetCachedCPUUsage()
	require.NotNil(t, result, "CPU cache should not be nil")
	require.Len(t, result, 1, "CPU cache should have at least 1 value")

	// Verify it returns a copy (modifying result shouldn't affect cache)
	originalValue := result[0]
	result[0] = 999.0

	newResult := GetCachedCPUUsage()
	assert.InDelta(t, originalValue, newResult[0], 0.001, "Cache should return a copy, not the original slice")
}
