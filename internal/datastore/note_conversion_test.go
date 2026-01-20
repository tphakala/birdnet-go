package datastore_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestNoteFromResult(t *testing.T) {
	now := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
	result := detection.Result{
		ID:         123,
		Timestamp:  now,
		SourceNode: "test-node",
		AudioSource: detection.AudioSource{
			ID:          "rtsp://camera1",
			SafeString:  "camera1",
			DisplayName: "Camera 1",
		},
		BeginTime: now,
		EndTime:   now.Add(3 * time.Second),
		Species: detection.Species{
			Code:           "amerob",
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
		},
		Confidence:     0.95,
		Latitude:       42.0,
		Longitude:      -71.0,
		Threshold:      0.8,
		Sensitivity:    1.0,
		ClipName:       "test.wav",
		ProcessingTime: 100 * time.Millisecond,
		Occurrence:     0.85,
		Verified:       "correct",
		Locked:         true,
	}

	note := datastore.NoteFromResult(&result)

	assert.Equal(t, uint(123), note.ID)
	assert.Equal(t, "test-node", note.SourceNode)
	assert.Equal(t, "2024-06-15", note.Date)
	assert.Equal(t, "14:30:45", note.Time)
	assert.Equal(t, "rtsp://camera1", note.Source.ID)
	assert.Equal(t, "camera1", note.Source.SafeString)
	assert.Equal(t, "Camera 1", note.Source.DisplayName)
	assert.Equal(t, now, note.BeginTime)
	assert.Equal(t, now.Add(3*time.Second), note.EndTime)
	assert.Equal(t, "amerob", note.SpeciesCode)
	assert.Equal(t, "Turdus migratorius", note.ScientificName)
	assert.Equal(t, "American Robin", note.CommonName)
	assert.InDelta(t, 0.95, note.Confidence, 0.001)
	assert.InDelta(t, 42.0, note.Latitude, 0.001)
	assert.InDelta(t, -71.0, note.Longitude, 0.001)
	assert.InDelta(t, 0.8, note.Threshold, 0.001)
	assert.InDelta(t, 1.0, note.Sensitivity, 0.001)
	assert.Equal(t, "test.wav", note.ClipName)
	assert.Equal(t, 100*time.Millisecond, note.ProcessingTime)
	assert.InDelta(t, 0.85, note.Occurrence, 0.001)
	assert.Equal(t, "correct", note.Verified)
	assert.True(t, note.Locked)
}

func TestNoteFromResult_ZeroValues(t *testing.T) {
	result := detection.Result{
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	note := datastore.NoteFromResult(&result)

	assert.Equal(t, uint(0), note.ID)
	assert.Empty(t, note.SourceNode)
	assert.Equal(t, "2024-01-01", note.Date)
	assert.Equal(t, "00:00:00", note.Time)
	assert.Empty(t, note.CommonName)
	assert.InDelta(t, 0.0, note.Confidence, 0.001)
}

func TestAdditionalResultsToDatastoreResults(t *testing.T) {
	t.Run("empty slice returns nil", func(t *testing.T) {
		result := datastore.AdditionalResultsToDatastoreResults(nil)
		assert.Nil(t, result)

		result = datastore.AdditionalResultsToDatastoreResults([]detection.AdditionalResult{})
		assert.Nil(t, result)
	})

	t.Run("converts results without species code", func(t *testing.T) {
		input := []detection.AdditionalResult{
			{
				Species: detection.Species{
					ScientificName: "Turdus merula",
					CommonName:     "Common Blackbird",
				},
				Confidence: 0.85,
			},
		}

		result := datastore.AdditionalResultsToDatastoreResults(input)

		require.Len(t, result, 1)
		assert.Equal(t, "Turdus merula_Common Blackbird", result[0].Species)
		assert.InDelta(t, float32(0.85), result[0].Confidence, 0.001)
	})

	t.Run("converts results with species code", func(t *testing.T) {
		input := []detection.AdditionalResult{
			{
				Species: detection.Species{
					ScientificName: "Turdus migratorius",
					CommonName:     "American Robin",
					Code:           "amerob",
				},
				Confidence: 0.92,
			},
		}

		result := datastore.AdditionalResultsToDatastoreResults(input)

		require.Len(t, result, 1)
		assert.Equal(t, "Turdus migratorius_American Robin_amerob", result[0].Species)
		assert.InDelta(t, float32(0.92), result[0].Confidence, 0.001)
	})

	t.Run("converts multiple results", func(t *testing.T) {
		input := []detection.AdditionalResult{
			{
				Species: detection.Species{
					ScientificName: "Turdus merula",
					CommonName:     "Common Blackbird",
				},
				Confidence: 0.85,
			},
			{
				Species: detection.Species{
					ScientificName: "Turdus philomelos",
					CommonName:     "Song Thrush",
					Code:           "sonthr",
				},
				Confidence: 0.75,
			},
		}

		result := datastore.AdditionalResultsToDatastoreResults(input)

		require.Len(t, result, 2)
		assert.Equal(t, "Turdus merula_Common Blackbird", result[0].Species)
		assert.InDelta(t, float32(0.85), result[0].Confidence, 0.001)
		assert.Equal(t, "Turdus philomelos_Song Thrush_sonthr", result[1].Species)
		assert.InDelta(t, float32(0.75), result[1].Confidence, 0.001)
	})
}
