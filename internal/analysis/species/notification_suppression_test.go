package species

import (
	"context"
	"testing"
	"time"

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
func (m *mockSpeciesDatastore) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
	return []datastore.NotificationHistory{}, nil
}

func (m *mockSpeciesDatastore) SaveNotificationHistory(history *datastore.NotificationHistory) error {
	return nil
}

func (m *mockSpeciesDatastore) DeleteExpiredNotificationHistory(before time.Time) (int64, error) {
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
	if tracker.ShouldSuppressNotification(species1, now) {
		t.Errorf("First notification should not be suppressed")
	}

	// Record that notification was sent
	tracker.RecordNotificationSent(species1, now)

	// Test 2: Immediate second notification SHOULD be suppressed
	if !tracker.ShouldSuppressNotification(species1, now.Add(1*time.Minute)) {
		t.Errorf("Second notification within suppression window should be suppressed")
	}

	// Test 3: Different species should NOT be suppressed
	if tracker.ShouldSuppressNotification(species2, now) {
		t.Errorf("Different species should not be suppressed")
	}

	// Record notification for species2
	tracker.RecordNotificationSent(species2, now)

	// Test 4: After suppression window, notification should NOT be suppressed
	futureTime := now.Add(2 * time.Hour) // Beyond 1 hour suppression window
	if tracker.ShouldSuppressNotification(species1, futureTime) {
		t.Errorf("Notification after suppression window should not be suppressed")
	}

	// Test 5: Cleanup should remove old records
	// Add an old record using the public API
	oldTime := now.Add(-3 * time.Hour)
	tracker.RecordNotificationSent("Old Species", oldTime)

	// Run cleanup
	cleaned := tracker.CleanupOldNotificationRecords(now)
	if cleaned != 1 {
		t.Errorf("Expected to clean 1 old record, got %d", cleaned)
	}

	// Old species should not suppress (because it was cleaned up)
	if tracker.ShouldSuppressNotification("Old Species", now) {
		t.Errorf("Old species should have been cleaned up and not suppress")
	}

	// Recent notifications should still be tracked
	if !tracker.ShouldSuppressNotification(species1, now.Add(30*time.Minute)) {
		t.Errorf("Recent notification should still suppress within window")
	}
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
		for i := 0; i < 100; i++ {
			tracker.RecordNotificationSent("Species1", now)
			tracker.RecordNotificationSent("Species2", now)
		}
		done <- true
	}()

	// Goroutine 2: Check suppression
	go func() {
		for i := 0; i < 100; i++ {
			tracker.ShouldSuppressNotification("Species1", now)
			tracker.ShouldSuppressNotification("Species2", now)
		}
		done <- true
	}()

	// Goroutine 3: Cleanup
	go func() {
		for i := 0; i < 10; i++ {
			tracker.CleanupOldNotificationRecords(now)
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Verify final state is consistent
	if tracker.ShouldSuppressNotification("Species1", now.Add(1*time.Hour)) != true {
		t.Errorf("Species1 should be suppressed after recording")
	}
	if tracker.ShouldSuppressNotification("Species2", now.Add(1*time.Hour)) != true {
		t.Errorf("Species2 should be suppressed after recording")
	}
}
