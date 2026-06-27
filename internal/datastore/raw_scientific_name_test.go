package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/detection"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newRawSciTestDB opens an in-memory SQLite database with the notes schema
// migrated. The single-connection pool avoids races against the shared
// ":memory:" database.
func newRawSciTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&Note{}))
	return db
}

// TestNote_RawScientificName_SchemaAndRoundTrip verifies that AutoMigrate adds the
// raw_scientific_name column and that the value round-trips through the legacy
// notes table. The column preserves the exact scientific name a model emitted
// before canonical-name normalization collapsed it into ScientificName.
func TestNote_RawScientificName_SchemaAndRoundTrip(t *testing.T) {
	t.Parallel()

	db := newRawSciTestDB(t)

	assert.True(t, db.Migrator().HasColumn(&Note{}, "raw_scientific_name"),
		"AutoMigrate should add the raw_scientific_name column")

	// Aliased detection: scientific_name holds the canonical name; raw preserves the legacy one.
	aliased := Note{
		ScientificName:    "Spilopelia senegalensis",
		RawScientificName: "Streptopelia senegalensis",
		CommonName:        "Laughing Dove",
	}
	require.NoError(t, db.Create(&aliased).Error)

	var got Note
	require.NoError(t, db.First(&got, aliased.ID).Error)
	assert.Equal(t, "Spilopelia senegalensis", got.ScientificName)
	assert.Equal(t, "Streptopelia senegalensis", got.RawScientificName)
}

// TestNoteFromResult_ThreadsRawScientificName verifies the raw scientific name
// carried on detection.Species is threaded onto the persisted Note.
func TestNoteFromResult_ThreadsRawScientificName(t *testing.T) {
	t.Parallel()

	result := detection.Result{
		Species: detection.Species{
			ScientificName:    "Spilopelia senegalensis",
			RawScientificName: "Streptopelia senegalensis",
			CommonName:        "Laughing Dove",
		},
	}

	note := NoteFromResult(&result)
	assert.Equal(t, "Spilopelia senegalensis", note.ScientificName)
	assert.Equal(t, "Streptopelia senegalensis", note.RawScientificName)
}

// TestNote_RawScientificName_NullReadsAsEmpty verifies that a pre-existing row
// (written before the column existed, i.e. NULL) is still readable and reads back
// as an empty string. NULL/empty means "raw equals scientific_name".
func TestNote_RawScientificName_NullReadsAsEmpty(t *testing.T) {
	t.Parallel()

	db := newRawSciTestDB(t)

	// Simulate a pre-existing row with a NULL raw_scientific_name by inserting
	// without that column.
	require.NoError(t, db.Exec(
		`INSERT INTO notes (scientific_name, common_name) VALUES (?, ?)`,
		"Turdus merula", "Eurasian Blackbird").Error)

	var got Note
	require.NoError(t, db.Where("scientific_name = ?", "Turdus merula").First(&got).Error)
	assert.Equal(t, "Turdus merula", got.ScientificName)
	assert.Empty(t, got.RawScientificName, "NULL raw_scientific_name must read back as empty")
}
