package species

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// mockSpeciesDatastore implements SpeciesDatastore interface for testing
type mockSpeciesDatastore struct{}

func (m *mockSpeciesDatastore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	return []datastore.NewSpeciesData{}, nil
}

func (m *mockSpeciesDatastore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	return []datastore.NewSpeciesData{}, nil
}

// BG-17 fix: Add notification history methods
func (m *mockSpeciesDatastore) GetActiveNotificationHistory(_ context.Context, after time.Time) ([]datastore.NotificationHistory, error) {
	return []datastore.NotificationHistory{}, nil
}

func (m *mockSpeciesDatastore) GetActiveNotificationHistoryByType(_ context.Context, _ string, _ time.Time) ([]datastore.NotificationHistory, error) {
	return []datastore.NotificationHistory{}, nil
}

func (m *mockSpeciesDatastore) SaveNotificationHistory(_ context.Context, history *datastore.NotificationHistory) error {
	return nil
}

func (m *mockSpeciesDatastore) DeleteExpiredNotificationHistory(_ context.Context, before time.Time) (int64, error) {
	return 0, nil
}

// TestNotificationSuppression tests the simplified notification suppression logic
func TestNotificationSuppression(t *testing.T) {
	// Create a mock datastore
	mockDS := &mockSpeciesDatastore{}

	// Create settings with a short suppression window for testing
	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		NotificationSuppressionHours: 1, // 1 hour suppression window for testing
		SyncIntervalMinutes:          60,
	}

	// Create tracker
	tracker := NewTrackerFromSettings(mockDS, settings)

	// Test species
	species1 := "Cardinalis cardinalis" //nolint:misspell // Scientific name, not a misspelling
	species2 := "Turdus migratorius"
	now := time.Now()

	// Test 1: First notification should NOT be suppressed
	assert.False(t, tracker.ShouldSuppressNotification(species1, now),
		"First notification should not be suppressed")

	// Record that notification was sent
	tracker.RecordNotificationSent(species1, now)

	// Test 2: Immediate second notification SHOULD be suppressed
	assert.True(t, tracker.ShouldSuppressNotification(species1, now.Add(1*time.Minute)),
		"Second notification within suppression window should be suppressed")

	// Test 3: Different species should NOT be suppressed
	assert.False(t, tracker.ShouldSuppressNotification(species2, now),
		"Different species should not be suppressed")

	// Record notification for species2
	tracker.RecordNotificationSent(species2, now)

	// Test 4: After suppression window, notification should NOT be suppressed
	futureTime := now.Add(2 * time.Hour) // Beyond 1 hour suppression window
	assert.False(t, tracker.ShouldSuppressNotification(species1, futureTime),
		"Notification after suppression window should not be suppressed")

	// Test 5: Cleanup should remove old records
	// Add an old record using the public API
	oldTime := now.Add(-3 * time.Hour)
	tracker.RecordNotificationSent("Old Species", oldTime)

	// Run cleanup
	cleaned := tracker.CleanupOldNotificationRecords(now)
	require.Equal(t, 1, cleaned, "Expected to clean 1 old record")

	// Old species should not suppress (because it was cleaned up)
	assert.False(t, tracker.ShouldSuppressNotification("Old Species", now),
		"Old species should have been cleaned up and not suppress")

	// Recent notifications should still be tracked
	assert.True(t, tracker.ShouldSuppressNotification(species1, now.Add(30*time.Minute)),
		"Recent notification should still suppress within window")
}

// TestLiferNotificationSuppression exercises the lifer suppression path
// (ShouldSuppressLiferNotification / RecordLiferNotificationSent): first
// notification not suppressed, immediate repeat suppressed, different
// species not suppressed, and cleanup removes old records. The
// NotificationSuppressionHours setting below is deliberately NOT what governs
// lifer suppression — it uses the fixed liferNotificationSuppressionWindow
// (5 minutes) regardless — see TestLiferNotificationSuppression_FixedWindowIndependentOfSetting
// for a test that isolates that independence explicitly.
func TestLiferNotificationSuppression(t *testing.T) {
	mockDS := &mockSpeciesDatastore{}

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		NotificationSuppressionHours: 1,
		SyncIntervalMinutes:          60,
	}

	tracker := NewTrackerFromSettings(mockDS, settings)

	species1 := "Cardinalis cardinalis" //nolint:misspell // Scientific name, not a misspelling
	species2 := "Turdus migratorius"
	now := time.Now()

	assert.False(t, tracker.ShouldSuppressLiferNotification(species1, now),
		"First lifer notification should not be suppressed")

	tracker.RecordLiferNotificationSent(species1, now)

	assert.True(t, tracker.ShouldSuppressLiferNotification(species1, now.Add(1*time.Minute)),
		"Second lifer notification within suppression window should be suppressed")

	assert.False(t, tracker.ShouldSuppressLiferNotification(species2, now),
		"Different species should not be suppressed")

	futureTime := now.Add(2 * time.Hour)
	assert.False(t, tracker.ShouldSuppressLiferNotification(species1, futureTime),
		"Lifer notification after suppression window should not be suppressed")

	oldTime := now.Add(-3 * time.Hour)
	tracker.RecordLiferNotificationSent("Old Species", oldTime)

	cleaned := tracker.CleanupOldNotificationRecords(now)
	require.Equal(t, 1, cleaned, "Expected to clean 1 old lifer record")

	assert.False(t, tracker.ShouldSuppressLiferNotification("Old Species", now),
		"Old species should have been cleaned up and not suppress")
}

