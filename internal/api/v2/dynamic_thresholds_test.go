package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

func TestGetMergedThresholdData_NoDuplicates(t *testing.T) {
	t.Parallel()

	mockDS := mocks.NewMockInterface(t)
	now := time.Now()
	expires := now.Add(24 * time.Hour)

	// Database returns Title Case species name (as resolveCommonName does)
	mockDS.EXPECT().GetAllDynamicThresholds().Return([]datastore.DynamicThreshold{
		{
			SpeciesName:    "Tawny Owl",
			ScientificName: "Strix aluco",
			Level:          1,
			CurrentValue:   0.45,
			BaseThreshold:  0.6,
			HighConfCount:  1,
			ExpiresAt:      expires,
		},
	}, nil)

	// Processor memory stores lowercase species name
	proc := &processor.Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{Threshold: 0.6},
		},
		DynamicThresholds: map[string]*processor.DynamicThreshold{
			"tawny owl": {
				Level:          2,
				CurrentValue:   0.3,
				Timer:          expires,
				HighConfCount:  2,
				ValidHours:     24,
				ScientificName: "Strix aluco",
			},
		},
	}

	controller := &Controller{
		DS:        mockDS,
		Settings:  proc.Settings,
		Processor: proc,
	}

	result := controller.getMergedThresholdData()

	// Bug: before fix this returns 2 entries (one Title Case, one lowercase)
	// After fix: should return exactly 1 entry with memory overlay applied
	require.Len(t, result, 1, "should merge same species regardless of case")

	// Find the single entry
	var entry *DynamicThresholdResponse
	for _, v := range result {
		entry = v
	}
	require.NotNil(t, entry)

	// Memory data should override database data (memory is more current)
	assert.Equal(t, 2, entry.Level, "level should come from memory overlay")
	assert.InDelta(t, 0.3, entry.CurrentValue, 0.001, "current value should come from memory overlay")
	// Display name should be Title Case (from database, the proper display name)
	assert.Equal(t, "Tawny Owl", entry.SpeciesName, "display name should be Title Case from database")
}

func TestGetMergedThresholdData_MemoryOnlySpecies(t *testing.T) {
	t.Parallel()

	mockDS := mocks.NewMockInterface(t)
	expires := time.Now().Add(24 * time.Hour)

	// Database returns no thresholds
	mockDS.EXPECT().GetAllDynamicThresholds().Return([]datastore.DynamicThreshold{}, nil)

	// Processor memory has a species not in the database
	proc := &processor.Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{Threshold: 0.6},
		},
		DynamicThresholds: map[string]*processor.DynamicThreshold{
			"eurasian blue tit": {
				Level:          1,
				CurrentValue:   0.45,
				Timer:          expires,
				HighConfCount:  1,
				ValidHours:     24,
				ScientificName: "Cyanistes caeruleus",
			},
		},
	}

	controller := &Controller{
		DS:        mockDS,
		Settings:  proc.Settings,
		Processor: proc,
	}

	result := controller.getMergedThresholdData()

	require.Len(t, result, 1, "memory-only species should appear")
	var entry *DynamicThresholdResponse
	for _, v := range result {
		entry = v
	}
	require.NotNil(t, entry)
	assert.Equal(t, "eurasian blue tit", entry.SpeciesName)
	assert.Equal(t, "Cyanistes caeruleus", entry.ScientificName)
}

func TestGetMergedThresholdData_DatabaseOnlySpecies(t *testing.T) {
	t.Parallel()

	mockDS := mocks.NewMockInterface(t)
	now := time.Now()
	expires := now.Add(24 * time.Hour)

	// Database has a species
	mockDS.EXPECT().GetAllDynamicThresholds().Return([]datastore.DynamicThreshold{
		{
			SpeciesName:    "Common Blackbird",
			ScientificName: "Turdus merula",
			Level:          1,
			CurrentValue:   0.45,
			BaseThreshold:  0.6,
			HighConfCount:  1,
			ExpiresAt:      expires,
		},
	}, nil)

	// Processor has no thresholds (empty map)
	proc := &processor.Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{Threshold: 0.6},
		},
		DynamicThresholds: map[string]*processor.DynamicThreshold{},
	}

	controller := &Controller{
		DS:        mockDS,
		Settings:  proc.Settings,
		Processor: proc,
	}

	result := controller.getMergedThresholdData()

	require.Len(t, result, 1, "database-only species should appear")
	var entry *DynamicThresholdResponse
	for _, v := range result {
		entry = v
	}
	require.NotNil(t, entry)
	assert.Equal(t, "Common Blackbird", entry.SpeciesName)
	assert.Equal(t, "Turdus merula", entry.ScientificName)
	assert.Equal(t, 1, entry.Level)
}
