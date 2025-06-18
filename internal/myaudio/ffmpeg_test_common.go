// Package myaudio provides audio processing functionality
package myaudio

import (
	"github.com/stretchr/testify/mock"
)

// MockCmd mocks an exec.Cmd for testing FFmpeg processes
type MockCmd struct {
	mock.Mock
	pid     int
	process *MockProcess
}

// MockProcess mocks os.Process for testing
type MockProcess struct {
	mock.Mock
	pid int
}

// Kill implements the process kill method
func (p *MockProcess) Kill() error {
	return nil
}
