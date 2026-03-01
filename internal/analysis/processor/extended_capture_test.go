package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestIsExtendedCaptureSpecies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		enabled        bool
		allSpecies     bool
		speciesMap     map[string]bool
		scientificName string
		expected       bool
	}{
		{
			name:           "disabled returns false",
			enabled:        false,
			scientificName: "Strix aluco",
			expected:       false,
		},
		{
			name:           "all species mode returns true",
			enabled:        true,
			allSpecies:     true,
			scientificName: "Strix aluco",
			expected:       true,
		},
		{
			name:           "matching species returns true",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"strix aluco": true},
			scientificName: "Strix aluco",
			expected:       true,
		},
		{
			name:           "non-matching species returns false",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"strix aluco": true},
			scientificName: "Parus major",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: tt.enabled},
					},
				},
				extendedCaptureAll:     tt.allSpecies,
				extendedCaptureSpecies: tt.speciesMap,
			}
			assert.Equal(t, tt.expected, p.isExtendedCaptureSpecies(tt.scientificName))
		})
	}
}

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

func TestResolveExtendedCaptureFilter_WithTaxonomy(t *testing.T) {
	t.Parallel()

	db, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err)

	// Resolve "Strigidae" (owl family) via taxonomy DB
	isAll, resolved := resolveSpeciesFilter([]string{"Strigidae"}, nil, db)
	assert.False(t, isAll)
	assert.NotEmpty(t, resolved)
	// Should include well-known owls
	assert.True(t, resolved["strix aluco"] || resolved["bubo bubo"] || len(resolved) > 5,
		"Strigidae should resolve to multiple owl species, got %d", len(resolved))
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
