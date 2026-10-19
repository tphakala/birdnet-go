package support

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCollectSystemInfo_HostFacts checks that the host facts are wired into
// collectSystemInfo, since the readers being correct alone is not what the dump
// depends on.
func TestCollectSystemInfo_HostFacts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("host fact probes are only asserted on Linux")
	}

	info := (&Collector{}).collectSystemInfo()

	assert.NotEmpty(t, info.KernelVersion)
	assert.NotEmpty(t, info.CPUFlags)
	assert.NotEmpty(t, info.CPUModel)
}
