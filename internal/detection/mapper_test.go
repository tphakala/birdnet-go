package detection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestMapper_ToDatastore(t *testing.T) {
	mapper := NewMapper()

	now := time.Now()
	detection := &Detection{
		ID:             123,
		SourceNode:     "test-node",
		Date:           "2025-01-15",
		Time:           "14:30:00",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		SpeciesCode:    "amecro",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
		Confidence:     0.95,
		Threshold:      0.1,
		Sensitivity:    1.2,
		Latitude:       47.6062,
		Longitude:      -122.3321,
		ClipName:       "/clips/test.wav",
		ProcessingTime: 50 * time.Millisecond,
		Source: AudioSource{
			ID:          "rtsp_123",
			SafeString:  "rtsp://camera1",
			DisplayName: "Front Camera",
		},
		Occurrence: 0.85,
	}

	note := mapper.ToDatastore(detection)

	// Verify all persisted fields are copied
	assert.Equal(t, detection.ID, note.ID)
	assert.Equal(t, detection.SourceNode, note.SourceNode)
	assert.Equal(t, detection.Date, note.Date)
	assert.Equal(t, detection.Time, note.Time)
	assert.Equal(t, detection.BeginTime, note.BeginTime)
	assert.Equal(t, detection.EndTime, note.EndTime)
	assert.Equal(t, detection.SpeciesCode, note.SpeciesCode)
	assert.Equal(t, detection.ScientificName, note.ScientificName)
	assert.Equal(t, detection.CommonName, note.CommonName)
	assert.InDelta(t, detection.Confidence, note.Confidence, 0.001)
	assert.InDelta(t, detection.Threshold, note.Threshold, 0.001)
	assert.InDelta(t, detection.Sensitivity, note.Sensitivity, 0.001)
	assert.InDelta(t, detection.Latitude, note.Latitude, 0.001)
	assert.InDelta(t, detection.Longitude, note.Longitude, 0.001)
	assert.Equal(t, detection.ClipName, note.ClipName)
	assert.Equal(t, detection.ProcessingTime, note.ProcessingTime)

	// Verify runtime-only fields are NOT in datastore.Note
	// (Source and Occurrence are gorm:"-" fields)
}

func TestMapper_FromDatastore(t *testing.T) {
	mapper := NewMapper()

	now := time.Now()
	note := datastore.Note{
		ID:             456,
		SourceNode:     "test-node-2",
		Date:           "2025-01-16",
		Time:           "15:45:30",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		SpeciesCode:    "norcar",
		ScientificName: "Cardinalis cardinalis", //nolint:misspell // Latin species name
		CommonName:     "Northern Cardinal",
		Confidence:     0.88,
		Threshold:      0.15,
		Sensitivity:    1.0,
		Latitude:       40.7128,
		Longitude:      -74.0060,
		ClipName:       "/clips/cardinal.wav",
		ProcessingTime: 75 * time.Millisecond,
		Occurrence:     0.92,
		Verified:       "correct",
		Locked:         true,
	}

	source := AudioSource{
		ID:          "mic_456",
		SafeString:  "USB Microphone",
		DisplayName: "Back Yard Mic",
	}

	detection := mapper.FromDatastore(&note, source)

	// Verify all fields are copied
	assert.Equal(t, note.ID, detection.ID)
	assert.Equal(t, note.SourceNode, detection.SourceNode)
	assert.Equal(t, note.Date, detection.Date)
	assert.Equal(t, note.Time, detection.Time)
	assert.Equal(t, note.BeginTime, detection.BeginTime)
	assert.Equal(t, note.EndTime, detection.EndTime)
	assert.Equal(t, note.SpeciesCode, detection.SpeciesCode)
	assert.Equal(t, note.ScientificName, detection.ScientificName)
	assert.Equal(t, note.CommonName, detection.CommonName)
	assert.InDelta(t, note.Confidence, detection.Confidence, 0.001)
	assert.InDelta(t, note.Threshold, detection.Threshold, 0.001)
	assert.InDelta(t, note.Sensitivity, detection.Sensitivity, 0.001)
	assert.InDelta(t, note.Latitude, detection.Latitude, 0.001)
	assert.InDelta(t, note.Longitude, detection.Longitude, 0.001)
	assert.Equal(t, note.ClipName, detection.ClipName)
	assert.Equal(t, note.ProcessingTime, detection.ProcessingTime)
	assert.InDelta(t, note.Occurrence, detection.Occurrence, 0.001)
	assert.Equal(t, note.Verified, detection.Verified)
	assert.Equal(t, note.Locked, detection.Locked)

	// Verify AudioSource is injected from parameter
	assert.Equal(t, source, detection.Source)

	// Verify Species object is created
	require.NotNil(t, detection.Species)
	assert.Equal(t, note.SpeciesCode, detection.Species.SpeciesCode)
	assert.Equal(t, note.ScientificName, detection.Species.ScientificName)
	assert.Equal(t, note.CommonName, detection.Species.CommonName)
}

