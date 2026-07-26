//go:build notflite

package classifier

// tfliteBackendAvailable reports whether this build links the TFLite backend.
// False here: strictly-ONNX builds (the notflite tag) have no TFLite classifier or
// range filter, so paths that would fall back to the embedded TFLite range filter
// must be disabled.
const tfliteBackendAvailable = false
