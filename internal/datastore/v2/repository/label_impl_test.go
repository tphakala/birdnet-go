package repository

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// setupLabelTestDB creates an in-memory (file-backed temp) SQLite DB with the
// labels and label_types tables migrated.
func setupLabelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "label_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err, "failed to open label test database")

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, db.AutoMigrate(
		&entities.LabelType{},
		&entities.AIModel{},
		&entities.TaxonomicClass{},
		&entities.Label{},
	), "label test DB migration failed")

	return db
}

// TestUpdateLabelType_ChangesLabelTypeID verifies that UpdateLabelType persists
// a new label_type_id on the given row.
func TestUpdateLabelType_ChangesLabelTypeID(t *testing.T) {
	t.Parallel()

	db := setupLabelTestDB(t)
	repo := NewLabelRepository(db, nil, false, false)
	ctx := t.Context()

	// Seed two label types.
	ltA := entities.LabelType{Name: "species"}
	ltB := entities.LabelType{Name: "human"}
	require.NoError(t, db.Create(&ltA).Error)
	require.NoError(t, db.Create(&ltB).Error)
	require.NotZero(t, ltA.ID)
	require.NotZero(t, ltB.ID)

	// Seed a model (required FK for labels).
	model := entities.AIModel{Name: "BirdNET", Version: "2.4", Variant: "default", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)

	// Create a label with type A.
	label, err := repo.GetOrCreate(ctx, "Parus major", model.ID, ltA.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, ltA.ID, label.LabelTypeID)

	// Update to type B.
	require.NoError(t, repo.UpdateLabelType(ctx, label.ID, ltB.ID))

	// Re-fetch and assert.
	updated, err := repo.GetByID(ctx, label.ID)
	require.NoError(t, err)
	assert.Equal(t, ltB.ID, updated.LabelTypeID, "label_type_id should have been updated to typeB")
}

// TestUpdateLabelType_NonExistentIDIsNotAnError documents the chosen behavior:
// GORM's Update on a non-existent row does not return an error (zero rows affected
// is not treated as an error condition). Callers that need to detect missing rows
// must check RowsAffected separately.
func TestUpdateLabelType_NonExistentIDIsNotAnError(t *testing.T) {
	t.Parallel()

	db := setupLabelTestDB(t)
	repo := NewLabelRepository(db, nil, false, false)
	ctx := t.Context()

	// Calling UpdateLabelType on a row that does not exist should not error.
	err := repo.UpdateLabelType(ctx, 999999, 1)
	assert.NoError(t, err, "UpdateLabelType on non-existent id should not error")
}

// TestBatchGetOrCreate_SQLiteKeepsCaseVariantsDistinct verifies that SQLite,
// which compares names case-sensitively, stores a case variant as its own row
// and keys the result by the requested spelling.
func TestBatchGetOrCreate_SQLiteKeepsCaseVariantsDistinct(t *testing.T) {
	t.Parallel()

	db := setupLabelTestDB(t)
	repo := NewLabelRepository(db, nil, false, false)
	ctx := t.Context()

	lt := entities.LabelType{Name: "species"}
	require.NoError(t, db.Create(&lt).Error)
	model := entities.AIModel{Name: "BirdNET", Version: "2.4", Variant: "default", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)

	got, err := repo.BatchGetOrCreate(ctx, []string{"Homo Sapiens", "Homo sapiens"}, model.ID, lt.ID, nil)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.NotEqual(t, got["Homo Sapiens"].ID, got["Homo sapiens"].ID, "a case variant is a distinct row on SQLite")
	assert.Equal(t, "Homo sapiens", got["Homo sapiens"].ScientificName)
}
