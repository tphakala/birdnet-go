//go:build !notflite

package hwprofile

// tfliteLinked reports whether this build links the TFLite backend. True for
// normal builds; the notflite tag produces an ONNX-only build where it is false.
//
// It mirrors classifier.tfliteBackendAvailable, which is unexported and so
// cannot be reused here. Probing it matters because the tflite capability token
// is a join key against the published model manifests: reporting it on a build
// with no TFLite runtime would offer the user models the binary cannot execute.
const tfliteLinked = true
