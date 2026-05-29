// internal/health/check_test.go
package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorstStatus_Empty(t *testing.T) {
	t.Parallel()
	// An empty slice means there is no data to aggregate. No data is not
	// "healthy"; it is unknown.
	assert.Equal(t, StatusUnknown, WorstStatus([]Result{}))
	assert.Equal(t, StatusUnknown, WorstStatus(nil))
}

func TestWorstStatus_AllHealthy(t *testing.T) {
	t.Parallel()
	results := []Result{
		{Status: StatusHealthy},
		{Status: StatusHealthy},
		{Status: StatusHealthy},
	}
	got := WorstStatus(results)
	assert.Equal(t, StatusHealthy, got)
}

func TestWorstStatus_MixedStatuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		statuses []Status
		want     Status
	}{
		{
			name:     "warning beats healthy",
			statuses: []Status{StatusHealthy, StatusWarning, StatusHealthy},
			want:     StatusWarning,
		},
		{
			name:     "critical beats warning",
			statuses: []Status{StatusHealthy, StatusWarning, StatusCritical},
			want:     StatusCritical,
		},
		{
			name:     "healthy with skipped stays healthy",
			statuses: []Status{StatusHealthy, StatusSkipped},
			want:     StatusHealthy,
		},
		{
			name:     "healthy with unknown stays healthy",
			statuses: []Status{StatusHealthy, StatusUnknown},
			want:     StatusHealthy,
		},
		{
			name:     "all skipped returns skipped",
			statuses: []Status{StatusSkipped, StatusSkipped},
			want:     StatusSkipped,
		},
		{
			name:     "all unknown returns unknown",
			statuses: []Status{StatusUnknown, StatusUnknown},
			want:     StatusUnknown,
		},
		{
			name:     "mixed skipped and unknown returns unknown",
			statuses: []Status{StatusSkipped, StatusUnknown},
			want:     StatusUnknown,
		},
		{
			name:     "single skipped returns skipped",
			statuses: []Status{StatusSkipped},
			want:     StatusSkipped,
		},
		{
			name:     "warning with skipped stays warning",
			statuses: []Status{StatusWarning, StatusSkipped},
			want:     StatusWarning,
		},
		{
			name:     "critical beats all",
			statuses: []Status{StatusSkipped, StatusUnknown, StatusWarning, StatusCritical},
			want:     StatusCritical,
		},
		{
			name:     "single critical",
			statuses: []Status{StatusCritical},
			want:     StatusCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := make([]Result, len(tt.statuses))
			for i, s := range tt.statuses {
				results[i] = Result{Status: s}
			}
			got := WorstStatus(results)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSeverityOrdering(t *testing.T) {
	t.Parallel()
	// Verify the ordering: healthy < skipped == unknown < warning < critical
	assert.Less(t, Severity(StatusHealthy), Severity(StatusSkipped))
	assert.Less(t, Severity(StatusHealthy), Severity(StatusUnknown))
	assert.Equal(t, Severity(StatusSkipped), Severity(StatusUnknown))
	assert.Less(t, Severity(StatusSkipped), Severity(StatusWarning))
	assert.Less(t, Severity(StatusUnknown), Severity(StatusWarning))
	assert.Less(t, Severity(StatusWarning), Severity(StatusCritical))
}

func TestSeverityUnknownStatus(t *testing.T) {
	t.Parallel()
	// An unrecognised status string should map to severity 0 (same as healthy)
	assert.Equal(t, Severity(StatusHealthy), Severity(Status("bogus")))
}
