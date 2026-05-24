package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestDatabaseIntegrityCheck_Healthy(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "ok", false
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Equal(t, "database_integrity", result.Name)
	assert.Equal(t, health.CategoryDatabase, result.Category)
	assert.Contains(t, result.Message, "passed")
}

func TestDatabaseIntegrityCheck_Corrupted(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "database disk image is malformed", true
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
	assert.Contains(t, result.Message, "corruption")
	recoveryHint, ok := result.Details["recovery_hint"].(string)
	assert.True(t, ok, "recovery_hint should be a string")
	assert.Contains(t, recoveryHint, "Support")
}

func TestDatabaseIntegrityCheck_NotYetRun(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "", false
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusUnknown, result.Status)
	assert.Contains(t, result.Message, "not run yet")
}

func TestDatabaseIntegrityCheck_NilProvider(t *testing.T) {
	check := NewDatabaseIntegrityCheck(nil)

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestDatabaseIntegrityCheck_ResultNotOkButNotLatched(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "*** in database main ***\nPage 42: btreeInitPage() error", false
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status,
		"non-ok integrity result should be critical even without latch")
}