func TestMapper_RoundTrip(t *testing.T) {
	mapper := NewMapper()

	now := time.Now()
	original := &Detection{
		SourceNode:     "node-1",
		Date:           "2025-01-15",
		Time:           "10:30:00",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		SpeciesCode:    "rebwoo",
		ScientificName: "Melanerpes carolinus",
		CommonName:     "Red-bellied Woodpecker",
		Confidence:     0.92,
		Threshold:      0.1,
		Sensitivity:    1.5,
		Latitude:       35.0,
		Longitude:      -85.0,
		ClipName:       "/clips/woodpecker.wav",
		ProcessingTime: 60 * time.Millisecond,
		Source: AudioSource{
			ID:          "audio_1",
			SafeString:  "input.wav",
			DisplayName: "Test Input",
		},
		Occurrence: 0.75,
	}

	// Round trip: Detection → Note → Detection
	note := mapper.ToDatastore(original)
	converted := mapper.FromDatastore(&note, original.Source)

	// Verify data integrity
	assert.Equal(t, original.SourceNode, converted.SourceNode)
	assert.Equal(t, original.Date, converted.Date)
	assert.Equal(t, original.Time, converted.Time)
	assert.Equal(t, original.ScientificName, converted.ScientificName)
	assert.Equal(t, original.CommonName, converted.CommonName)
	assert.InDelta(t, original.Confidence, converted.Confidence, 0.001)
	assert.Equal(t, original.ClipName, converted.ClipName)

	// Verify AudioSource is preserved
	assert.Equal(t, original.Source, converted.Source)

	// Note: Occurrence is NOT persisted, so it won't round-trip unless set
	// This is expected behavior - it's a calculated field
}

func TestMapper_ToPredictionEntities(t *testing.T) {
	mapper := NewMapper()

	predictions := []Prediction{
		{
			Species: &Species{
				SpeciesCode:    "amecro",
				ScientificName: "Corvus brachyrhynchos",
				CommonName:     "American Crow",
			},
			Confidence: 0.95,
			Rank:       1,
		},
		{
			Species: &Species{
				SpeciesCode:    "comrav",
				ScientificName: "Corvus corax",
				CommonName:     "Common Raven",
			},
			Confidence: 0.85,
			Rank:       2,
		},
	}

	results := mapper.ToPredictionEntities(123, predictions)

	require.Len(t, results, 2)

	// Verify first result
	assert.Equal(t, uint(123), results[0].NoteID)
	assert.Contains(t, results[0].Species, "Corvus brachyrhynchos")
	assert.Contains(t, results[0].Species, "American Crow")
	assert.Contains(t, results[0].Species, "amecro")
	assert.InDelta(t, 0.95, float64(results[0].Confidence), 0.001)

	// Verify second result
	assert.Equal(t, uint(123), results[1].NoteID)
	assert.Contains(t, results[1].Species, "Corvus corax")
	assert.Contains(t, results[1].Species, "Common Raven")
	assert.InDelta(t, 0.85, float64(results[1].Confidence), 0.001)
}

func TestMapper_FromPredictionEntities(t *testing.T) {
	mapper := NewMapper()

	results := []datastore.Results{
		{
			NoteID:     456,
			Species:    "Turdus migratorius_American Robin_amerobin",
			Confidence: 0.90,
		},
		{
			NoteID:     456,
			Species:    "Sialia sialis_Eastern Bluebird_easblu",
			Confidence: 0.75,
		},
	}

	predictions := mapper.FromPredictionEntities(results)

	require.Len(t, predictions, 2)

	// Verify first prediction
	assert.Equal(t, "Turdus migratorius", predictions[0].Species.ScientificName)
	assert.Equal(t, "American Robin", predictions[0].Species.CommonName)
	assert.Equal(t, "amerobin", predictions[0].Species.SpeciesCode)
	assert.InDelta(t, 0.90, predictions[0].Confidence, 0.001) // float32->float64 conversion
	assert.Equal(t, 1, predictions[0].Rank) // 1-indexed

	// Verify second prediction
	assert.Equal(t, "Sialia sialis", predictions[1].Species.ScientificName)
	assert.Equal(t, "Eastern Bluebird", predictions[1].Species.CommonName)
	assert.Equal(t, "easblu", predictions[1].Species.SpeciesCode)
	assert.InDelta(t, 0.75, predictions[1].Confidence, 0.001) // float32->float64 conversion
	assert.Equal(t, 2, predictions[1].Rank)
}

