//go:build onnx

package classifier

// Compile-time check that Perch implements ModelInstance.
var _ ModelInstance = (*Perch)(nil)
