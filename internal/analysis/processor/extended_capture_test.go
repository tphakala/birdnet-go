package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateExtendedFlushDeadline(t *testing.T) {
	t.Parallel()

	now := time.Now()
	maxDuration := 10 * time.Minute
	normalDetectionWindow := 5 * time.Second

	tests := []struct {
		name            string
		firstDetected   time.Time
		now             time.Time
		maxDeadline     time.Time
		expectedMinWait time.Duration
		expectedMaxWait time.Duration
	}{
		{
			name:            "short session under 30s uses minimum 15s",
			firstDetected:   now.Add(-10 * time.Second),
			now:             now,
			maxDeadline:     now.Add(maxDuration),
			expectedMinWait: 15 * time.Second,
			expectedMaxWait: 15 * time.Second,
		},
		{
			name:            "medium session 30s-2m waits 30s",
			firstDetected:   now.Add(-45 * time.Second),
			now:             now,
			maxDeadline:     now.Add(maxDuration),
			expectedMinWait: 30 * time.Second,
			expectedMaxWait: 30 * time.Second,
		},
		{
			name:            "long session over 2m waits 60s",
			firstDetected:   now.Add(-3 * time.Minute),
			now:             now,
			maxDeadline:     now.Add(maxDuration),
			expectedMinWait: 60 * time.Second,
			expectedMaxWait: 60 * time.Second,
		},
		{
			name:            "capped at maxDeadline",
			firstDetected:   now.Add(-9*time.Minute - 50*time.Second),
			now:             now,
			maxDeadline:     now.Add(10 * time.Second),
			expectedMinWait: 0,
			expectedMaxWait: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deadline := calculateExtendedFlushDeadline(
				tt.now, tt.firstDetected, tt.maxDeadline, normalDetectionWindow,
			)
			waitTime := deadline.Sub(tt.now)
			assert.GreaterOrEqual(t, waitTime, tt.expectedMinWait,
				"wait time %v should be >= %v", waitTime, tt.expectedMinWait)
			assert.LessOrEqual(t, waitTime, tt.expectedMaxWait,
				"wait time %v should be <= %v", waitTime, tt.expectedMaxWait)
		})
	}
}
