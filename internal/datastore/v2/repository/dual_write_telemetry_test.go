package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReportDualWriteReconciliation_NilSafe(t *testing.T) {
	t.Parallel()
	dw := &DualWriteRepository{}
	assert.NotPanics(t, func() {
		dw.reportDualWriteReconciliation(50, 10, 40)
	})
}

func TestReportDualWriteReconciliation_BelowThreshold(t *testing.T) {
	t.Parallel()
	dw := &DualWriteRepository{}
	// Below threshold should not panic and should return early
	assert.NotPanics(t, func() {
		dw.reportDualWriteReconciliation(5, 2, 3)
	})
}

func TestReportDualWriteReconciliation_RateLimited(t *testing.T) {
	t.Parallel()
	dw := &DualWriteRepository{
		lastDirtyIDTelemetry: time.Now(), // Recent telemetry
	}
	// Should not panic even when rate-limited
	assert.NotPanics(t, func() {
		dw.reportDualWriteReconciliation(50, 10, 40)
	})
}
