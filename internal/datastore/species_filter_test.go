package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestApplySpeciesFilter covers the legacy-datastore species filter, including the
// SpeciesScientific branch added for the per-visitor dictionary search. The key
// regression it guards: a dictionary-resolved search sends an empty Species and a
// non-empty SpeciesScientific list; without the IN branch the legacy path would
// apply no species filter and return every detection.
func TestApplySpeciesFilter(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// Serialize access to the in-memory database to avoid sqlite connection races.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(&Note{}))
	require.NoError(t, db.Create(&[]Note{
		{ScientificName: "Barbastella barbastellus", CommonName: "Western Barbastelle"},
		{ScientificName: "Myotis daubentonii", CommonName: "Daubenton's Bat"},
		{ScientificName: "Strix aluco", CommonName: "Tawny Owl"},
	}).Error)

	countMatches := func(t *testing.T, filters *SearchFilters) int64 {
		t.Helper()
		var count int64
		q := applySpeciesFilter(db.Model(&Note{}), filters)
		require.NoError(t, q.Count(&count).Error)
		return count
	}

	t.Run("no species filter matches all", func(t *testing.T) {
		assert.Equal(t, int64(3), countMatches(t, &SearchFilters{}))
	})

	t.Run("free-text matches by substring", func(t *testing.T) {
		assert.Equal(t, int64(1), countMatches(t, &SearchFilters{Species: "Tawny"}))
	})

	t.Run("scientific names match exactly, not everything", func(t *testing.T) {
		got := countMatches(t, &SearchFilters{
			SpeciesScientific: []string{"Barbastella barbastellus", "Myotis daubentonii"},
		})
		assert.Equal(t, int64(2), got)
	})

	t.Run("unknown scientific name matches nothing", func(t *testing.T) {
		got := countMatches(t, &SearchFilters{
			SpeciesScientific: []string{"Nonexistent species"},
		})
		assert.Equal(t, int64(0), got)
	})

	t.Run("free-text and scientific names form a union", func(t *testing.T) {
		got := countMatches(t, &SearchFilters{
			Species:           "Tawny",
			SpeciesScientific: []string{"Barbastella barbastellus"},
		})
		assert.Equal(t, int64(2), got)
	})
}
