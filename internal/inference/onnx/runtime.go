//go:build onnx

// Package onnx provides inference for bird species classification models
// (BirdNET, Google Perch, and others) using ONNX Runtime.
//
// The caller is responsible for initializing the ONNX Runtime environment
// before creating any Classifier or RangeFilter instances. Use MustInitORT
// for simple applications, or call ort.SetSharedLibraryPath and
// ort.InitializeEnvironment directly for full control.
package onnx

import ort "github.com/yalue/onnxruntime_go"

// MustInitORT initializes the ONNX Runtime with the given shared library path.
// It panics on failure. Intended for simple applications.
// For production use, call ort.SetSharedLibraryPath and ort.InitializeEnvironment directly.
func MustInitORT(libraryPath string) {
	ort.SetSharedLibraryPath(libraryPath)
	if err := ort.InitializeEnvironment(); err != nil {
		panic("onnxruntime: failed to initialize ONNX Runtime: " + err.Error())
	}
}

// DestroyORT tears down the ONNX Runtime environment.
// Call this when completely done with all classifiers and range filters.
func DestroyORT() error {
	return ort.DestroyEnvironment()
}
