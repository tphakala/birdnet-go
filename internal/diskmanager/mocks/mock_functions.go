// mock_functions.go - mock functions for testing
package mock_diskmanager

import (
	"github.com/tphakala/birdnet-go/internal/conf"
)

// MockFunctions contains all mock functions for testing
// This struct-based approach prevents shared state issues when tests run concurrently
type MockFunctions struct {
	// Mock function implementations
	GetDiskUsage         func(path string) (float64, error)
	GetAudioFiles        func(baseDir string, allowedExts []string, db interface{}, debug bool) ([]interface{}, error)
	Setting              func() *conf.Settings
	ParseRetentionPeriod func(period string) (int, error)
	ParsePercentage      func(percentage string) (float64, error)
	OsRemove             func(name string) error
}

// NewMockFunctions creates a new instance of MockFunctions with default implementations
func NewMockFunctions() *MockFunctions {
	return &MockFunctions{
		GetDiskUsage: func(path string) (float64, error) {
			return 0.0, nil
		},
		GetAudioFiles: func(baseDir string, allowedExts []string, db interface{}, debug bool) ([]interface{}, error) {
			return []interface{}{}, nil
		},
		Setting: func() *conf.Settings {
			return &conf.Settings{}
		},
		ParseRetentionPeriod: func(period string) (int, error) {
			return 0, nil
		},
		ParsePercentage: func(percentage string) (float64, error) {
			return 0.0, nil
		},
		OsRemove: func(name string) error {
			return nil
		},
	}
}

// For backward compatibility with existing tests
// These can be gradually phased out as tests are updated

// MockGetDiskUsage is a mock for the GetDiskUsage function
var MockGetDiskUsage func(path string) (float64, error)

// MockGetAudioFiles is a mock for the GetAudioFiles function
var MockGetAudioFiles func(baseDir string, allowedExts []string, db interface{}, debug bool) ([]interface{}, error)

// MockSetting is a mock for the conf.Setting function
var MockSetting func() *conf.Settings

// MockParseRetentionPeriod is a mock for the conf.ParseRetentionPeriod function
var MockParseRetentionPeriod func(period string) (int, error)

// MockParsePercentage is a mock for the conf.ParsePercentage function
var MockParsePercentage func(percentage string) (float64, error)

// MockOsRemove is a mock for the os.Remove function
var MockOsRemove func(name string) error
