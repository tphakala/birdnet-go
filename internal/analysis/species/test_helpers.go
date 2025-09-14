package species

import (
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// MockSpeciesDatastore implements the SpeciesDatastore interface using testify/mock
type MockSpeciesDatastore struct {
	mock.Mock
}

// GetNewSpeciesDetections implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	args := m.Called(startDate, endDate, limit, offset)
	return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
}

// GetSpeciesFirstDetectionInPeriod implements the SpeciesDatastore interface method using testify/mock
func (m *MockSpeciesDatastore) GetSpeciesFirstDetectionInPeriod(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	args := m.Called(startDate, endDate, limit, offset)
	return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
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
