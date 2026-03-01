package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveExtendedCaptureFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configSpecies []string
		labels        []string
		expectAll     bool
		expectSpecies []string
	}{
		{
			name:          "empty list means all species",
			configSpecies: []string{},
			labels:        []string{},
			expectAll:     true,
		},
		{
			name:          "nil list means all species",
			configSpecies: nil,
			labels:        []string{},
			expectAll:     true,
		},
		{
			name:          "common name resolved via labels",
			configSpecies: []string{"Eurasian Eagle-Owl"},
			labels:        []string{"Bubo bubo_Eurasian Eagle-Owl", "Strix aluco_Tawny Owl"},
			expectAll:     false,
			expectSpecies: []string{"bubo bubo"},
		},
		{
			name:          "scientific name resolved directly",
			configSpecies: []string{"Bubo bubo"},
			labels:        []string{"Bubo bubo_Eurasian Eagle-Owl"},
			expectAll:     false,
			expectSpecies: []string{"bubo bubo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			isAll, resolved := resolveSpeciesFilter(tt.configSpecies, tt.labels, nil)
			assert.Equal(t, tt.expectAll, isAll)
			if !tt.expectAll {
				for _, expected := range tt.expectSpecies {
					assert.True(t, resolved[expected],
						"expected %q in resolved set", expected)
				}
			}
		})
	}
}

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
