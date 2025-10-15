package detection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDetection_Valid(t *testing.T) {
	now := time.Now()
	source := AudioSource{
		ID:          "test_source",
		SafeString:  "safe",
		DisplayName: "Test Source",
	}

	detection, err := NewDetection(
		"test-node",
		"2025-01-15",
		"14:30:00",
		now,
		now.Add(3*time.Second),
		source,
		"amecro",
		"Corvus brachyrhynchos",
		"American Crow",
		0.95,
		0.1,
		1.2,
		47.6062,
		-122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.NoError(t, err)
	assert.NotNil(t, detection)
	assert.Equal(t, "test-node", detection.SourceNode)
	assert.Equal(t, "Corvus brachyrhynchos", detection.ScientificName)
	assert.InDelta(t, 0.95, detection.Confidence, 0.001)
}

func TestNewDetection_EmptySourceNode(t *testing.T) {
	now := time.Now()
	source := AudioSource{ID: "test"}

	_, err := NewDetection(
		"",                       // Empty sourceNode
		"2025-01-15", "14:30:00",
		now, now.Add(3*time.Second),
		source,
		"amecro", "Corvus brachyrhynchos", "American Crow",
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sourceNode cannot be empty")
}

func TestNewDetection_EmptySpeciesNames(t *testing.T) {
	now := time.Now()
	source := AudioSource{ID: "test"}

	_, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		now, now.Add(3*time.Second),
		source,
		"amecro",
		"",  // Empty scientific name
		"",  // Empty common name
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "either scientificName or commonName must be provided")
}

func TestNewDetection_InvalidConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		wantErr    string
	}{
		{"confidence too high", 1.5, "confidence must be between 0.0 and 1.0"},
		{"confidence too low", -0.1, "confidence must be between 0.0 and 1.0"},
		{"confidence at lower bound", 0.0, ""},
		{"confidence at upper bound", 1.0, ""},
		{"confidence valid", 0.95, ""},
	}

	now := time.Now()
	source := AudioSource{ID: "test"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDetection(
				"test-node",
				"2025-01-15", "14:30:00",
				now, now.Add(3*time.Second),
				source,
				"amecro", "Corvus brachyrhynchos", "American Crow",
				tt.confidence, 0.1, 1.2,
				47.6062, -122.3321,
				"/clips/test.wav",
				50*time.Millisecond,
				0.85,
			)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewDetection_InvalidOccurrence(t *testing.T) {
	tests := []struct {
		name       string
		occurrence float64
		wantErr    string
	}{
		{"occurrence too high", 1.5, "occurrence must be between 0.0 and 1.0"},
		{"occurrence too low", -0.1, "occurrence must be between 0.0 and 1.0"},
		{"occurrence at lower bound", 0.0, ""},
		{"occurrence at upper bound", 1.0, ""},
		{"occurrence valid", 0.85, ""},
	}

	now := time.Now()
	source := AudioSource{ID: "test"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDetection(
				"test-node",
				"2025-01-15", "14:30:00",
				now, now.Add(3*time.Second),
				source,
				"amecro", "Corvus brachyrhynchos", "American Crow",
				0.95, 0.1, 1.2,
				47.6062, -122.3321,
				"/clips/test.wav",
				50*time.Millisecond,
				tt.occurrence,
			)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewDetection_InvalidTimeRange(t *testing.T) {
	now := time.Now()
	source := AudioSource{ID: "test"}

	// endTime before beginTime
	_, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		now,
		now.Add(-3*time.Second), // End before begin
		source,
		"amecro", "Corvus brachyrhynchos", "American Crow",
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "endTime cannot be before beginTime")
}

func TestNewDetection_OnlyScientificName(t *testing.T) {
	now := time.Now()
	source := AudioSource{ID: "test"}

	detection, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		now, now.Add(3*time.Second),
		source,
		"amecro",
		"Corvus brachyrhynchos",
		"", // No common name
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.NoError(t, err)
	assert.Equal(t, "Corvus brachyrhynchos", detection.ScientificName)
	assert.Empty(t, detection.CommonName)
}

func TestNewDetection_OnlyCommonName(t *testing.T) {
	now := time.Now()
	source := AudioSource{ID: "test"}

	detection, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		now, now.Add(3*time.Second),
		source,
		"amecro",
		"", // No scientific name
		"American Crow",
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.NoError(t, err)
	assert.Empty(t, detection.ScientificName)
	assert.Equal(t, "American Crow", detection.CommonName)
}

func TestNewDetection_ZeroTimes(t *testing.T) {
	source := AudioSource{ID: "test"}

	// Zero times should be allowed (they're checked only if non-zero)
	detection, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		time.Time{}, // Zero begin time
		time.Time{}, // Zero end time
		source,
		"amecro", "Corvus brachyrhynchos", "American Crow",
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.NoError(t, err)
	assert.True(t, detection.BeginTime.IsZero())
	assert.True(t, detection.EndTime.IsZero())
}

func TestNewDetection_OneZeroTime(t *testing.T) {
	now := time.Now()
	source := AudioSource{ID: "test"}

	// Zero end time with non-zero begin time should be allowed
	detection, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		now,
		time.Time{}, // Zero end time
		source,
		"amecro", "Corvus brachyrhynchos", "American Crow",
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.NoError(t, err)
	assert.False(t, detection.BeginTime.IsZero())
	assert.True(t, detection.EndTime.IsZero())

	// Zero begin time with non-zero end time should also be allowed
	detection2, err := NewDetection(
		"test-node",
		"2025-01-15", "14:30:00",
		time.Time{}, // Zero begin time
		now,
		source,
		"amecro", "Corvus brachyrhynchos", "American Crow",
		0.95, 0.1, 1.2,
		47.6062, -122.3321,
		"/clips/test.wav",
		50*time.Millisecond,
		0.85,
	)

	require.NoError(t, err)
	assert.True(t, detection2.BeginTime.IsZero())
	assert.False(t, detection2.EndTime.IsZero())
}
