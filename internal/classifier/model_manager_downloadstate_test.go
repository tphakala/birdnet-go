package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDownloadStateIsActive pins the failed-download follow-up: only a genuinely
// in-progress
// download suppresses the not-analyzing alarm. A FAILED state is retained for
// failedStateRetention so SSE pollers can observe it, but the model is NOT
// analyzing during that window, so IsActive must report false for it (and for the
// terminal complete/removed states, and a nil receiver).
func TestDownloadStateIsActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   bool
	}{
		{StatusDownloading, true},
		{StatusVerifying, true},
		{StatusLoading, true},
		{StatusFailed, false},
		{StatusComplete, false},
		{StatusRemoved, false},
		{"", false},
		{"bogus", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			s := &DownloadState{Status: tt.status}
			assert.Equal(t, tt.want, s.IsActive())
		})
	}

	var nilState *DownloadState
	assert.False(t, nilState.IsActive(), "a nil download state is not active")
}
