package checks

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestRecentErrorsCheck_TopErrorsPresent(t *testing.T) {
	t.Parallel()
	buf := health.NewErrorRingBuffer(100)
	now := time.Now()

	for i := range 15 {
		buf.Add(&health.LogEntry{
			Level:     "error",
			Message:   "connection timeout",
			Component: "database",
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	for range 3 {
		buf.Add(&health.LogEntry{
			Level:     "error",
			Message:   "auth failed",
			Component: "api",
			Timestamp: now.Add(-5 * time.Minute),
		})
	}

	check := NewRecentErrorsCheck(buf)
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusWarning, result.Status)
	require.Contains(t, result.Details, "top_errors")
	topErrors, ok := result.Details["top_errors"].([]errorGroup)
	require.True(t, ok, "top_errors should be []errorGroup")
	require.GreaterOrEqual(t, len(topErrors), 2)
	assert.Equal(t, "database", topErrors[0].Component)
	assert.Equal(t, "connection timeout", topErrors[0].Message)
	assert.Equal(t, 15, topErrors[0].Count)
}

func TestRecentErrorsCheck_NoTopErrorsWhenHealthy(t *testing.T) {
	t.Parallel()
	buf := health.NewErrorRingBuffer(100)

	for i := range 3 {
		buf.Add(&health.LogEntry{
			Level:     "error",
			Message:   "minor",
			Component: "test",
			Timestamp: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}

	check := NewRecentErrorsCheck(buf)
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.NotContains(t, result.Details, "top_errors")
}

func TestErrorTrendCheck_TopErrorsPresent(t *testing.T) {
	t.Parallel()
	buf := health.NewErrorRingBuffer(200)
	now := time.Now()

	for i := range 10 {
		buf.Add(&health.LogEntry{
			Level:     "error",
			Message:   "disk full",
			Component: "storage",
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	check := NewErrorTrendCheck(buf)
	result := check.Run(t.Context())

	assert.Equal(t, health.StatusWarning, result.Status)
	require.Contains(t, result.Details, "top_errors")
	topErrors, ok := result.Details["top_errors"].([]errorGroup)
	require.True(t, ok)
	assert.Equal(t, "disk full", topErrors[0].Message)
}

func TestTopErrorGrouping_Cap(t *testing.T) {
	t.Parallel()
	buf := health.NewErrorRingBuffer(500)
	now := time.Now()

	for i := range 15 {
		for range 5 {
			buf.Add(&health.LogEntry{
				Level:     "error",
				Message:   fmt.Sprintf("error-type-%d", i),
				Component: "test",
				Timestamp: now.Add(-5 * time.Minute),
			})
		}
	}

	check := NewRecentErrorsCheck(buf)
	result := check.Run(t.Context())

	topErrors, ok := result.Details["top_errors"].([]errorGroup)
	require.True(t, ok)
	assert.LessOrEqual(t, len(topErrors), 10)
}
