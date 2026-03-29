package tflite

import (
	"runtime"

	"github.com/tphakala/birdnet-go/internal/cpuspec"
)

// determineThreadCount calculates the appropriate number of threads based on
// the configured value and system capabilities.
func determineThreadCount(configuredThreads int) int {
	systemCPUCount := runtime.NumCPU()

	if configuredThreads == 0 {
		spec := cpuspec.GetCPUSpec()
		optimalThreads := spec.GetOptimalThreadCount()
		if optimalThreads > 0 {
			return min(optimalThreads, systemCPUCount)
		}
		return systemCPUCount
	}

	if configuredThreads > systemCPUCount {
		return systemCPUCount
	}

	return configuredThreads
}
