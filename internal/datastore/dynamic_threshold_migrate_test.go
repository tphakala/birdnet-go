package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// legacyDynamicThresholdEntity mirrors the pre-migration DynamicThreshold schema
// (with model_name and the composite unique index). Migrating it with GORM
// reproduces exactly the on-disk schema production shipped, so the migration test
// exercises a realistic starting point rather than a hand-rolled table.
type legacyDynamicThresholdEntity struct {
	ID             uint      `gorm:"primaryKey"`
	SpeciesName    string    `gorm:"uniqueIndex:idx_dt_species_model;not null;size:200"`
	ModelName      string    `gorm:"uniqueIndex:idx_dt_species_model;not null;size:100;default:'BirdNET'"`
	ScientificName string    `gorm:"size:200"`
	Level          int       `gorm:"not null;default:0"`
	CurrentValue   float64   `gorm:"not null"`
	BaseThreshold  float64   `gorm:"not null"`
	HighConfCount  int       `gorm:"not null;default:0"`
	ValidHours     int       `gorm:"not null"`
	ExpiresAt      time.Time `gorm:"index;not null"`
	LastTriggered  time.Time `gorm:"index;not null"`
	FirstCreated   time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
	TriggerCount   int       `gorm:"not null;default:0"`
}

func (legacyDynamicThresholdEntity) TableName() string { return "dynamic_thresholds" }

// createLegacyDynamicThresholdTable builds the pre-migration dynamic_thresholds
// table via GORM AutoMigrate, matching how production created it.
func createLegacyDynamicThresholdTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&legacyDynamicThresholdEntity{}))
}

// insertLegacyRow inserts one per-model dynamic threshold row into the legacy table.
func insertLegacyRow(t *testing.T, db *gorm.DB, species, model string, level, highConf, triggerCount int, expiresAt time.Time) {
	t.Helper()
	now := time.Now()
	require.NoError(t, db.Create(&legacyDynamicThresholdEntity{
		SpeciesName:    species,
		ModelName:      model,
		ScientificName: "Sci " + species,
		Level:          level,
		CurrentValue:   0.5,
		BaseThreshold:  0.5,
		HighConfCount:  highConf,
		ValidHours:     24,
		ExpiresAt:      expiresAt,
		LastTriggered:  now,
		FirstCreated:   now,
		UpdatedAt:      now,
		TriggerCount:   triggerCount,
	}).Error)
}

func TestMigrateDynamicThresholdsToPerSpecies_Consolidates(t *testing.T) {
	t.Parallel()
	db := openSQLiteTestDB(t)
	createLegacyDynamicThresholdTable(t, db)

	base := time.Now()
	// Three per-model rows for one species with differing levels/expiries/counters.
	// The highest counters (9/7) live on a NON-winning (level-1) row and the latest
	// expiry on another non-winner, so the assertions below fail unless the max-merge
	// actually runs across the whole group rather than just copying the winner's fields.
	insertLegacyRow(t, db, "pajulintu", "BirdNET_V2.4", 1, 9, 7, base.Add(1*time.Hour))
	insertLegacyRow(t, db, "pajulintu", "BirdNET_V3.0", 3, 5, 5, base.Add(2*time.Hour))
	insertLegacyRow(t, db, "pajulintu", "Perch_V2", 1, 1, 1, base.Add(3*time.Hour))
	// A species with a single row is left as-is.
	insertLegacyRow(t, db, "tikli", "BirdNET_V2.4", 2, 4, 4, base.Add(1*time.Hour))

	require.NoError(t, migrateDynamicThresholdsToPerSpecies(db, GetLogger()))

	// AutoMigrate to the new schema must now succeed (no duplicates, creates the
	// single-column unique index).
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))

	// One row per species.
	var all []DynamicThreshold
	require.NoError(t, db.Order("species_name ASC").Find(&all).Error)
	require.Len(t, all, 2, "should consolidate to one row per species")

	bySpecies := map[string]DynamicThreshold{}
	for _, r := range all {
		bySpecies[r.SpeciesName] = r
	}

	// Winner is the highest-level row (level 3), but counters merge via max across the
	// whole group (9/7 from the level-1 row, not the winner's 5/5) and expiry is the
	// latest (+3h from a third row), proving the merge is independent of winner selection.
	paju := bySpecies["pajulintu"]
	assert.Equal(t, 3, paju.Level, "highest level wins")
	assert.Equal(t, 9, paju.HighConfCount, "high-conf count merged via max from a non-winning row")
	assert.Equal(t, 7, paju.TriggerCount, "trigger count merged via max from a non-winning row")
	assert.WithinDuration(t, base.Add(3*time.Hour), paju.ExpiresAt, time.Second, "latest expiry wins")

	assert.Equal(t, 2, bySpecies["tikli"].Level, "single-row species is preserved")

	// The model_name column and composite index are gone; the new unique index exists.
	assert.False(t, db.Migrator().HasColumn(&DynamicThreshold{}, "model_name"),
		"model_name column should be dropped")
	indexes := sqliteIndexNames(t, db, "dynamic_thresholds")
	assert.NotContains(t, indexes, "idx_dt_species_model", "composite index should be dropped")
	assert.Contains(t, indexes, "idx_dynamic_thresholds_species_name", "new unique index should exist")

	// The unique index enforces one row per species: re-inserting updates, not duplicates.
	require.NoError(t, db.Exec(`INSERT INTO dynamic_thresholds
		(species_name, scientific_name, level, current_value, base_threshold, high_conf_count,
		 valid_hours, expires_at, last_triggered, first_created, updated_at, trigger_count)
		VALUES ('newbird','Sci new',1,0.5,0.5,1,24,?,?,?,?,1)`,
		base, base, base, base).Error)
	require.Error(t, db.Exec(`INSERT INTO dynamic_thresholds
		(species_name, scientific_name, level, current_value, base_threshold, high_conf_count,
		 valid_hours, expires_at, last_triggered, first_created, updated_at, trigger_count)
		VALUES ('newbird','Sci new',2,0.4,0.5,2,24,?,?,?,?,2)`,
		base, base, base, base).Error, "duplicate species insert must violate the unique index")
}