// TestLiferNotificationSuppression_FixedWindowIndependentOfSetting proves
// lifer suppression uses the fixed liferNotificationSuppressionWindow (5
// minutes) rather than the user-configurable NotificationSuppressionHours: a
// generous 30-day new-species window has no bearing on the lifer re-alert
// interval, which stays short so an unresolved lifer keeps reminding the user.
func TestLiferNotificationSuppression_FixedWindowIndependentOfSetting(t *testing.T) {
	mockDS := &mockSpeciesDatastore{}

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		NotificationSuppressionHours: 720, // 30 days — must NOT govern lifer suppression
		SyncIntervalMinutes:          60,
	}

	tracker := NewTrackerFromSettings(mockDS, settings)
	require.Equal(t, 720*time.Hour, tracker.notificationSuppressionWindow,
		"sanity check: new-species window really is the configured 30 days")

	species := "Cardinalis cardinalis" //nolint:misspell // Scientific name, not a misspelling
	now := time.Now()

	tracker.RecordLiferNotificationSent(species, now)

	assert.True(t, tracker.ShouldSuppressLiferNotification(species, now.Add(4*time.Minute)),
		"still within the fixed 5-minute lifer window, should suppress")
	assert.False(t, tracker.ShouldSuppressLiferNotification(species, now.Add(6*time.Minute)),
		"past the fixed 5-minute lifer window, should re-alert — despite a 30-day new-species setting")
}

// TestLiferNotificationSuppression_IndependentOfNewSpecies verifies that
// recording a "new_species" notification and a "lifer" notification for the
// same scientific name maintain independent suppression timers, since a
// species can be new to this install but not a lifer (or vice versa once a
// life list is uploaded) — see liferNotificationLastSent's doc comment.
func TestLiferNotificationSuppression_IndependentOfNewSpecies(t *testing.T) {
	mockDS := &mockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		NotificationSuppressionHours: 1,
		SyncIntervalMinutes:          60,
	}
	tracker := NewTrackerFromSettings(mockDS, settings)

	species := "Cardinalis cardinalis" //nolint:misspell // Scientific name, not a misspelling
	now := time.Now()

	// Recording a new-species notification must not suppress a lifer
	// notification for the same species, and vice versa.
	tracker.RecordNotificationSent(species, now)
	assert.False(t, tracker.ShouldSuppressLiferNotification(species, now),
		"A new-species notification must not suppress an independent lifer notification")

	tracker.RecordLiferNotificationSent(species, now)
	assert.True(t, tracker.ShouldSuppressNotification(species, now),
		"New-species suppression should be unaffected, still suppressed from its own record")
	assert.True(t, tracker.ShouldSuppressLiferNotification(species, now),
		"Lifer notification should now be suppressed from its own record")
}

// TestNotificationSuppressionThreadSafety tests thread safety of notification suppression
func TestNotificationSuppressionThreadSafety(t *testing.T) {
	t.Parallel()

	// Create a mock datastore
	mockDS := &mockSpeciesDatastore{}

	// Create settings
	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		NotificationSuppressionHours: 24,
		SyncIntervalMinutes:          60,
	}

	// Create tracker
	tracker := NewTrackerFromSettings(mockDS, settings)

	// Run concurrent operations
	done := make(chan bool)
	now := time.Now()

	// Goroutine 1: Record notifications
	go func() {
		for range 100 {
			tracker.RecordNotificationSent("Species1", now)
			tracker.RecordNotificationSent("Species2", now)
		}
		done <- true
	}()

	// Goroutine 2: Check suppression
	go func() {
		for range 100 {
			tracker.ShouldSuppressNotification("Species1", now)
			tracker.ShouldSuppressNotification("Species2", now)
		}
		done <- true
	}()

	// Goroutine 3: Cleanup
	go func() {
		for range 10 {
			tracker.CleanupOldNotificationRecords(now)
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Verify final state is consistent
	assert.True(t, tracker.ShouldSuppressNotification("Species1", now.Add(1*time.Hour)),
		"Species1 should be suppressed after recording")
	assert.True(t, tracker.ShouldSuppressNotification("Species2", now.Add(1*time.Hour)),
		"Species2 should be suppressed after recording")
}
