//go:build !noembed

package birdnet

import (
	_ "embed" // Embedding data directly into the binary.
)

// This file is included by default (when NOT using -tags noembed)
// It provides the embedded TensorFlow Lite models for a monolithic build.

// Embedded TensorFlow Lite model data.
//
//go:embed data/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite
var modelData []byte

// Embedded TensorFlow Lite range filter model data.
//
//go:embed data/BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite
var metaModelDataV1 []byte

//go:embed data/BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite
var metaModelDataV2 []byte

// hasEmbeddedModels indicates whether models are embedded in the binary
// This is a var instead of const to allow test overrides
var hasEmbeddedModels = true