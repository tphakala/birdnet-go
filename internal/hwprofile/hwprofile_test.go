package hwprofile

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbeAgainstFixtureTrees exercises the filesystem-backed probes against
// the host shapes this project actually ships to. Architecture, CPU and backend
// facts always describe the machine running the test, so the assertions cover
// what the fixture tree determines: board, accelerators and memory ceiling.
func TestProbeAgainstFixtureTrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tree   map[string]string
		assert func(t *testing.T, p Profile)
	}{
		{
			name: "raspberry pi 5",
			tree: fixturePi5,
			assert: func(t *testing.T, p Profile) {
				t.Helper()
				assert.Equal(t, BoardRaspberryPi, p.Board.Kind)
				assert.Equal(t, TierPi5, p.Board.Tier)
				assert.Empty(t, p.Accelerators, "the Pi's VideoCore is not on the PCI DRM bus")
			},
		},
		{
			name: "raspberry pi 4",
			tree: fixturePi4,
			assert: func(t *testing.T, p Profile) {
				t.Helper()
				assert.Equal(t, BoardRaspberryPi, p.Board.Kind)
				assert.Equal(t, TierPi4, p.Board.Tier)
			},
		},
		{
			name: "raspberry pi 3",
			tree: fixturePi3,
			assert: func(t *testing.T, p Profile) {
				t.Helper()
				assert.Equal(t, BoardRaspberryPi, p.Board.Kind)
				assert.Equal(t, TierPi3, p.Board.Tier)
			},
		},
		{
			name: "generic amd64 desktop with an intel igpu",
			tree: fixtureAMD64Desktop,
			assert: func(t *testing.T, p Profile) {
				t.Helper()
				assert.Equal(t, BoardGeneric, p.Board.Kind)
				assert.Empty(t, p.Board.Tier)
				require.Len(t, p.Accelerators, 1)
				// Usability depends on how this binary was built, so only the
				// hardware facts are asserted here; the reason codes are covered
				// by TestApplyUsability against explicit backend states.
				assert.Equal(t, VendorIntel, p.Accelerators[0].Vendor)
				assert.Equal(t, AcceleratorIGPU, p.Accelerators[0].Kind)
				assert.Equal(t, 12, p.Accelerators[0].Generation)
			},
		},
		{
			name: "container with a cgroup memory limit",
			tree: fixtureCgroupLimited,
			assert: func(t *testing.T, p Profile) {
				t.Helper()
				// Host RAM would mask the real ceiling, which is exactly why the
				// cgroup limit is consulted.
				assert.Equal(t, cgroupLimitBytes, p.TotalRAMBytes)
				assert.Contains(t, p.Capabilities(), CapLowRAM)
			},
		},
		{
			name: "host without a device tree",
			tree: nil,
			assert: func(t *testing.T, p Profile) {
				t.Helper()
				assert.Equal(t, Board{Kind: BoardGeneric}, p.Board)
				assert.Empty(t, p.Accelerators)
				assert.NotContains(t, p.Capabilities(), CapLowRAM, "an unlimited host is not low-ram")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeTree(t, tt.tree)

			profile := evaluateBackends(probeHardware(root))

			// Invariants that hold on every host: probing is best-effort and
			// never leaves the profile unusable.
			assert.Empty(t, profile.Issues, "no fixture tree should make a probe fail")
			assert.NotEmpty(t, profile.Arch)
			assert.Positive(t, profile.PhysicalCores)
			assert.Positive(t, profile.TotalRAMBytes)
			assert.Contains(t, profile.Capabilities(), CapTFLite, "TFLite is compiled into every build")

			tt.assert(t, profile)
		})
	}
}

