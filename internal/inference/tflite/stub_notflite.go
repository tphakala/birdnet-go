//go:build notflite

package tflite

// This file provides stub implementations of the TFLite backend for builds with
// the notflite tag (a strictly-ONNX build with no libtensorflowlite_c). The stubs
// keep the same exported API as the real backend but return an error and, most
// importantly, do not import go-tflite, so the binary does not link
// libtensorflowlite_c.

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/inference"
)

// LogFunc is a callback for logging messages from the inference backend.
type LogFunc func(msg string)

// TFLiteClassifierOptions configures the TFLite species classifier. Retained for
// API compatibility with non-notflite builds; ignored here.
type TFLiteClassifierOptions struct {
	Threads    int
	UseXNNPACK bool
	ErrorFunc  LogFunc
	WarnFunc   LogFunc
}

// errTFLiteUnavailable explains that this build has no TFLite backend.
func errTFLiteUnavailable() error {
	return fmt.Errorf("TFLite backend not available: this binary was built without TFLite (notflite tag); use an ONNX model")
}

// NewTFLiteClassifier always returns an error in notflite builds.
func NewTFLiteClassifier(_ []byte, _ TFLiteClassifierOptions) (inference.Classifier, int, error) {
	return nil, 0, errTFLiteUnavailable()
}

// NewTFLiteRangeFilter always returns an error in notflite builds.
func NewTFLiteRangeFilter(_ []byte, _ LogFunc) (inference.RangeFilter, error) {
	return nil, errTFLiteUnavailable()
}
