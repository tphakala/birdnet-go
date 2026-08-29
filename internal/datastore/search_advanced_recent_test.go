package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchNotesAdvancedExactRangeExcludesFalsePositives(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}, &NoteLock{}, &NoteComment{}))
	ds := &DataStore{DB: db}

	notes := []Note{
		{Date: "2026-05-01", Time: "21:59:59", ScientificName: "Before window", Confidence: 0.9},
		{Date: "2026-05-01", Time: "22:00:00", ScientificName: "Window start", Confidence: 0.8},
		{Date: "2026-05-01", Time: "23:00:00", ScientificName: "False positive", Confidence: 0.95},
		{Date: "2026-05-02", Time: "01:30:00", ScientificName: "Inside window", Confidence: 0.9},
		{Date: "2026-05-02", Time: "01:45:00", ScientificName: "Below confidence", Confidence: 0.6},
		{Date: "2026-05-02", Time: "02:00:00", ScientificName: "Window end", Confidence: 0.9},
	}
	require.NoError(t, db.Create(&notes).Error)
	require.NoError(t, db.Create(&NoteReview{
		NoteID:   notes[2].ID,
		Verified: "false_positive",
	}).Error)

	start := time.Date(2026, 5, 1, 22, 0, 0, 0, time.Local)
	end := time.Date(2026, 5, 2, 2, 0, 0, 0, time.Local)
	results, total, err := ds.SearchNotesAdvanced(&AdvancedSearchFilters{
		DetectedAtRange: &DateRange{Start: start, End: end},
		Confidence: &ConfidenceFilter{
			Operator: ">=",
			Value:    0.7,
		},
		ExcludeFalsePositives: true,
		MinimalResults:        true,
		SkipTotal:             true,
	})

	require.NoError(t, err)
	assert.Zero(t, total)
	require.Len(t, results, 2)
	assert.Equal(t, "Inside window", results[0].ScientificName)
	assert.Equal(t, "Window start", results[1].ScientificName)
	assert.Nil(t, results[0].Review)
}

func TestSearchNotesAdvancedExactRangeTakesPrecedenceOverDateRange(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}, &NoteLock{}, &NoteComment{}))
	ds := &DataStore{DB: db}

	notes := []Note{
		{Date: "2026-05-01", Time: "21:59:59", ScientificName: "Before window", Confidence: 0.9},
		{Date: "2026-05-01", Time: "22:00:00", ScientificName: "Window start", Confidence: 0.8},
		{Date: "2026-05-02", Time: "01:30:00", ScientificName: "Inside window", Confidence: 0.9},
		{Date: "2026-05-02", Time: "02:00:00", ScientificName: "Window end", Confidence: 0.9},
	}
	require.NoError(t, db.Create(&notes).Error)

	start := time.Date(2026, 5, 1, 22, 0, 0, 0, time.Local)
	end := time.Date(2026, 5, 2, 2, 0, 0, 0, time.Local)
	results, total, err := ds.SearchNotesAdvanced(&AdvancedSearchFilters{
		DateRange:       &DateRange{Start: start.AddDate(0, 0, 10), End: end.AddDate(0, 0, 10)},
		DetectedAtRange: &DateRange{Start: start, End: end},
		MinimalResults:  true,
		SkipTotal:       true,
	})

	require.NoError(t, err)
	assert.Zero(t, total)
	require.Len(t, results, 2)
	assert.Equal(t, "Inside window", results[0].ScientificName)
	assert.Equal(t, "Window start", results[1].ScientificName)
}
