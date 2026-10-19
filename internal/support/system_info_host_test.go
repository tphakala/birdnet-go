package support

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// TestCollectSystemInfo_HostFacts checks that the host facts are wired into
// collectSystemInfo, since the readers being correct alone is not what the dump
// depends on. The CPU readers are best effort, so a field is only compared when
// its reader has data on this host.
func TestCollectSystemInfo_HostFacts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("host fact probes are only asserted on Linux")
	}

	info := (&Collector{}).collectSystemInfo()

	assert.NotEmpty(t, info.KernelVersion)
	if flags := sysinfo.GetCPUFlags(); len(flags) > 0 {
		assert.Equal(t, flags, info.CPUFlags)
	}
	if model := sysinfo.GetCPUModel(); model != "" {
		assert.Equal(t, model, info.CPUModel)
	}
}
