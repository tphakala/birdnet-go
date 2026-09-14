// species_note_ops_test.go: pins the contract that both species-note stores
// share a single implementation (SpeciesNoteOps) rather than parallel copies.
package datastore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// speciesNoteStore is the full species-note surface both concrete stores expose
// (datastore.DataStore and v2only.Datastore). The guards below fail to compile
// if either the shared ops or the legacy store drifts from it — the v2-only
// store is guarded in its own package, which cannot be imported from here.
type speciesNoteStore interface {
	GetSpeciesNotes(ctx context.Context, scientificName string) ([]SpeciesNote, error)
	GetSpeciesNoteByID(ctx context.Context, id uint) (*SpeciesNote, error)
	SaveSpeciesNote(ctx context.Context, note *SpeciesNote) error
	UpdateSpeciesNote(ctx context.Context, noteID, entry string) error
	DeleteSpeciesNote(ctx context.Context, noteID string) error
}

var (
	_ speciesNoteStore = SpeciesNoteOps{}
	_ speciesNoteStore = (*DataStore)(nil)
)

// TestSpeciesNoteOps_DBErrorCarriesContext pins the telemetry context on the
// read path. The two stores had already drifted here before they were unified:
// only one of them attached the scientific name to the failure.
func TestSpeciesNoteOps_DBErrorCarriesContext(t *testing.T) {
	t.Parallel()

	// A database with no species_notes table: every query fails at the driver.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = NewSpeciesNoteOps(db, nil).GetSpeciesNotes(t.Context(), "  Turdus merula  ")
	require.Error(t, err)

	var enhanced *errors.EnhancedError
	require.True(t, errors.As(err, &enhanced), "database failures must be enhanced errors")
	ctxFields := enhanced.GetContext()
	assert.Equal(t, "get_species_notes", ctxFields["operation"])
	assert.Equal(t, "species_notes", ctxFields["table"])
	assert.Equal(t, "Turdus merula", ctxFields["scientific_name"],
		"the normalized species name belongs on the failure, not the raw input")
}

// TestSpeciesNoteOps_NilMetricsAccepted pins that a store with metrics disabled
// can bind the ops: RetryOnLock takes a nil *Metrics, and the v2-only store's
// getMetrics returns nil until metrics are registered.
func TestSpeciesNoteOps_NilMetricsAccepted(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ops := NewSpeciesNoteOps(ds.DB, nil)
	ctx := t.Context()

	note := &SpeciesNote{ScientificName: "Erithacus rubecula", Entry: "ticking alarm call"}
	require.NoError(t, ops.SaveSpeciesNote(ctx, note))
	require.NoError(t, ops.DeleteSpeciesNote(ctx, idString(note.ID)))
}
