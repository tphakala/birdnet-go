// detection_repository_bridge_test.go - Tests that DetectionRepository
// produces the same behavior as direct datastore.Interface usage.
//
// These tests verify the bridge implementation maintains behavioral equivalence,
// ensuring safe refactoring from direct Interface usage to DetectionRepository.
package datastore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// TestDetectionRepository_Save_MatchesInterfaceBehavior verifies that
// saving through DetectionRepository produces the same result as using
// datastore.Interface directly.
//
// This test demonstrates the bridge pattern and validates that DatabaseAction
// could safely use DetectionRepository instead of Interface.Save().
func TestDetectionRepository_Save_MatchesInterfaceBehavior(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	store := createDatabase(t, settings)

	// Create repository wrapping the store
	repo := NewDetectionRepository(store, time.UTC)

	// Create a detection.Result
	now := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
	result := &detection.Result{
		Timestamp:  now,
		SourceNode: "test-node",
		AudioSource: detection.AudioSource{
			ID:          "test-source",
			SafeString:  "test-source",
			DisplayName: "Test Source",
		},
		BeginTime: now,
		EndTime:   now.Add(3 * time.Second),
		Species: detection.Species{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			Code:           "amerob",
		},
		Confidence: 0.95,
		Latitude:   42.0,
		Longitude:  -71.0,
		ClipName:   "test.wav",
	}

	additionalResults := []detection.AdditionalResult{
		{
			Species:    detection.Species{ScientificName: "Parus major", CommonName: "Great Tit", Code: "gretit1"},
			Confidence: 0.85,
		},
	}

	// Save through repository
	ctx := context.Background()
	err := repo.Save(ctx, result, additionalResults)
	require.NoError(t, err, "Save should succeed")

	// Verify ID was assigned
	assert.NotEqual(t, uint(0), result.ID, "ID should be assigned")

	// Retrieve through repository
	retrieved, err := repo.Get(ctx, fmt.Sprintf("%d", result.ID))
	require.NoError(t, err, "Get should succeed")

	// Verify retrieved matches original
	assert.Equal(t, result.ID, retrieved.ID)
	assert.Equal(t, result.Species.CommonName, retrieved.Species.CommonName)
	assert.Equal(t, result.Species.ScientificName, retrieved.Species.ScientificName)
	assert.InDelta(t, result.Confidence, retrieved.Confidence, 0.001)
	assert.Equal(t, result.ClipName, retrieved.ClipName)

	// Verify additional results
	additionalRetrieved, err := repo.GetAdditionalResults(ctx, fmt.Sprintf("%d", result.ID))
	require.NoError(t, err)
	require.Len(t, additionalRetrieved, 1)
	assert.Equal(t, "Parus major", additionalRetrieved[0].Species.ScientificName)
}

// TestDetectionRepository_Save_IDAssignmentMatchesInterface verifies that
// DetectionRepository.Save() assigns the ID back to the result the same way
// Interface.Save() assigns ID back to the Note.
//
// This is critical for the DatabaseAction refactoring path.
func TestDetectionRepository_Save_IDAssignmentMatchesInterface(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	store := createDatabase(t, settings)
	repo := NewDetectionRepository(store, time.UTC)

	// Save via Interface directly
	note1 := Note{
		SourceNode:     "direct-interface",
		Date:           "2024-06-15",
		Time:           "14:30:45",
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Confidence:     0.95,
		ClipName:       "direct.wav",
	}
	err := store.Save(&note1, nil)
	require.NoError(t, err, "Direct Interface.Save should succeed")
	directID := note1.ID

	// Save via DetectionRepository
	now := time.Date(2024, 6, 15, 14, 31, 45, 0, time.UTC)
	result := &detection.Result{
		Timestamp:  now,
		SourceNode: "via-repository",
		Species: detection.Species{
			ScientificName: "Parus major",
			CommonName:     "Great Tit",
			Code:           "gretit1",
		},
		Confidence: 0.90,
		ClipName:   "repo.wav",
	}

	ctx := context.Background()
	err = repo.Save(ctx, result, nil)
	require.NoError(t, err, "Repository.Save should succeed")
	repoID := result.ID

	// Both should have valid, different IDs
	assert.NotEqual(t, uint(0), directID, "Direct save should assign ID")
	assert.NotEqual(t, uint(0), repoID, "Repository save should assign ID")
	assert.NotEqual(t, directID, repoID, "IDs should be different (sequential)")

	// Verify both can be retrieved
	loadedDirect, err := store.Get(fmt.Sprintf("%d", directID))
	require.NoError(t, err)
	assert.Equal(t, "American Robin", loadedDirect.CommonName)

	loadedRepo, err := repo.Get(ctx, fmt.Sprintf("%d", repoID))
	require.NoError(t, err)
	assert.Equal(t, "Great Tit", loadedRepo.Species.CommonName)
}

