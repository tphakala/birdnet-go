//go:build !openvino

package openvino

// This file is built when the "openvino" tag is absent. Every entry point
// reports ErrOpenVINOUnavailable so the classifier falls back to ORT and no
// libopenvino_c symbol is referenced.

// NewClassifier always fails without the openvino build tag.
func NewClassifier(_ string, _ Options) (Classifier, error) {
	return nil, ErrOpenVINOUnavailable
}

// InitOV always fails without the openvino build tag.
func InitOV(_ string) error { return ErrOpenVINOUnavailable }

// DestroyOV always fails without the openvino build tag.
func DestroyOV() error { return ErrOpenVINOUnavailable }