func TestMapper_PredictionRoundTrip(t *testing.T) {
	mapper := NewMapper()

	originalPredictions := []Prediction{
		{
			Species: &Species{
				SpeciesCode:    "carwre",
				ScientificName: "Thryothorus ludovicianus",
				CommonName:     "Carolina Wren",
			},
			Confidence: 0.88,
			Rank:       1,
		},
		{
			Species: &Species{
				SpeciesCode:    "houspa",
				ScientificName: "Passer domesticus",
				CommonName:     "House Sparrow",
			},
			Confidence: 0.65,
			Rank:       2,
		},
	}

	// Round trip: Predictions → Results → Predictions
	results := mapper.ToPredictionEntities(789, originalPredictions)
	converted := mapper.FromPredictionEntities(results)

	require.Len(t, converted, 2)

	// Verify data integrity
	assert.Equal(t, originalPredictions[0].Species.ScientificName, converted[0].Species.ScientificName)
	assert.Equal(t, originalPredictions[0].Species.CommonName, converted[0].Species.CommonName)
	assert.Equal(t, originalPredictions[0].Species.SpeciesCode, converted[0].Species.SpeciesCode)
	assert.InDelta(t, originalPredictions[0].Confidence, converted[0].Confidence, 0.001) // float32 precision loss

	assert.Equal(t, originalPredictions[1].Species.ScientificName, converted[1].Species.ScientificName)
	assert.Equal(t, originalPredictions[1].Species.CommonName, converted[1].Species.CommonName)
	assert.InDelta(t, originalPredictions[1].Confidence, converted[1].Confidence, 0.001) // float32 precision loss
}

func TestMapper_FromDatastoreBatch(t *testing.T) {
	mapper := NewMapper()

	now := time.Now()
	notes := []datastore.Note{
		{
			ID:             1,
			ScientificName: "Species One",
			CommonName:     "Common One",
			Confidence:     0.9,
			BeginTime:      now,
			EndTime:        now.Add(3 * time.Second),
		},
		{
			ID:             2,
			ScientificName: "Species Two",
			CommonName:     "Common Two",
			Confidence:     0.8,
			BeginTime:      now,
			EndTime:        now.Add(3 * time.Second),
		},
	}

	source := AudioSource{ID: "test", SafeString: "test", DisplayName: "Test"}

	detections := mapper.FromDatastoreBatch(notes, source)

	require.Len(t, detections, 2)
	assert.Equal(t, "Species One", detections[0].ScientificName)
	assert.Equal(t, "Species Two", detections[1].ScientificName)
	assert.Equal(t, source, detections[0].Source)
	assert.Equal(t, source, detections[1].Source)
}

func TestMapper_ToDatastoreBatch(t *testing.T) {
	mapper := NewMapper()

	now := time.Now()
	detections := []*Detection{
		{
			ID:             1,
			ScientificName: "Species One",
			Confidence:     0.9,
			BeginTime:      now,
			EndTime:        now.Add(3 * time.Second),
		},
		{
			ID:             2,
			ScientificName: "Species Two",
			Confidence:     0.8,
			BeginTime:      now,
			EndTime:        now.Add(3 * time.Second),
		},
	}

	notes := mapper.ToDatastoreBatch(detections)

	require.Len(t, notes, 2)
	assert.Equal(t, "Species One", notes[0].ScientificName)
	assert.Equal(t, "Species Two", notes[1].ScientificName)
}

func TestMapper_EmptySpecies(t *testing.T) {
	mapper := NewMapper()

	note := datastore.Note{
		ID:             1,
		ScientificName: "",
		CommonName:     "",
		SpeciesCode:    "",
	}

	detection := mapper.FromDatastore(&note, AudioSource{})

	// Species object should not be created for empty data
	assert.Nil(t, detection.Species)
}