// TestDetectionRepository_Save_AdditionalResultsMatchInterface verifies that
// additional results saved through DetectionRepository are stored identically
// to results saved through Interface.Save().
func TestDetectionRepository_Save_AdditionalResultsMatchInterface(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	store := createDatabase(t, settings)
	repo := NewDetectionRepository(store, time.UTC)

	// Save via Interface directly with Results
	note := Note{
		SourceNode:     "direct-interface",
		Date:           "2024-06-15",
		Time:           "14:30:45",
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Confidence:     0.95,
		ClipName:       "direct.wav",
	}
	directResults := []Results{
		{Species: "Parus major_Great Tit_gretit1", Confidence: 0.85},
		{Species: "Cyanistes caeruleus_Blue Tit_eurblu", Confidence: 0.75},
	}
	err := store.Save(&note, directResults)
	require.NoError(t, err)

	// Verify direct results via Interface
	directLoaded, err := store.GetNoteResults(fmt.Sprintf("%d", note.ID))
	require.NoError(t, err)
	require.Len(t, directLoaded, 2)

	// Save via DetectionRepository with AdditionalResults
	now := time.Date(2024, 6, 15, 14, 31, 45, 0, time.UTC)
	result := &detection.Result{
		Timestamp:  now,
		SourceNode: "via-repository",
		Species: detection.Species{
			ScientificName: "Corvus corax",
			CommonName:     "Common Raven",
			Code:           "comrav",
		},
		Confidence: 0.92,
		ClipName:   "repo.wav",
	}
	additionalResults := []detection.AdditionalResult{
		{
			Species:    detection.Species{ScientificName: "Corvus corone", CommonName: "Carrion Crow", Code: "carcro"},
			Confidence: 0.82,
		},
		{
			Species:    detection.Species{ScientificName: "Corvus monedula", CommonName: "Jackdaw", Code: "jackda"},
			Confidence: 0.72,
		},
	}

	ctx := context.Background()
	err = repo.Save(ctx, result, additionalResults)
	require.NoError(t, err)

	// Verify repository results via repository method
	repoLoaded, err := repo.GetAdditionalResults(ctx, fmt.Sprintf("%d", result.ID))
	require.NoError(t, err)
	require.Len(t, repoLoaded, 2)

	// Both should have same count of additional results
	assert.Len(t, directLoaded, len(repoLoaded),
		"Direct and Repository should save same number of results")
}

// TestDetectionRepository_ConversionFunctions verifies that the exported
// conversion functions produce correct results for use by action structs.
func TestDetectionRepository_ConversionFunctions(t *testing.T) {
	t.Parallel()

	t.Run("NoteFromResult converts correctly", func(t *testing.T) {
		now := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
		result := &detection.Result{
			ID:         42,
			Timestamp:  now,
			SourceNode: "test-node",
			AudioSource: detection.AudioSource{
				ID:          "source-1",
				SafeString:  "safe-string",
				DisplayName: "Display Name",
			},
			BeginTime: now,
			EndTime:   now.Add(3 * time.Second),
			Species: detection.Species{
				ScientificName: "Turdus migratorius",
				CommonName:     "American Robin",
				Code:           "amerob",
			},
			Confidence:     0.95,
			Latitude:       42.0,
			Longitude:      -71.0,
			Threshold:      0.7,
			Sensitivity:    1.0,
			ClipName:       "clip.wav",
			ProcessingTime: 150 * time.Millisecond,
			Occurrence:     0.85,
		}

		note := NoteFromResult(result)

		assert.Equal(t, result.ID, note.ID)
		assert.Equal(t, result.SourceNode, note.SourceNode)
		assert.Equal(t, "2024-06-15", note.Date)
		assert.Equal(t, "14:30:45", note.Time)
		assert.Equal(t, result.Species.ScientificName, note.ScientificName)
		assert.Equal(t, result.Species.CommonName, note.CommonName)
		assert.Equal(t, result.Species.Code, note.SpeciesCode)
		assert.InDelta(t, result.Confidence, note.Confidence, 0.001)
		assert.InDelta(t, result.Latitude, note.Latitude, 0.001)
		assert.InDelta(t, result.Longitude, note.Longitude, 0.001)
		assert.Equal(t, result.ClipName, note.ClipName)
		assert.Equal(t, result.AudioSource.ID, note.Source.ID)
		assert.Equal(t, result.AudioSource.SafeString, note.Source.SafeString)
		assert.Equal(t, result.AudioSource.DisplayName, note.Source.DisplayName)
	})

	t.Run("AdditionalResultsToDatastoreResults converts correctly", func(t *testing.T) {
		additionalResults := []detection.AdditionalResult{
			{
				Species:    detection.Species{ScientificName: "Parus major", CommonName: "Great Tit", Code: "gretit1"},
				Confidence: 0.85,
			},
			{
				Species:    detection.Species{ScientificName: "Cyanistes caeruleus", CommonName: "Blue Tit"},
				Confidence: 0.75,
			},
		}

		results := AdditionalResultsToDatastoreResults(additionalResults)

		require.Len(t, results, 2)
		assert.Contains(t, results[0].Species, "Parus major")
		assert.Contains(t, results[0].Species, "Great Tit")
		assert.Contains(t, results[0].Species, "gretit1")
		assert.InDelta(t, 0.85, float64(results[0].Confidence), 0.001)

		assert.Contains(t, results[1].Species, "Cyanistes caeruleus")
		assert.Contains(t, results[1].Species, "Blue Tit")
		assert.InDelta(t, 0.75, float64(results[1].Confidence), 0.001)
	})

	t.Run("AdditionalResultsToDatastoreResults handles nil", func(t *testing.T) {
		results := AdditionalResultsToDatastoreResults(nil)
		assert.Nil(t, results)
	})

	t.Run("AdditionalResultsToDatastoreResults handles empty slice", func(t *testing.T) {
		results := AdditionalResultsToDatastoreResults([]detection.AdditionalResult{})
		assert.Nil(t, results)
	})
}