func TestMigrateDynamicThresholdsToPerSpecies_NormalizesSingleMixedCaseRow(t *testing.T) {
	t.Parallel()
	db := openSQLiteTestDB(t)
	createLegacyDynamicThresholdTable(t, db)

	// A lone legacy row with a mixed-case species name (no other row for the taxon).
	insertLegacyRow(t, db, "American Crow", "BirdNET_V2.4", 2, 3, 3, time.Now().Add(time.Hour))

	require.NoError(t, migrateDynamicThresholdsToPerSpecies(db, GetLogger()))
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))

	var all []DynamicThreshold
	require.NoError(t, db.Find(&all).Error)
	require.Len(t, all, 1)
	assert.Equal(t, "american crow", all[0].SpeciesName,
		"a single mixed-case legacy row must be lowercased so it matches processor lookups")
	assert.Equal(t, 2, all[0].Level, "learned state is preserved during normalization")
}

func TestMigrateDynamicThresholdsToPerSpecies_Idempotent(t *testing.T) {
	t.Parallel()
	db := openSQLiteTestDB(t)
	createLegacyDynamicThresholdTable(t, db)

	base := time.Now()
	insertLegacyRow(t, db, "pajulintu", "BirdNET_V2.4", 1, 2, 2, base.Add(1*time.Hour))
	insertLegacyRow(t, db, "pajulintu", "Perch_V2", 2, 3, 3, base.Add(2*time.Hour))

	require.NoError(t, migrateDynamicThresholdsToPerSpecies(db, GetLogger()))
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))

	var afterFirst int64
	require.NoError(t, db.Model(&DynamicThreshold{}).Count(&afterFirst).Error)
	assert.Equal(t, int64(1), afterFirst)

	// A second run is a no-op (model_name column already gone).
	require.NoError(t, migrateDynamicThresholdsToPerSpecies(db, GetLogger()))

	var afterSecond int64
	require.NoError(t, db.Model(&DynamicThreshold{}).Count(&afterSecond).Error)
	assert.Equal(t, afterFirst, afterSecond, "second migration run must be a no-op")
}

func TestMigrateDynamicThresholdsToPerSpecies_FreshInstall(t *testing.T) {
	t.Parallel()
	db := openSQLiteTestDB(t)

	// No dynamic_thresholds table exists yet: migration is a no-op, not an error.
	require.NoError(t, migrateDynamicThresholdsToPerSpecies(db, GetLogger()))

	// A fresh AutoMigrate then creates the per-species schema cleanly.
	require.NoError(t, db.AutoMigrate(&DynamicThreshold{}))
	assert.False(t, db.Migrator().HasColumn(&DynamicThreshold{}, "model_name"))
}
