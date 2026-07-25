package hwprofile

import (
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TFLiteLinked reports whether this build links the TensorFlow Lite backend.
// It is a compile-time fact, decided by the notflite build tag, and is exported
// so a caller assembling its own Backends does not have to hardcode it: doing so
// emits the tflite capability token on a build that cannot execute those models.
func TFLiteLinked() bool { return tfliteLinked }

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
// probe would make the probe write to disk on first use.
//
// A caller that holds the configured path therefore must not use this result.
// It should build its own Backends and pass them to Profile.WithBackends, so
// one probe decides every field it reports; otherwise a user with a custom
// library path silently loses the onnxruntime-cpu token.
func probeBackends() Backends {
	// TFLite needs no runtime probe: whether it is linked is decided at compile
	// time by the notflite build tag.
	backends := Backends{TFLite: BackendStatus{Available: tfliteLinked}}

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