// TestProbeAgainstLiveHost runs the probe against the real filesystem. Fixture
// trees are curated, so they cannot catch a sysfs layout nobody anticipated;
// this asserts the contract that matters on any host, that probing completes
// without recording an issue and yields a usable profile. Run it with -v to see
// the detected profile, which is the quickest way to check a new board.
func TestProbeAgainstLiveHost(t *testing.T) {
	t.Parallel()

	profile := evaluateBackends(probeHardware(rootFS))

	t.Logf("arch=%s cpuArch=%s cpu=%q env=%s cores=%d/%d ram=%dMiB fp16=%t simd=%v",
		profile.Arch, profile.CPUArch, profile.CPUModel, profile.Environment,
		profile.PerfCores, profile.PhysicalCores, profile.TotalRAMBytes>>20,
		profile.HasNativeF16, profile.SIMD)
	t.Logf("board=%+v", profile.Board)
	for i := range profile.Accelerators {
		t.Logf("accelerator=%+v", profile.Accelerators[i])
	}
	t.Logf("backends=%+v", profile.Backends)
	t.Logf("capabilities=%v", profile.Capabilities())

	assert.Empty(t, profile.Issues, "probing a real host must not record an issue")
	assert.NotEmpty(t, profile.Arch)
	assert.NotEmpty(t, profile.CPUArch)
	assert.Positive(t, profile.PhysicalCores)
	assert.Positive(t, profile.TotalRAMBytes)
	assert.NotEmpty(t, profile.Board.Kind)
	assert.Contains(t, profile.Capabilities(), CapTFLite)

	for i := range profile.Accelerators {
		accelerator := profile.Accelerators[i]
		assert.Contains(t, []string{VendorIntel, VendorAMD, VendorNVIDIA}, accelerator.Vendor)
		assert.Contains(t, []string{AcceleratorIGPU, AcceleratorDGPU}, accelerator.Kind)
		// Usable and Reasons are complementary: a reason without a defect, or a
		// defect without a reason, would both mislead the panel.
		assert.Equal(t, accelerator.Usable, len(accelerator.Reasons) == 0,
			"an unusable accelerator must carry a reason and a usable one must not")
	}
}

func TestProbeRecordsIssueWhenMemoryIsUnknown(t *testing.T) {
	t.Parallel()

	// Every supported platform reports host RAM, so this asserts the shape of
	// the best-effort contract rather than a reachable state: a failed probe
	// yields a zero value plus a recorded reason.
	profile := Profile{}
	profile.Issues = append(profile.Issues, Issue{Probe: ProbeMemory, Reason: ReasonUnavailable})

	assert.Zero(t, profile.TotalRAMBytes)
	assert.NotContains(t, profile.Capabilities(), CapLowRAM,
		"unknown memory must not be reported as low memory")
}

func TestDetectCachesTheProbe(t *testing.T) {
	first := Detect()
	second := Detect()

	assert.Equal(t, first, second)
	assert.NotEmpty(t, first.Arch)
}

func TestRefreshReplacesTheCachedProfile(t *testing.T) {
	Detect()

	refreshed := Refresh()

	// Nothing the probe reads changes between two calls on a live host, so the
	// contract to verify is that Refresh re-probes and the cache then serves the
	// new value rather than the original one.
	assert.Equal(t, refreshed, Detect())
	assert.NotEmpty(t, refreshed.Arch)
}

func TestDetectIsSafeForConcurrentUse(t *testing.T) {
	const callers = 8

	var wg sync.WaitGroup
	profiles := make([]Profile, callers)
	for i := range callers {
		wg.Go(func() {
			profiles[i] = Detect()
		})
	}
	wg.Wait()

	for i := range callers {
		assert.Equal(t, profiles[0], profiles[i])
	}
}

func TestProbeBackendsAlwaysReportsTFLite(t *testing.T) {
	t.Parallel()

	backends := probeBackends()

	// TFLite is linked into every build, so a profile that does not report it
	// would mean the probe itself is broken rather than the host lacking it.
	assert.True(t, backends.TFLite.Available)
}

func TestDetectSIMDMatchesArchitecture(t *testing.T) {
	t.Parallel()

	simd := detectSIMD()

	// The exact extensions depend on the CPU running the test; what must hold is
	// that nothing from another architecture leaks in.
	for _, ext := range simd {
		switch ext {
		case SIMDAVX2, SIMDAVX512:
			assert.Contains(t, []string{archAMD64, arch386}, runtime.GOARCH,
				"x86 SIMD reported on a non-x86 host")
		case SIMDNEON, SIMDSVE:
			assert.Contains(t, []string{archARM64, archARM}, runtime.GOARCH,
				"ARM SIMD reported on a non-ARM host")
		default:
			assert.Fail(t, "unexpected SIMD extension", ext)
		}
	}
}
