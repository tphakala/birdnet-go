//go:build notflite

package hwprofile

// tfliteLinked reports whether this build links the TFLite backend. False here:
// the notflite tag produces a strictly-ONNX build with no TFLite runtime, so
// the tflite capability token must not be emitted.
const tfliteLinked = false
