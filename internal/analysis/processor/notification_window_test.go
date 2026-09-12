package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestDatabaseAction_NewSpeciesWindow covers #4013 through the real save and
// event-publishing path, including a reload of historical data from SQLite.
func TestDatabaseAction_NewSpeciesWindow(t *testing.T) {
	location, err := time.LoadLocation("Europe/Amsterdam")
	require.NoError(t, err)

	reference := time.Now().In(location).AddDate(0, 0, -7)
	tests := []struct {
		name  string
		first time.Time
	}{
		{"audio_crosses_midnight", time.Date(reference.Year(), reference.Month(), reference.Day(), 23, 59, 50, 0, location)},
		{"midnight", time.Date(2025, 9, 5, 0, 0, 0, 0, location)},
		{"morning", time.Date(2025, 9, 5, 7, 3, 0, 0, location)},
		{"late_evening", time.Date(2025, 9, 5, 23, 59, 0, 0, location)},
		{"spring_clock_change", time.Date(2025, 3, 24, 0, 30, 0, 0, location)},
		// After the autumn clock change, 168 elapsed hours can still fall on
		// calendar day 6. Merely replacing <= with < does not fix that case.
		{"autumn_clock_change", time.Date(2025, 10, 20, 0, 30, 0, 0, location)},
	}
	for _, tt := range tests {
		for _, restart := range []bool{false, true} {
			name := tt.name + "/running"
			if restart {
				name = tt.name + "/restarted"
			}
			t.Run(name, func(t *testing.T) {
				// The event bus is global, so these subtests must run sequentially.
				consumer := setupEventBusWithConsumer(t)
				ds := setupIntegrationTestDB(t)
				sqlDB, err := ds.DB.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
				require.NoError(t, ds.DB.AutoMigrate(&datastore.NotificationHistory{}))

				settings := &conf.SpeciesTrackingSettings{
					Enabled:                      true,
					NewSpeciesWindowDays:         conf.DefaultNewSpeciesWindowDays,
					NotificationSuppressionHours: conf.DefaultNotificationSuppressionHours,
				}
				tracker := species.NewTrackerFromSettings(ds, settings)
				t.Cleanup(func() { assert.NoError(t, tracker.Close()) })
				det := testDetectionWithSpecies("Great Tit", "Parus major", 0.95)
				store := &datastore.SQLiteStore{}
				store.DB = ds.DB
				action := &DatabaseAction{
					Settings:          &conf.Settings{},
					Repo:              datastore.NewDetectionRepository(store, location),
					Result:            det.Result,
					EventTracker:      NewEventTracker(0),
					NewSpeciesTracker: tracker,
				}
				action.Result.Timestamp = tt.first.Add(15 * time.Second)
				action.Result.BeginTime = tt.first
				action.Result.EndTime = tt.first.Add(15 * time.Second)
				require.NoError(t, action.Execute(t.Context(), nil))
				// Wait for notification history persistence before reading/reloading it.
				require.NoError(t, tracker.Close())
				history, err := ds.GetNotificationHistory(t.Context(), "Parus major", "new_species")
				require.NoError(t, err)
				require.True(t, history.LastSent.Equal(tt.first))

				if restart {
					tracker = species.NewTrackerFromSettings(ds, settings)
					require.NoError(t, tracker.InitFromDatabase())
					action.NewSpeciesTracker = tracker
				}

				repeat := tt.first.Add(time.Duration(conf.DefaultNotificationSuppressionHours) * time.Hour)
				action.Result.Timestamp = repeat.Add(15 * time.Second)
				action.Result.BeginTime = repeat
				action.Result.EndTime = repeat.Add(15 * time.Second)
				require.NoError(t, action.Execute(t.Context(), nil))
				require.NoError(t, tracker.Close())

				require.Eventually(t, func() bool {
					return len(consumer.GetReceivedEvents()) == 2
				}, 2*time.Second, 10*time.Millisecond)
				received := consumer.GetReceivedEvents()
				assert.True(t, received[0].IsNewSpecies(), "first detection must still notify")
				assert.False(t, received[1].IsNewSpecies(), "expired suppression must not produce a second new-species event")
				assert.Equal(t, "Parus major", received[1].GetScientificName())

				var count int64
				require.NoError(t, ds.DB.Model(&datastore.Note{}).Count(&count).Error)
				assert.Equal(t, int64(2), count, "ordinary detections must still be saved")
				history, err = ds.GetNotificationHistory(t.Context(), "Parus major", "new_species")
				require.NoError(t, err)
				assert.True(t, history.LastSent.Equal(tt.first), "duplicate must not overwrite notification history")

				status := tracker.GetSpeciesStatus("Parus major", repeat)
				assert.True(t, status.IsNew, "the calendar-day badge window is independent of notification eligibility")
			})
		}
	}
}
