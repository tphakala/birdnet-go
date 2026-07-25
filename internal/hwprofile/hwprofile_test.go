package hwprofile

import (
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/mempolicy"
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
				// Accessibility depends on this host's /dev/dri, so only the
				// hardware facts are asserted here; the reason codes are covered
				// by TestApplyAccessibility against explicit render-node states.
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
				// The effective ceiling is the smaller of host RAM and the cgroup
				// cap, so this only demonstrates the cgroup path on a host with
				// more than the fixture's 512 MiB. This project's own low-memory
				// regression VM has exactly 512 MB, so the guard is not
				// hypothetical.
				if hostRAM := mempolicy.DetectTotalMemoryAt(t.TempDir()); hostRAM <= cgroupLimitBytes {
					t.Skipf("host RAM (%d bytes) is not above the fixture's cgroup cap, so the cap is not the binding limit", hostRAM)
				}
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

			profile := probeHardware(root).WithBackends(probeBackends())

			// Invariants that hold on every host: probing is best-effort and
			// never leaves the profile unusable.
			assert.Empty(t, profile.Issues, "no fixture tree should make a probe fail")
			assert.NotEmpty(t, profile.Arch)
			assert.Positive(t, profile.PhysicalCores)
			assert.Positive(t, profile.TotalRAMBytes)
			assert.NotEmpty(t, profile.Capabilities(), "every host matches at least an architecture token")

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

	profile := probeHardware(rootFS).WithBackends(probeBackends())

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

	for i := range profile.Accelerators {
		accelerator := profile.Accelerators[i]
		assert.Contains(t, []string{VendorIntel, VendorAMD, VendorNVIDIA}, accelerator.Vendor)
		assert.Contains(t, []string{AcceleratorIGPU, AcceleratorDGPU}, accelerator.Kind)
		// Usable and Reasons are complementary: a reason without a defect, or a
		// defect without a reason, would both mislead the panel.
		assert.Equal(t, accelerator.Accessible, !slices.Contains(accelerator.Reasons, ReasonRenderNodeUnavailable) &&
			!slices.Contains(accelerator.Reasons, ReasonRenderNodePermission),
			"a render-node reason and Accessible must never disagree")
	}
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

	// Drop the cache so every goroutine races on the FIRST probe, which is the
	// only path the mutex exists to serialise. Without this the earlier
	// non-parallel tests have already warmed it, all eight callers merely read,
	// and removing the lock entirely still passes under -race.
	hardwareMu.Lock()
	cachedHardware = nil
	hardwareMu.Unlock()

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

func TestProbeBackendsReportsTFLiteFromTheBuildTag(t *testing.T) {
	t.Parallel()

	backends := probeBackends()

	// Whether TFLite is linked is a compile-time fact, so the probe must report
	// exactly what the build decided. Emitting the tflite capability token on a
	// notflite build would offer the user models the binary cannot execute.
	assert.Equal(t, tfliteLinked, backends.TFLite.Available)
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

// TestHardwareProfileDoesNotShareSlicesWithTheCache is the regression test for
// the aliasing class: a struct copy duplicates slice headers, not the arrays
// behind them, so a caller appending to anything on a returned profile would
// write into the process-global cache and every later caller would see it.
//
// Reasons is the subtle one. It is nested inside Accelerator, so cloning the
// accelerator slice is not enough, and applyAccessibility builds it with spare
// capacity, which is exactly what makes a stray append land in the cache
// instead of allocating.
func TestHardwareProfileDoesNotShareSlicesWithTheCache(t *testing.T) {
	// Not parallel: it mutates the package-level cache.
	hardwareMu.Lock()
	cachedHardware = &Profile{
		SIMD:   append(make([]string, 0, 2), SIMDAVX2),
		Issues: append(make([]Issue, 0, 2), Issue{Probe: ProbeBoard, Reason: ReasonReadFailed}),
		Accelerators: []Accelerator{{
			Vendor:  VendorAMD,
			Reasons: append(make([]string, 0, 2), ReasonNoRuntime),
		}},
	}
	hardwareMu.Unlock()
	t.Cleanup(func() {
		hardwareMu.Lock()
		cachedHardware = nil
		hardwareMu.Unlock()
	})

	// Writing an existing element, which is what sorting or normalising a
	// returned slice does. An append would not do: these slices carry spare
	// capacity, so an append lands past the cached slice's length where a
	// read of the cache can never observe it, and the test would pass against
	// the very sharing it is meant to catch.
	first := hardwareProfile(false)
	first.SIMD[0] = "scribble"
	first.Issues[0] = Issue{Probe: ProbeMemory, Reason: ReasonUnavailable}
	first.Accelerators[0].Reasons[0] = "scribble"

	second := hardwareProfile(false)

	assert.Equal(t, []string{SIMDAVX2}, second.SIMD)
	assert.Equal(t, []Issue{{Probe: ProbeBoard, Reason: ReasonReadFailed}}, second.Issues)
	assert.Equal(t, []string{ReasonNoRuntime}, second.Accelerators[0].Reasons,
		"a write through a returned accelerator must not reach the cached one")
}
