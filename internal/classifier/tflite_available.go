//go:build !notflite

package classifier

// tfliteBackendAvailable reports whether this build links the TFLite backend.
// True for normal builds; false for ONNX-only builds (notflite tag).
const tfliteBackendAvailable = true
