// Package onnx provides inference for bird species classification models
// (BirdNET, Google Perch, and others) using ONNX Runtime.
//
// The caller is responsible for initializing the ONNX Runtime environment
// before creating any Classifier or RangeFilter instances. Use InitORT (or
// MustInitORT in tests and small tools). Do not call the binding's
// SetSharedLibraryPath and Init directly: that skips the telemetry opt-out.
package onnx

import ort "github.com/microsoft/onnxruntime/go/onnxruntime"

// SessionOptions is the binding's session options type. Callers that tune a
// session (thread counts, execution providers) take it through this alias so
// that callers outside the session owners (the VAD session and the heatmap
// service import the binding themselves) need not import it.
type SessionOptions = ort.SessionOptions

// Version returns the version string of the loaded ONNX Runtime library, or
// an empty string when the library is not initialized.
func Version() string {
	v, err := ort.GetVersion()
	if err != nil {
		return ""
	}
	return v
}

// InitORT initializes the ONNX Runtime with the given shared library path.
// It switches ONNX Runtime telemetry off first, unless the user set
// ORT_DISABLE_TELEMETRY, and returns the opt-out state for logging. The
// initialization error is returned unwrapped so callers can match its text.
func InitORT(libraryPath string) (TelemetryOptOut, error) {
	optOut, err := ensureTelemetryOptOut()
	if err != nil {
		return optOut, err
	}
	ort.SetSharedLibraryPath(libraryPath)
	if err := ort.Init(); err != nil {
		return optOut, err
	}
	// Windows official builds use ETW TraceLogging, which ignores the variable;
	// on 1DS builds the call is harmless after the opt-out has latched.
	if optOut.Disabled {
		optOut.APIDisabled = ort.DisableTelemetry() == nil
	}
	return optOut, nil
}

// MustInitORT initializes the ONNX Runtime with the given shared library path.
// It panics on failure. Intended for tests and small tools; production code
// should call InitORT and handle the error.
func MustInitORT(libraryPath string) {
	if _, err := InitORT(libraryPath); err != nil {
		panic("onnxruntime: failed to initialize ONNX Runtime: " + err.Error())
	}
}

// DestroyORT tears down the ONNX Runtime environment. The teardown is terminal
// for the process: the binding refuses to initialize again afterwards. It
// fails while any session is still open, so close every session first
// (classifiers, range filter, VAD and the heatmap service).
func DestroyORT() error {
	return ort.Shutdown()
}
