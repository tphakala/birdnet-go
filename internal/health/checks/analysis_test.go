package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestORTAvailabilityCheck_Available(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(func() (bool, bool, string, string, string) {
		return true, true, "1.25.1", "/usr/lib/libonnxruntime.so", ""
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "1.25.1")
}

func TestORTAvailabilityCheck_Unavailable(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(func() (bool, bool, string, string, string) {
		return false, false, "", "", "ONNX Runtime library not found"
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "not found")
}

func TestORTAvailabilityCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestORTAvailabilityCheck_FoundNotInitialized(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(func() (bool, bool, string, string, string) {
		return true, false, "1.25.1", "/usr/lib/libonnxruntime.so", ""
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "not yet initialized")
}
