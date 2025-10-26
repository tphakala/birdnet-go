package species

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// createTestTrackerWithMocks creates a SpeciesTracker with all necessary mock expectations set up.
// This consolidates duplicate test setup code and reduces duplication.
// BG-17: Includes notification history mocks required by InitFromDatabase.
// BG-21: Migrated to use auto-generated mocks from mockery.
//
// NOTE: This helper uses .Maybe() for flexibility across different test scenarios.
// For tests that need to enforce deterministic call counts:
//   - GetNewSpeciesDetections: Called .Once() during InitFromDatabase (lifetime tracking)
//   - GetActiveNotificationHistory: Called .Once() if NotificationSuppressionHours > 0
//   - GetSpeciesFirstDetectionInPeriod: Called .Once() per enabled period tracker:
//     * Once for YearlyTracking if enabled
//     * Once for SeasonalTracking if enabled
//   - SaveNotificationHistory: Called asynchronously, use .Maybe()
//   - DeleteExpiredNotificationHistory: Called asynchronously, use .Maybe()
//
// If your test requires strict enforcement of these call counts, set up mocks manually
// instead of using this helper.
func createTestTrackerWithMocks(t *testing.T, settings *conf.SpeciesTrackingSettings) (*SpeciesTracker, *mocks.MockInterface) {
	t.Helper()

	// Use generated mock instead of manual implementation
	mockDS := mocks.NewMockInterface(t)

	// Set up expectations using the expecter pattern with .Maybe() for flexibility
	// This allows the helper to work across different test scenarios
	mockDS.EXPECT().
		GetNewSpeciesDetections(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).
		Maybe() // Flexible - always called but .Maybe() allows helper reuse

	// BG-17: InitFromDatabase now loads notification history
	mockDS.EXPECT().
		GetActiveNotificationHistory(mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).
		Maybe() // Conditional - only called if NotificationSuppressionHours > 0

	mockDS.EXPECT().
		GetSpeciesFirstDetectionInPeriod(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).
		Maybe() // Conditional - called for yearly and/or seasonal tracking

	// BG-17: Notification persistence - async operations called in goroutines
	mockDS.EXPECT().
		SaveNotificationHistory(mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).
		Maybe() // Called asynchronously by RecordNotificationSent

	mockDS.EXPECT().
		DeleteExpiredNotificationHistory(mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).
		Maybe() // Called asynchronously by CleanupOldNotificationRecords

	tracker := NewTrackerFromSettings(mockDS, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	return tracker, mockDS
}
