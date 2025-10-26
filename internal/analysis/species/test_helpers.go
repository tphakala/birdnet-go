package species

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
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
