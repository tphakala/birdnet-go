// mock_interface.go - Mock implementation of diskmanager.Interface using testify/mock
package mock_diskmanager

import (
	"github.com/stretchr/testify/mock"
)

// MockInterface is a mock implementation of the diskmanager.Interface for testing
// This uses testify/mock for a more flexible and test-friendly mocking approach
//
// Note: Cannot add compile-time interface assertion (var _ diskmanager.Interface = (*MockInterface)(nil))
// as it would create an import cycle: mocks -> diskmanager -> mocks (via tests)
type MockInterface struct {
	mock.Mock
}

// GetLockedNotesClipPaths mocks the GetLockedNotesClipPaths method
func (m *MockInterface) GetLockedNotesClipPaths() ([]string, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	// Safe type assertion with ok check to avoid panics on misconfigured returns
	if paths, ok := args.Get(0).([]string); ok {
		return paths, args.Error(1)
	}
	return nil, args.Error(1)
}
