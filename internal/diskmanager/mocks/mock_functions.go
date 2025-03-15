// mock_functions.go - mock functions for testing
package mock_diskmanager

import (
	"github.com/tphakala/birdnet-go/internal/conf"
)

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
