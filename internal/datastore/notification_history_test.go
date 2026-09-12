// notification_history_test.go: Unit tests for notification history database operations
package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupNotificationHistoryTestDB creates an in-memory SQLite database for testing.
func setupNotificationHistoryTestDB(t *testing.T) *DataStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "Failed to create test database")

	err = db.AutoMigrate(&NotificationHistory{})
	require.NoError(t, err, "Failed to migrate schema")

	return &DataStore{DB: db}
}

// TestGetActiveNotificationHistoryByType verifies that the query is scoped to
// a single notification type, so a consumer tracking "lifer" suppression
// state on restart does not load unrelated "new_species" records, and vice
// versa (see internal/analysis/species.SpeciesTracker's
// liferNotificationLastSent doc comment for why the two must stay isolated).
func TestGetActiveNotificationHistoryByType(t *testing.T) {
	ds := setupNotificationHistoryTestDB(t)
	ctx := t.Context()
	now := time.Now()

	histories := []*NotificationHistory{
		{
			ScientificName:   "turdus merula",
			NotificationType: "new_species",
			LastSent:         now,
			ExpiresAt:        now.Add(time.Hour),
		},
		{
			ScientificName:   "parus major",
			NotificationType: "lifer",
			LastSent:         now,
			ExpiresAt:        now.Add(time.Hour),
		},
		{
			ScientificName:   "corvus corax",
			NotificationType: "lifer",
			LastSent:         now,
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	for _, h := range histories {
		require.NoError(t, ds.SaveNotificationHistory(ctx, h))
	}

	t.Run("returns only the requested type", func(t *testing.T) {
		lifers, err := ds.GetActiveNotificationHistoryByType(ctx, "lifer", now.Add(-time.Hour))
		require.NoError(t, err)
		require.Len(t, lifers, 2)
		for _, h := range lifers {
			assert.Equal(t, "lifer", h.NotificationType)
		}

		newSpecies, err := ds.GetActiveNotificationHistoryByType(ctx, "new_species", now.Add(-time.Hour))
		require.NoError(t, err)
		require.Len(t, newSpecies, 1)
		assert.Equal(t, "turdus merula", newSpecies[0].ScientificName)
	})

	t.Run("respects the after cutoff", func(t *testing.T) {
		lifers, err := ds.GetActiveNotificationHistoryByType(ctx, "lifer", now.Add(time.Hour))
		require.NoError(t, err)
		assert.Empty(t, lifers, "records sent before the cutoff should be excluded")
	})

	t.Run("unknown type returns empty", func(t *testing.T) {
		result, err := ds.GetActiveNotificationHistoryByType(ctx, "does_not_exist", now.Add(-time.Hour))
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}
