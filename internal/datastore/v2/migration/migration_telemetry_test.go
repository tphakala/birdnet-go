package migration

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMigrationTelemetry_NilSafety(t *testing.T) {
	t.Parallel()

	var mt *MigrationTelemetry

	// All methods must be safe to call on nil receiver
	assert.NotPanics(t, func() { mt.ReportStarted(1000) })
	assert.NotPanics(t, func() { mt.ReportCompleted(1000, 5*time.Minute, 3.5, 0) })
	assert.NotPanics(t, func() { mt.ReportValidationFailed(1000, 990, 0, "count mismatch") })
	assert.NotPanics(t, func() { mt.ReportAutoPaused(10, fmt.Errorf("disk full"), 500, 1000) })
	assert.NotPanics(t, func() { mt.ReportCancelled(500, 1000) })
	assert.NotPanics(t, func() { mt.ReportPanic("nil pointer dereference") })
}

func TestNewMigrationTelemetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dbType string
	}{
		{name: "sqlite", dbType: "sqlite"},
		{name: "mysql", dbType: "mysql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mt := NewMigrationTelemetry(tt.dbType)
			assert.NotNil(t, mt)
			assert.Equal(t, tt.dbType, mt.dbType)
		})
	}
}

func TestFormatDurationHuman(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{name: "seconds only", duration: 45 * time.Second, expected: "45s"},
		{name: "minutes and seconds", duration: 5*time.Minute + 30*time.Second, expected: "5m 30s"},
		{name: "hours minutes seconds", duration: 2*time.Hour + 15*time.Minute + 10*time.Second, expected: "2h 15m 10s"},
		{name: "zero", duration: 0, expected: "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatDurationHuman(tt.duration))
		})
	}
}
