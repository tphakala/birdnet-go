package hwprofile

import (
	"github.com/tphakala/birdnet-go/internal/inference"
)

// deviceCPU is the OpenVINO device name for the host CPU.
const deviceCPU = "CPU"

// openvinoDevices are the OpenVINO device names worth probing. OpenVINO
// enumerates more (AUTO, MULTI, HETERO), but those are dispatch pseudo-devices
// rather than hardware, and nothing in the model catalog selects on them.
var openvinoDevices = []string{deviceCPU, deviceGPU}

// probeBackends reports which inference backends this binary can reach.
//
// The ONNX Runtime probe deliberately passes an empty library path, so it
// searches the default locations rather than a user-configured one: this
// package has no settings dependency, and loading settings from a hardware
// probe would make the probe write to disk on first use. A caller that holds
// the configured path (the inference status endpoint does) overrides
// Backends.ONNX.Available on its copy of the Profile before deriving tokens.
func probeBackends() Backends {
	// TFLite is compiled into every build, so it needs no probe.
	backends := Backends{TFLite: BackendStatus{Available: true}}

	ort := inference.CheckORTAvailability("")
	backends.ONNX = BackendStatus{
		Available:   ort.Available,
		Initialized: ort.Initialized,
		Version:     ort.Version,
	}

	openvino := inference.CheckOpenVINOAvailability()
	backends.OpenVINO = OpenVINOStatus{Supported: openvino.Supported, Active: openvino.Active}
	if openvino.Supported {
		for _, device := range openvinoDevices {
			if inference.OpenVINOHasDevice(device) {
				backends.OpenVINO.Devices = append(backends.OpenVINO.Devices, device)
			}
		}
	}

	return backends
}
