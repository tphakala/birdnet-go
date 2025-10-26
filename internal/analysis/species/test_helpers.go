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
func createTestTrackerWithMocks(t *testing.T, settings *conf.SpeciesTrackingSettings) (*SpeciesTracker, *mocks.MockInterface) {
	t.Helper()

	// Use generated mock instead of manual implementation
	mockDS := mocks.NewMockInterface(t)

	// Set up expectations using the expecter pattern
	mockDS.EXPECT().
		GetNewSpeciesDetections(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).
		Maybe() // Allow this to be called zero or more times

	// BG-17: InitFromDatabase now loads notification history
	mockDS.EXPECT().
		GetActiveNotificationHistory(mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).
		Maybe()

	mockDS.EXPECT().
		GetSpeciesFirstDetectionInPeriod(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).
		Maybe()

	tracker := NewTrackerFromSettings(mockDS, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	return tracker, mockDS
}
