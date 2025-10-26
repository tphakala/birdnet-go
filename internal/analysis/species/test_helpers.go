package species

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// MockSpeciesDatastore implements the SpeciesDatastore interface using testify/mock
type MockSpeciesDatastore struct {
	mock.Mock
}

// GetNewSpeciesDetections implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	args := m.Called(ctx, startDate, endDate, limit, offset)
	return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
}

// GetSpeciesFirstDetectionInPeriod implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	args := m.Called(ctx, startDate, endDate, limit, offset)
	return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
}

// BG-17 fix: Add notification history methods
// GetActiveNotificationHistory implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
	args := m.Called(after)
	return safeSlice[datastore.NotificationHistory](args, 0), args.Error(1)
}

// SaveNotificationHistory implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) SaveNotificationHistory(history *datastore.NotificationHistory) error {
	args := m.Called(history)
	return args.Error(0)
}

// DeleteExpiredNotificationHistory implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) DeleteExpiredNotificationHistory(before time.Time) (int64, error) {
	args := m.Called(before)
	return args.Get(0).(int64), args.Error(1)
}

// safeSlice is a helper for mock methods returning slices.
// It safely handles nil arguments and performs type assertion.
// TODO: Move to a shared test utilities package to reduce duplication
func safeSlice[T any](args mock.Arguments, index int) []T {
	if arg := args.Get(index); arg != nil {
		if slice, ok := arg.([]T); ok {
			return slice
		}
		panic("safeSlice: type assertion failed")
	}
	return nil
}

// createTestTrackerWithMocks creates a SpeciesTracker with all necessary mock expectations set up.
// This consolidates duplicate test setup code and reduces duplication.
// BG-17: Includes notification history mocks required by InitFromDatabase.
func createTestTrackerWithMocks(t *testing.T, settings *conf.SpeciesTrackingSettings) (*SpeciesTracker, *MockSpeciesDatastore) {
	t.Helper()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	return tracker, ds
}
