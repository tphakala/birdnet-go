package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetLastDetectionsWithMinimumConfidence(t *testing.T) {
	const (
		highConfidence = 0.95
		lowConfidence  = 0.60
		midConfidence  = 0.85
		resultLimit    = 10
		minConfidence  = 0.8
		expectedCount  = 2
	)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}, &NoteComment{}, &NoteLock{}))

	ds := &DataStore{DB: db}
	notes := []Note{
		{Date: "2026-05-25", Time: "10:00:00", ScientificName: "Turdus migratorius", CommonName: "American Robin", Confidence: highConfidence},
		{Date: "2026-05-25", Time: "10:01:00", ScientificName: "Corvus brachyrhynchos", CommonName: "American Crow", Confidence: lowConfidence},
		{Date: "2026-05-25", Time: "10:02:00", ScientificName: "Cyanocitta cristata", CommonName: "Blue Jay", Confidence: midConfidence},
	}
	for i := range notes {
		require.NoError(t, db.Create(&notes[i]).Error)
	}

	got, err := ds.GetLastDetectionsWithMinimumConfidence(resultLimit, minConfidence)
	require.NoError(t, err)
	require.Len(t, got, expectedCount)
	assert.Equal(t, "Blue Jay", got[0].CommonName)
	assert.Equal(t, "American Robin", got[1].CommonName)
	for i := range got {
		assert.GreaterOrEqual(t, got[i].Confidence, minConfidence)
	}
}
