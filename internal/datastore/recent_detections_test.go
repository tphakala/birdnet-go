package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetLastDetectionsWithMinimumConfidence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}, &NoteComment{}, &NoteLock{}))

	ds := &DataStore{DB: db}
	notes := []Note{
		{Date: "2026-05-25", Time: "10:00:00", ScientificName: "Turdus migratorius", CommonName: "American Robin", Confidence: 0.95},
		{Date: "2026-05-25", Time: "10:01:00", ScientificName: "Corvus brachyrhynchos", CommonName: "American Crow", Confidence: 0.60},
		{Date: "2026-05-25", Time: "10:02:00", ScientificName: "Cyanocitta cristata", CommonName: "Blue Jay", Confidence: 0.85},
	}
	for i := range notes {
		require.NoError(t, db.Create(&notes[i]).Error)
	}

	got, err := ds.GetLastDetectionsWithMinimumConfidence(10, 0.8)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Blue Jay", got[0].CommonName)
	assert.Equal(t, "American Robin", got[1].CommonName)
	for i := range got {
		assert.GreaterOrEqual(t, got[i].Confidence, 0.8)
	}
}
