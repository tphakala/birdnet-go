// mock_interface.go - Mock implementation of diskmanager.Interface using testify/mock
package mock_diskmanager

import (
	"github.com/stretchr/testify/mock"
)

// MockInterface is a mock implementation of the diskmanager.Interface for testing
// This uses testify/mock for a more flexible and test-friendly mocking approach
type MockInterface struct {
	mock.Mock
}

// GetLockedNotesClipPaths mocks the GetLockedNotesClipPaths method
func (m *MockInterface) GetLockedNotesClipPaths() ([]string, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}
