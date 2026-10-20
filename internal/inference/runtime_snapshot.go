package inference

import (
	"sync/atomic"

	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
	ortlib "github.com/yalue/onnxruntime_go"
)

// Probe cache states reported in OpenVINORuntime.ProbeStatus.
const (
	// OVProbeStatusNotRun means no device probe has produced a result yet.
	OVProbeStatusNotRun = "not_run"
	// OVProbeStatusInFlight means a device probe child is running right now.
	OVProbeStatusInFlight = "in_flight"
	// OVProbeStatusCached means a probe result (devices or a failure) is cached.
	OVProbeStatusCached = "cached"
)

// ORTRuntime is a point-in-time record of the ONNX Runtime state, readable
// without waiting on any initialization lock.
type ORTRuntime struct {
	// Initialized is true while the runtime is initialized.
	Initialized bool
	// Version is the runtime version reported by the library once initialized.
	Version string
	// LibraryPath is exactly the string handed to the binding. It can be a bare
	// dlopen name rather than an absolute path.
	LibraryPath string
	// LibraryConfigured is true when the path came from configuration and false
	// when it was found by the automatic library search.
	LibraryConfigured bool
}

// OpenVINORuntime is a point-in-time record of the OpenVINO backend state,
// readable without waiting on any initialization or compile lock.
type OpenVINORuntime struct {
	// CompiledIn is true when this build links the OpenVINO backend.
	CompiledIn bool
	// Initialized is true while the OpenVINO core is loaded.
	Initialized bool
	// Active is true while at least one classifier runs on OpenVINO.
	Active bool
	// LibraryPath is the path handed to the dlopen that bound libopenvino_c;
	// empty means the default soname. Meaningful only when LibraryLoaded.
	LibraryPath string
	// LibraryLoaded is true once libopenvino_c has been bound.
	LibraryLoaded bool
	// ProbeStatus is one of the OVProbeStatus constants.
	ProbeStatus string
	// ProbeDevices lists the devices the cached probe found.
	ProbeDevices []string
	// ProbeFailed is true when the cached probe result is a failure. The error
	// text is deliberately not copied: it can embed child process output.
	ProbeFailed bool
}

var (
	// ortRuntimeSnap is written under ortInitMu and read lock-free.
	ortRuntimeSnap atomic.Pointer[ORTRuntime]
	// ovInitializedSnap mirrors ovInitialized for lock-free readers.
	ovInitializedSnap atomic.Bool
)

// publishORTRuntime records a successful ONNX Runtime init. The caller holds
// ortInitMu and has just initialized the runtime, which is when GetVersion is valid.
func publishORTRuntime(libraryPath string, configured bool) {
	ortRuntimeSnap.Store(&ORTRuntime{
		Initialized:       true,
		Version:           ortlib.GetVersion(),
		LibraryPath:       libraryPath,
		LibraryConfigured: configured,
	})
}

// clearORTRuntime drops the record after a successful ONNX Runtime destroy.
func clearORTRuntime() {
	ortRuntimeSnap.Store(nil)
}

// ORTRuntimeSnapshot returns the ONNX Runtime state recorded at init. It is an
// atomic load, never waits on ortInitMu, and returns the zero value when the
// runtime is not initialized.
func ORTRuntimeSnapshot() ORTRuntime {
	if snap := ortRuntimeSnap.Load(); snap != nil {
		return *snap
	}
	return ORTRuntime{}
}

// OpenVINORuntimeSnapshot returns the OpenVINO state without taking ovInitMu or
// the backend's compile lock. The probe fields come from the probe cache, whose
// mutex is never held while a probe child runs.
func OpenVINORuntimeSnapshot() OpenVINORuntime {
	path, loaded := ov.LoadedLibraryPath()
	devices, status, err := cachedOVProbeDevices()
	return OpenVINORuntime{
		CompiledIn:    ov.Supported,
		Initialized:   ovInitializedSnap.Load(),
		Active:        ovActiveClassifiers.Load() > 0,
		LibraryPath:   path,
		LibraryLoaded: loaded,
		ProbeStatus:   probeStatusName(status),
		ProbeDevices:  devices,
		ProbeFailed:   err != nil,
	}
}

// probeStatusName maps the probe cache state to its reported name.
func probeStatusName(status ovProbeCacheStatus) string {
	switch status {
	case ovProbeInFlight:
		return OVProbeStatusInFlight
	case ovProbeCached:
		return OVProbeStatusCached
	default:
		return OVProbeStatusNotRun
	}
}
