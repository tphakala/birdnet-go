package labels

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupResolverTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = db.AutoMigrate(
		&entities.Label{},
		&entities.AIModel{},
		&entities.ModelLabel{},
	)
	require.NoError(t, err)

	// Seed BirdNET model
	model := entities.AIModel{
		Name:      "BirdNET",
		Version:   "2.4",
		ModelType: entities.ModelTypeBird,
	}
	require.NoError(t, db.Create(&model).Error)

	return db
}

func TestResolver_Resolve_NewLabel(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)

	label, err := resolver.Resolve(&model, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	assert.NotZero(t, label.ID)
	assert.Equal(t, "Turdus merula", *label.ScientificName)
	assert.Equal(t, entities.LabelTypeSpecies, label.LabelType)

	// Verify model_label mapping created
	var modelLabel entities.ModelLabel
	err = db.Where("model_id = ? AND label_id = ?", model.ID, label.ID).First(&modelLabel).Error
	require.NoError(t, err)
	assert.Equal(t, "Turdus merula_Common Blackbird", modelLabel.RawLabel)
}

func TestResolver_Resolve_ExistingLabel(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)

	// First resolution
	label1, err := resolver.Resolve(&model, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	// Second resolution should return cached/existing
	label2, err := resolver.Resolve(&model, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	assert.Equal(t, label1.ID, label2.ID)

	// Verify only one label exists
	var count int64
	db.Model(&entities.Label{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestResolver_Resolve_SharedLabel_DifferentModels(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	// Create Perch model
	perchModel := entities.AIModel{
		Name:      "Perch",
		Version:   "v2",
		ModelType: entities.ModelTypeMulti,
	}
	require.NoError(t, db.Create(&perchModel).Error)

	var birdnetModel entities.AIModel
	require.NoError(t, db.Where("name = ?", "BirdNET").First(&birdnetModel).Error)

	// Resolve from BirdNET
	label1, err := resolver.Resolve(&birdnetModel, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	// Resolve same species from Perch (different raw format)
	label2, err := resolver.Resolve(&perchModel, "Turdus merula")
	require.NoError(t, err)

	// Should share the same label (matched by scientific name)
	assert.Equal(t, label1.ID, label2.ID)

	// But different model_label mappings
	var mappings []entities.ModelLabel
	db.Where("label_id = ?", label1.ID).Find(&mappings)
	assert.Len(t, mappings, 2)
}

func TestResolver_Resolve_NonSpecies(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)

	label, err := resolver.Resolve(&model, "noise")
	require.NoError(t, err)

	assert.Equal(t, "noise", *label.ScientificName)
	assert.Equal(t, entities.LabelTypeNoise, label.LabelType)
	assert.Nil(t, label.TaxonomicClass)
}

func TestResolver_Resolve_CacheHit(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)

	// First resolve
	label1, err := resolver.Resolve(&model, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	// Clear the cache entry manually by creating a new sync.Map
	// Then add the label back to verify cache is being used
	key := cacheKey{modelID: model.ID, rawLabel: "Turdus merula_Common Blackbird"}
	resolver.cache.Store(key, label1)

	// Second resolve should hit cache
	label2, err := resolver.Resolve(&model, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	assert.Equal(t, label1.ID, label2.ID)
}

func TestResolver_ClearCache(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)

	// Resolve to populate cache
	_, err := resolver.Resolve(&model, "Turdus merula_Common Blackbird")
	require.NoError(t, err)

	// Verify cache has entry
	key := cacheKey{modelID: model.ID, rawLabel: "Turdus merula_Common Blackbird"}
	_, ok := resolver.cache.Load(key)
	assert.True(t, ok, "cache should have entry before clear")

	// Clear cache
	resolver.ClearCache()

	// Verify cache is empty
	_, ok = resolver.cache.Load(key)
	assert.False(t, ok, "cache should be empty after clear")
}

func TestResolver_GetModel_New(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	model, err := resolver.GetModel("TestModel", "1.0", entities.ModelTypeBird)
	require.NoError(t, err)

	assert.NotZero(t, model.ID)
	assert.Equal(t, "TestModel", model.Name)
	assert.Equal(t, "1.0", model.Version)
	assert.Equal(t, entities.ModelTypeBird, model.ModelType)
}

func TestResolver_GetModel_Existing(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	// Get existing BirdNET model
	model, err := resolver.GetModel("BirdNET", "2.4", entities.ModelTypeBird)
	require.NoError(t, err)

	assert.Equal(t, "BirdNET", model.Name)
	assert.Equal(t, "2.4", model.Version)

	// Verify no duplicates created
	var count int64
	db.Model(&entities.AIModel{}).Where("name = ?", "BirdNET").Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestResolver_Resolve_Concurrent(t *testing.T) {
	// Use file-based SQLite with WAL mode for concurrent access (like production)
	tmpDir := t.TempDir()
	dsn := tmpDir + "/test.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = db.AutoMigrate(
		&entities.Label{},
		&entities.AIModel{},
		&entities.ModelLabel{},
	)
	require.NoError(t, err)

	// Seed BirdNET model
	model := entities.AIModel{
		Name:      "BirdNET",
		Version:   "2.4",
		ModelType: entities.ModelTypeBird,
	}
	require.NoError(t, db.Create(&model).Error)

	resolver := NewResolver(db)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	labels := make([]*entities.Label, goroutines)
	errors := make([]error, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			labels[idx], errors[idx] = resolver.Resolve(&model, "Turdus merula_Common Blackbird")
		}(i)
	}

	wg.Wait()

	// All should succeed
	for i, err := range errors {
		require.NoError(t, err, "goroutine %d failed", i)
	}

	// All should return the same label
	firstID := labels[0].ID
	for i, label := range labels {
		assert.Equal(t, firstID, label.ID, "goroutine %d returned different label", i)
	}

	// Verify only one label was created
	var count int64
	db.Model(&entities.Label{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestResolver_Resolve_NonSpecies_Concurrent(t *testing.T) {
	// Use file-based SQLite with WAL mode for concurrent access (like production)
	tmpDir := t.TempDir()
	dsn := tmpDir + "/test.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = db.AutoMigrate(
		&entities.Label{},
		&entities.AIModel{},
		&entities.ModelLabel{},
	)
	require.NoError(t, err)

	// Seed BirdNET model
	model := entities.AIModel{
		Name:      "BirdNET",
		Version:   "2.4",
		ModelType: entities.ModelTypeBird,
	}
	require.NoError(t, db.Create(&model).Error)

	resolver := NewResolver(db)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	labels := make([]*entities.Label, goroutines)
	errors := make([]error, goroutines)

	// Test concurrent resolution of a non-species label ("noise")
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			labels[idx], errors[idx] = resolver.Resolve(&model, "noise")
		}(i)
	}

	wg.Wait()

	// All should succeed
	for i, err := range errors {
		require.NoError(t, err, "goroutine %d failed", i)
	}

	// All should return the same label
	firstID := labels[0].ID
	for i, label := range labels {
		assert.Equal(t, firstID, label.ID, "goroutine %d returned different label", i)
	}

	// Verify only one label was created
	var count int64
	db.Model(&entities.Label{}).Count(&count)
	assert.Equal(t, int64(1), count)

	// Verify the label is of type noise
	var label entities.Label
	require.NoError(t, db.First(&label).Error)
	assert.Equal(t, entities.LabelTypeNoise, label.LabelType)
}

func TestResolver_Resolve_MultipleSpecies(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	var model entities.AIModel
	require.NoError(t, db.First(&model).Error)

	species := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Erithacus rubecula_European Robin",
	}

	for _, s := range species {
		_, err := resolver.Resolve(&model, s)
		require.NoError(t, err)
	}

	// Verify all labels created
	var count int64
	db.Model(&entities.Label{}).Count(&count)
	assert.Equal(t, int64(len(species)), count)

	// Verify all model_labels created
	db.Model(&entities.ModelLabel{}).Count(&count)
	assert.Equal(t, int64(len(species)), count)
}

func TestResolver_Resolve_TaxonomicClass(t *testing.T) {
	db := setupResolverTestDB(t)
	resolver := NewResolver(db)

	// Create bat model
	batModel := entities.AIModel{
		Name:      "BatNET",
		Version:   "1.0",
		ModelType: entities.ModelTypeBat,
	}
	require.NoError(t, db.Create(&batModel).Error)

	var birdModel entities.AIModel
	require.NoError(t, db.Where("name = ?", "BirdNET").First(&birdModel).Error)

	// Resolve bird
	birdLabel, err := resolver.Resolve(&birdModel, "Turdus merula_Common Blackbird")
	require.NoError(t, err)
	assert.Equal(t, "Aves", *birdLabel.TaxonomicClass)

	// Resolve bat
	batLabel, err := resolver.Resolve(&batModel, "Eptesicus nilssonii_Nordfladdermus")
	require.NoError(t, err)
	assert.Equal(t, "Chiroptera", *batLabel.TaxonomicClass)
}
