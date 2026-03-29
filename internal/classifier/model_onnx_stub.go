//go:build !onnx

package classifier

import "fmt"

// initializeONNXModel returns an error when built without ONNX support.
func (bn *BirdNET) initializeONNXModel() error {
	return fmt.Errorf("ONNX model specified but binary was built without ONNX support (build with -tags onnx)")
}

// initializeONNXMetaModel returns an error when built without ONNX support.
func (bn *BirdNET) initializeONNXMetaModel() error {
	return fmt.Errorf("ONNX range filter model specified but binary was built without ONNX support (build with -tags onnx)")
}

// isONNXSupported returns false when the binary is built without ONNX support.
func isONNXSupported() bool {
	return false
}
